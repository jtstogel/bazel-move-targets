// Package movetargets moves Bazel rules from a package into a subpackage and
// rewrites the references to them.
package movetargets

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const usage = `usage:
  bazel-move-targets plan  --workspace_root=DIR [selection flags] [--out=config.json]
  bazel-move-targets apply --workspace_root=DIR --config=config.json [--dry_run]
  bazel-move-targets move  --workspace_root=DIR [selection flags] [--dry_run]
Run "bazel-move-targets <command> --help" for the flags.`

// stringList is a repeatable flag that also accepts comma-separated values.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(v string) error {
	for _, x := range strings.Split(v, ",") {
		if x = strings.TrimSpace(x); x != "" {
			*l = append(*l, x)
		}
	}
	return nil
}

type selectionFlags struct {
	src, dst, packageComment           string
	strict                             bool
	targets, include, exclude          stringList
	prefixes, aliasKinds, aliasTargets stringList
	namePrefixes, extraNames, stmts    stringList
	refuseKinds, labelAttrs, fileAttrs stringList
	packageRelativeAttrs, outputAttrs  stringList
}

func (sf *selectionFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&sf.src, "src_package", "", "package to move rules out of, e.g. //foo (required)")
	fs.StringVar(&sf.dst, "dst_package", "", "subpackage to move rules into, e.g. //foo/bar (required)")
	fs.StringVar(&sf.packageComment, "package_comment", "", "comment line for a new destination BUILD file")
	fs.BoolVar(&sf.strict, "strict_source_directories", false, "require files to be directly in the destination directory")
	fs.Var(&sf.targets, "targets", "explicit rule names (disables directory selection); @FILE reads whitespace-separated names from FILE")
	fs.Var(&sf.include, "include", "rule names to add to the directory selection")
	fs.Var(&sf.exclude, "exclude", "rule names to remove from the directory selection")
	fs.Var(&sf.namePrefixes, "include_name_prefix", "also select rules without files whose names start with this prefix")
	fs.Var(&sf.prefixes, "rename_strip_package_prefix", `prefix to strip from moved rule names; the longest match wins; "auto" derives prefixes from the destination directory (without a leading src/, as LLVM-libc names it)`)
	fs.Var(&sf.aliasKinds, "alias_for_rule_kind", "rule kind that leaves an alias() behind")
	fs.Var(&sf.aliasTargets, "alias_targets", "rule or extra names that leave an alias() behind")
	fs.Var(&sf.extraNames, "extra_names", "OLD or OLD=NEW: names with no literal rule (made by macros or comprehensions) that move too")
	fs.Var(&sf.stmts, "move_statements", "FIRST-LAST: line range of top-level statements to move verbatim")
	fs.Var(&sf.refuseKinds, "refuse_rule_kind", "rule kind never to move, in addition to the defaults")
	fs.Var(&sf.labelAttrs, "label_attrs", "attributes whose bare strings are labels, in addition to the defaults")
	fs.Var(&sf.fileAttrs, "file_attrs", "attributes whose bare strings are files, in addition to the defaults")
	fs.Var(&sf.packageRelativeAttrs, "package_relative_attrs", "attributes that are relative to the package directory and are reported when moved, in addition to the defaults")
	fs.Var(&sf.outputAttrs, "output_attrs", "attributes that name the files a rule generates, in addition to the defaults")
}

func (sf *selectionFlags) options() (planOptions, error) {
	if sf.src == "" || sf.dst == "" {
		return planOptions{}, errors.New("--src_package and --dst_package are required")
	}
	targets, err := expandTargetFiles(sf.targets)
	if err != nil {
		return planOptions{}, err
	}
	stmts, err := parseRanges(sf.stmts)
	if err != nil {
		return planOptions{}, err
	}
	src, dst := trimPkg(sf.src), trimPkg(sf.dst)
	return planOptions{
		cfg: config{
			SrcPackage:              src,
			DstPackage:              dst,
			StrictSourceDirectories: sf.strict,
			PackageComment:          sf.packageComment,
			LabelAttrs:              slices.Concat(defaultLabelAttrs(), sf.labelAttrs),
			FileAttrs:               slices.Concat(defaultFileAttrs(), sf.fileAttrs),
			RefuseRuleKinds:         slices.Concat(defaultRefuseKinds(), sf.refuseKinds),
			PackageRelativeAttrs:    slices.Concat(defaultPackageRelativeAttrs(), sf.packageRelativeAttrs),
			OutputAttrs:             slices.Concat(defaultOutputAttrs(), sf.outputAttrs),
			Statements:              stmts,
		},
		targets:      targets,
		include:      sf.include,
		exclude:      sf.exclude,
		namePrefixes: sf.namePrefixes,
		prefixes:     expandAutoPrefixes(sf.prefixes, autoPrefixDir(src, dst)),
		aliasKinds:   sf.aliasKinds,
		aliasTargets: sf.aliasTargets,
		extraNames:   sf.extraNames,
	}, nil
}

// expandTargetFiles replaces "@FILE" entries with the whitespace-separated
// names in FILE.
func expandTargetFiles(targets []string) ([]string, error) {
	var out []string
	for _, t := range targets {
		file, ok := strings.CutPrefix(t, "@")
		if !ok {
			out = append(out, t)
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		out = append(out, strings.Fields(string(data))...)
	}
	return out, nil
}

func parseRanges(specs []string) ([]lineRange, error) {
	var ranges []lineRange
	for _, s := range specs {
		a, b, ok := strings.Cut(s, "-")
		first, errA := strconv.Atoi(a)
		last, errB := strconv.Atoi(b)
		if !ok || errA != nil || errB != nil || first < 1 || last < first {
			return nil, fmt.Errorf("--move_statements=%s: want FIRST-LAST line numbers", s)
		}
		ranges = append(ranges, lineRange{First: first, Last: last})
	}
	return ranges, nil
}

// expandAutoPrefixes replaces "auto" with prefixes derived from dir: for
// __support/CPP, "__support_CPP_" and "CPP_", each also lowercased.
func expandAutoPrefixes(prefixes []string, dir string) []string {
	var out []string
	for _, p := range prefixes {
		if p != "auto" {
			out = append(out, p)
			continue
		}
		for _, s := range []string{dir, path.Base(dir)} {
			s = strings.NewReplacer("/", "_", "-", "_").Replace(s) + "_"
			out = append(out, s, strings.ToLower(s))
		}
	}
	return out
}

// invocation is a parsed command line.
type invocation struct {
	cmd        string
	root       string
	verbose    bool
	localRepos []string
	opts       planOptions // plan and move
	out        string      // plan
	configPath string      // apply
	dryRun     bool        // apply and move
	extra      extraFiles  // apply and move
}

// parseArgs parses the command line. errReported means the message is
// already on stderr.
func parseArgs(args []string, stderr io.Writer) (invocation, error) {
	if len(args) == 0 {
		return invocation{}, errors.New("missing command")
	}
	cmd := args[0]
	fset := flag.NewFlagSet("bazel-move-targets "+cmd, flag.ContinueOnError)
	fset.SetOutput(stderr)
	root := fset.String("workspace_root", "", "workspace directory (required)")
	verbose := fset.Bool("verbose", false, "also print informational items")
	var localRepos stringList
	fset.Var(&localRepos, "local_repo_names", "repository names that refer to this workspace: foo for @foo//...")
	var sel selectionFlags
	var out, configPath string
	var dryRun bool
	var extra extraFiles
	applyFlags := func() {
		fset.BoolVar(&dryRun, "dry_run", false, "print a diff instead of writing files")
		fset.Var((*stringList)(&extra.paths), "extra_file", "BUILD or .bzl file outside the workspace whose labels into it are rewritten too")
		fset.StringVar(&extra.prefix, "extra_label_prefix", "", "how --extra_file files spell the workspace root, e.g. third_party/foo")
	}
	switch cmd {
	case "plan":
		sel.register(fset)
		fset.StringVar(&out, "out", "", "config file to write (default: stdout)")
	case "apply":
		fset.StringVar(&configPath, "config", "", "config file written by plan (required)")
		applyFlags()
	case "move":
		sel.register(fset)
		applyFlags()
	default:
		return invocation{}, fmt.Errorf("unknown command %q", cmd)
	}
	if err := fset.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return invocation{}, err
		}
		return invocation{}, errReported // flag printed the message
	}
	switch {
	case fset.NArg() > 0:
		return invocation{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fset.Args(), " "))
	case *root == "":
		return invocation{}, errors.New("--workspace_root is required")
	case cmd == "apply" && configPath == "":
		return invocation{}, errors.New("--config is required")
	}
	var opts planOptions
	if cmd != "apply" {
		var err error
		if opts, err = sel.options(); err != nil {
			return invocation{}, err
		}
	}
	return invocation{
		cmd:        cmd,
		root:       *root,
		verbose:    *verbose,
		localRepos: localRepos,
		opts:       opts,
		out:        out,
		configPath: configPath,
		dryRun:     dryRun,
		extra:      extra,
	}, nil
}

// Main runs the command line and returns the exit code: 0 on success, 1 on
// errors (nothing is written when validation fails) and 2 on usage errors.
func Main(args []string, stdout, stderr io.Writer) int {
	inv, err := parseArgs(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		if !errors.Is(err, errReported) {
			fmt.Fprintln(stderr, err)
			fmt.Fprintln(stderr, usage)
		}
		return 2
	}
	ws, err := loadWorkspace(inv.root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	r := runner{ws: ws, localRepos: inv.localRepos, extra: inv.extra, stdout: stdout, rep: &report{}}
	switch inv.cmd {
	case "plan":
		err = r.plan(inv.opts, inv.out)
	case "apply":
		err = r.apply(inv.configPath, inv.dryRun)
	case "move":
		err = r.move(inv.opts, inv.dryRun)
	}
	r.rep.print(stderr, inv.verbose)
	if err != nil {
		if !errors.Is(err, errReported) {
			fmt.Fprintln(stderr, err)
		}
		return 1
	}
	return 0
}

// errReported means that the message is already on stderr: in the report, or
// printed by the flag package.
var errReported = errors.New("errors reported")

type runner struct {
	ws         *workspace
	localRepos []string
	extra      extraFiles
	stdout     io.Writer
	rep        *report
}

func (r *runner) plan(opts planOptions, out string) error {
	cfg, err := plan(r.ws, opts, r.localRepos, r.rep)
	if err != nil {
		return err
	}
	data, err := cfg.marshal()
	if err != nil {
		return err
	}
	if out == "" {
		_, err := r.stdout.Write(data)
		return err
	}
	return os.WriteFile(out, data, 0o644)
}

func (r *runner) apply(configPath string, dryRun bool) error {
	cfg, err := readConfig(configPath)
	if err != nil {
		return err
	}
	changes, err := apply(r.ws, cfg, r.localRepos, r.extra, r.rep)
	if err != nil {
		return err
	}
	return r.write(changes, dryRun)
}

func (r *runner) move(opts planOptions, dryRun bool) error {
	cfg, err := plan(r.ws, opts, r.localRepos, r.rep)
	if err != nil {
		return err
	}
	r.rep.summary = nil // apply prints its own summary.
	changes, err := apply(r.ws, cfg, r.localRepos, r.extra, r.rep)
	if err != nil {
		return err
	}
	return r.write(changes, dryRun)
}

// write writes the changed files, or prints them as a diff.
func (r *runner) write(ch changes, dryRun bool) error {
	for _, p := range slices.Sorted(maps.Keys(ch.workspace)) {
		if err := r.writeOne(p, filepath.Join(r.ws.root, filepath.FromSlash(p)), ch.workspace[p], dryRun); err != nil {
			return err
		}
	}
	for _, p := range slices.Sorted(maps.Keys(ch.extra)) {
		if err := r.writeOne(p, p, ch.extra[p], dryRun); err != nil {
			return err
		}
	}
	return nil
}

func (r *runner) writeOne(rel, full string, data []byte, dryRun bool) error {
	if dryRun {
		return r.printDiff(rel, full, data)
	}
	return writeFile(full, data)
}

// printDiff prints the diff between the file at full and data, labeled rel.
func (r *runner) printDiff(rel, full string, data []byte) error {
	old, err := os.ReadFile(full)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	diff, err := unifiedDiff(rel, old, data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(r.stdout, diff)
	return err
}

func writeFile(full string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o644)
}
