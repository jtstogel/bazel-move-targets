package movetargets

import (
	"bytes"
	"embed"
	"flag"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/txtar"
)

// Each golden archive is one case with these sections:
//
//	args         one command per line; --workspace_root is added and $TMP expands to a scratch directory
//	config.json  optional; written to $TMP/config.json
//	in/PATH      the workspace before the commands
//	out/PATH     files that differ afterwards (the whole tree is compared)
//	extra/PATH   optional files outside the workspace, written to $TMP/extra/PATH
//	extra_out/PATH  extra files that differ afterwards
//	stdout       expected stdout of all commands (optional; default empty)
//	stderr       expected stderr of all commands (optional; default empty)
//	exit         exit code of each command, one per line (optional; default all 0)
//
//go:embed testdata/*.txtar
var goldenFS embed.FS

var updateDir = flag.String("update_dir", "", "rewrite the golden archives in this directory instead of comparing")

func TestGolden(t *testing.T) {
	paths, err := fs.Glob(goldenFS, "testdata/*.txtar")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no golden files: %v", err)
	}
	for _, p := range paths {
		name := strings.TrimSuffix(path.Base(p), ".txtar")
		t.Run(name, func(t *testing.T) {
			data, err := goldenFS.ReadFile(p)
			if err != nil {
				t.Fatalf("ReadFile(%q): %v", p, err)
			}
			ar := txtar.Parse(data)
			c := parseArchive(ar)
			got := runCase(t, c)
			if *updateDir != "" {
				updateGolden(t, filepath.Join(*updateDir, name+".txtar"), ar, c.in, c.extraIn, got)
				return
			}
			want := c.want(strings.Count(got.exit, "\n"))
			for _, d := range []struct{ what, diff string }{
				{what: "workspace", diff: cmp.Diff(want.tree, got.tree)},
				{what: "extra files", diff: cmp.Diff(want.extra, got.extra)},
				{what: "stdout", diff: cmp.Diff(want.stdout, got.stdout)},
				{what: "stderr", diff: cmp.Diff(want.stderr, got.stderr)},
				{what: "exit codes", diff: cmp.Diff(want.exit, got.exit)},
			} {
				if d.diff != "" {
					t.Errorf("Main(%q) %s mismatch (-want +got):\n%s", c.args, d.what, d.diff)
				}
			}
		})
	}
}

// goldenCase is the parsed form of a golden archive.
type goldenCase struct {
	args, config, stdout, stderr, exit string
	in, out, extraIn, extraOut         map[string]string
}

func parseArchive(ar *txtar.Archive) goldenCase {
	in, out := map[string]string{}, map[string]string{}
	extraIn, extraOut := map[string]string{}, map[string]string{}
	sections := map[string]string{}
	for _, f := range ar.Files {
		data := string(f.Data)
		switch {
		case strings.HasPrefix(f.Name, "in/"):
			in[strings.TrimPrefix(f.Name, "in/")] = data
		case strings.HasPrefix(f.Name, "out/"):
			out[strings.TrimPrefix(f.Name, "out/")] = data
		case strings.HasPrefix(f.Name, "extra/"):
			extraIn[strings.TrimPrefix(f.Name, "extra/")] = data
		case strings.HasPrefix(f.Name, "extra_out/"):
			extraOut[strings.TrimPrefix(f.Name, "extra_out/")] = data
		default:
			sections[f.Name] = data
		}
	}
	return goldenCase{
		args:     sections["args"],
		config:   sections["config.json"],
		stdout:   sections["stdout"],
		stderr:   sections["stderr"],
		exit:     sections["exit"],
		in:       in,
		out:      out,
		extraIn:  extraIn,
		extraOut: extraOut,
	}
}

// want returns the expected result of a case that ran n commands.
func (c goldenCase) want(n int) goldenResult {
	w := goldenResult{tree: maps.Clone(c.in), extra: maps.Clone(c.extraIn), stdout: c.stdout, stderr: c.stderr, exit: c.exit}
	maps.Copy(w.tree, c.out)
	maps.Copy(w.extra, c.extraOut)
	if w.exit == "" {
		w.exit = strings.Repeat("0\n", n)
	}
	return w
}

// runCase runs the case's commands in a scratch workspace.
func runCase(t *testing.T, c goldenCase) goldenResult {
	t.Helper()
	tmp := t.TempDir()
	root, scratch := filepath.Join(tmp, "ws"), filepath.Join(tmp, "tmp")
	writeTree(t, root, c.in)
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", scratch, err)
	}
	writeTree(t, filepath.Join(scratch, "extra"), c.extraIn)
	if c.config != "" {
		writeTree(t, scratch, map[string]string{"config.json": c.config})
	}
	var stdout, stderr bytes.Buffer
	var exits []string
	for _, line := range strings.Split(strings.TrimSpace(c.args), "\n") {
		args := splitArgs(strings.ReplaceAll(line, "$TMP", scratch))
		if len(args) == 0 {
			t.Fatalf("empty command in the args section: %q", c.args)
		}
		args = append([]string{args[0], "--workspace_root=" + root}, args[1:]...)
		exits = append(exits, strconv.Itoa(Main(args, &stdout, &stderr)))
	}
	return goldenResult{
		tree:   readTree(t, root),
		extra:  readTree(t, filepath.Join(scratch, "extra")),
		stdout: strings.ReplaceAll(stdout.String(), scratch, "$TMP"),
		stderr: strings.ReplaceAll(stderr.String(), scratch, "$TMP"),
		exit:   strings.Join(exits, "\n") + "\n",
	}
}

type goldenResult struct {
	tree, extra    map[string]string
	stdout, stderr string
	exit           string
}

func updateGolden(t *testing.T, file string, old *txtar.Archive, in, extraIn map[string]string, got goldenResult) {
	t.Helper()
	ar := &txtar.Archive{Comment: old.Comment}
	add := func(name, data string) { ar.Files = append(ar.Files, txtar.File{Name: name, Data: []byte(data)}) }
	for _, f := range old.Files {
		if f.Name == "args" || f.Name == "config.json" {
			add(f.Name, string(f.Data))
		}
	}
	addTrees := func(inPrefix, outPrefix string, in, got map[string]string) {
		for _, p := range slices.Sorted(maps.Keys(in)) {
			add(inPrefix+p, in[p])
		}
		for _, p := range slices.Sorted(maps.Keys(got)) {
			if data, ok := in[p]; !ok || data != got[p] {
				add(outPrefix+p, got[p])
			}
		}
	}
	addTrees("in/", "out/", in, got.tree)
	addTrees("extra/", "extra_out/", extraIn, got.extra)
	if got.stdout != "" {
		add("stdout", got.stdout)
	}
	if got.stderr != "" {
		add("stderr", got.stderr)
	}
	if strings.Trim(got.exit, "0\n") != "" {
		add("exit", got.exit)
	}
	if err := os.WriteFile(file, txtar.Format(ar), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", file, err)
	}
}

// splitArgs splits a command line on spaces; double quotes group words.
func splitArgs(line string) []string {
	var args []string
	var cur strings.Builder
	inQuote, inArg := false, false
	for _, r := range line {
		switch {
		case r == '"':
			inQuote, inArg = !inQuote, true
		case r == ' ' && !inQuote:
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", root, err)
	}
	for p, data := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", full, err)
		}
	}
}

func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		files[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatalf("WalkDir(%q): %v", root, err)
	}
	return files
}

func TestSplitArgs(t *testing.T) {
	in := `move --a=b  --c="x y" "z"`
	got := splitArgs(in)
	want := []string{"move", "--a=b", "--c=x y", "z"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("splitArgs(%q) mismatch (-want +got):\n%s", in, diff)
	}
}
