package movetargets

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/bazelbuild/buildtools/build"
)

// srcPackage is the parsed source package.
type srcPackage struct {
	path  string // BUILD file, relative to the workspace root
	file  *build.File
	data  []byte
	rules map[string]*build.CallExpr // named top-level calls
	order []string                   // rule names in file order
}

func loadSrc(ws *workspace, pkg string) (*srcPackage, error) {
	p, ok := ws.buildFiles[pkg]
	if !ok {
		return nil, fmt.Errorf("no BUILD file for //%s", pkg)
	}
	f, data, err := ws.parse(p)
	if err != nil {
		return nil, err
	}
	rules, order := namedRules(f)
	return &srcPackage{path: p, file: f, data: data, rules: rules, order: order}, nil
}

// namedRules returns the top-level calls of f that have a literal name, by
// name and in file order.
func namedRules(f *build.File) (map[string]*build.CallExpr, []string) {
	rules := map[string]*build.CallExpr{}
	var order []string
	for _, st := range f.Stmt {
		if c, ok := st.(*build.CallExpr); ok && ruleName(c) != "" {
			rules[ruleName(c)] = c
			order = append(order, ruleName(c))
		}
	}
	return rules, order
}

// dstRuleNames returns the rule names in the destination package, if it exists.
func dstRuleNames(ws *workspace, pkg string) (map[string]bool, error) {
	p, ok := ws.buildFiles[pkg]
	if !ok {
		return map[string]bool{}, nil
	}
	f, _, err := ws.parse(p)
	if err != nil {
		return nil, err
	}
	_, names := namedRules(f)
	return toSet(names), nil
}

// newLabelMap maps what moves: the targets and extra names of cfg, the names
// and variables defined by the moved statements, and the files the moved
// statements and rules generate.
func newLabelMap(cfg *config, s *srcPackage, stmtNames map[string]bool, moved []build.Expr, a attrKinds, localRepos []string) *labelMap {
	rel, _ := relDir(cfg.SrcPackage, cfg.DstPackage)
	renamed := map[string]string{}
	for _, t := range slices.Concat(cfg.Targets, cfg.ExtraNames) {
		renamed[t.Name] = t.NewName
	}
	for name := range stmtNames {
		renamed[name] = name
	}
	for _, st := range moved {
		if c, ok := st.(*build.CallExpr); ok {
			for _, out := range a.outputs(c) {
				renamed[out.Value] = strings.TrimPrefix(out.Value, rel+"/")
			}
		}
	}
	return &labelMap{
		src:        cfg.SrcPackage,
		dst:        cfg.DstPackage,
		rel:        rel,
		renamed:    renamed,
		srcRules:   toSet(s.order),
		localRepos: toSet(localRepos),
	}
}

// movedRule is a moved rule with its entry in the config.
type movedRule struct {
	target target
	call   *build.CallExpr
}

// move is the source package of a move and what leaves it, as named by the
// config: statements by line range, rules by target. Unknown targets are
// left out; checkTargets reports them.
type move struct {
	src       *srcPackage
	stmts     []build.Expr    // statements moved verbatim, in file order
	stmtNames map[string]bool // the rule names and variables they define
	rules     []movedRule     // in config order
	moved     []build.Expr    // stmts and the rules' calls
	labels    *labelMap
	attrs     attrKinds
}

// loadMove parses the source package named by cfg. Diagnostics go to rep.
func loadMove(ws *workspace, cfg *config, localRepos []string, rep *report) (*move, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	s, err := loadSrc(ws, cfg.SrcPackage)
	if err != nil {
		return nil, err
	}
	stmts := movedStatements(s, cfg.Statements, rep)
	stmtNames := statementNames(stmts)
	a := newAttrKinds(cfg.LabelAttrs, cfg.FileAttrs, cfg.PackageRelativeAttrs, cfg.OutputAttrs)
	var rules []movedRule
	moved := slices.Clone(stmts)
	for _, t := range cfg.Targets {
		if c := s.rules[t.Name]; c != nil {
			rules = append(rules, movedRule{target: t, call: c})
			moved = append(moved, c)
		}
	}
	return &move{
		src:       s,
		stmts:     stmts,
		stmtNames: stmtNames,
		rules:     rules,
		moved:     moved,
		labels:    newLabelMap(cfg, s, stmtNames, moved, a, localRepos),
		attrs:     a,
	}, nil
}

// planOptions are the selection flags. cfg holds everything except the
// targets and extra names.
type planOptions struct {
	cfg                       config
	targets, include, exclude []string
	namePrefixes              []string
	prefixes, aliasKinds      []string
	aliasTargets, extraNames  []string
}

// explicit returns the rules named by --targets or --include.
func (o planOptions) explicit() map[string]bool {
	return toSet(slices.Concat(o.targets, o.include))
}

// plan selects the rules to move and returns the config. Diagnostics go to
// rep; the result is errReported when rep has errors.
func plan(ws *workspace, o planOptions, localRepos []string, rep *report) (*config, error) {
	mv, err := loadMove(ws, &o.cfg, localRepos, rep)
	if err != nil {
		return nil, err
	}
	o.checkRuleNames(mv.src, rep)
	cfg := o.withTargets(o.selectTargets(mv, rep), o.extraTargets())
	o.checkAliasTargets(slices.Concat(cfg.Targets, cfg.ExtraNames), rep)
	warnComprehensions(mv, rep)
	if err := checkTargets(ws, cfg, mv, rep); err != nil {
		return nil, err
	}
	if rep.hasErrors() {
		return nil, errReported
	}
	rep.summary = append(rep.summary, fmt.Sprintf("Selected %d rules to move from //%s to //%s.", len(cfg.Targets), cfg.SrcPackage, cfg.DstPackage))
	return cfg, nil
}

// withTargets returns the config for the given targets and extra names.
func (o planOptions) withTargets(targets, extraNames []target) *config {
	cfg := o.cfg
	cfg.Targets, cfg.ExtraNames = targets, extraNames
	return &cfg
}

// checkRuleNames reports names given to --targets, --include or --exclude
// that aren't rules of the source package.
func (o planOptions) checkRuleNames(s *srcPackage, rep *report) {
	for _, name := range slices.Concat(o.targets, o.include, o.exclude) {
		if s.rules[name] == nil {
			rep.errorf("unknown-target", "", "no rule named %q in //%s", name, o.cfg.SrcPackage)
		}
	}
}

// selectTargets returns the targets for the selected rules, in file order.
func (o planOptions) selectTargets(mv *move, rep *report) []target {
	explicit := o.explicit()
	aliasKinds, aliasTargets := toSet(o.aliasKinds), toSet(o.aliasTargets)
	sel := selector{
		mv:           mv,
		strict:       o.cfg.StrictSourceDirectories,
		refuse:       toSet(o.cfg.RefuseRuleKinds),
		excluded:     toSet(o.exclude),
		fileVars:     varsWithFilesUnder(mv),
		namePrefixes: o.namePrefixes,
		prefixes:     o.prefixes,
		rep:          rep,
	}
	targets := []target{} // non-nil, so that an empty plan marshals as [] rather than null
	for _, name := range mv.src.order {
		call := mv.src.rules[name]
		if mv.stmtNames[name] {
			if explicit[name] {
				rep.errorf("target-in-moved-statements", posOf(mv.src.path, call), "%q is named explicitly but also lies in a --move_statements range", name)
			}
			continue
		}
		// --targets disables directory selection. Explicitly named rules of
		// refused kinds get an error from checkTargets.
		if !explicit[name] && (len(o.targets) > 0 || !sel.selects(name, call)) {
			continue
		}
		kind := callName(call)
		targets = append(targets, target{Name: name, NewName: stripPrefix(name, o.prefixes), Kind: kind, Alias: aliasKinds[kind] || aliasTargets[name]})
	}
	return targets
}

// selector applies directory selection to one rule at a time.
type selector struct {
	mv           *move
	strict       bool
	refuse       map[string]bool // rule kinds that never move
	excluded     map[string]bool
	fileVars     map[string]bool // top-level variables that name files under the destination
	namePrefixes []string
	prefixes     []string
	rep          *report
}

// selects reports whether directory selection picks the rule, and explains
// in the report when it doesn't for a reason the user should know about.
func (sel *selector) selects(name string, call *build.CallExpr) bool {
	pos := posOf(sel.mv.src.path, call)
	rel := sel.mv.labels.rel
	files, patterns := ruleFiles(call, sel.mv.attrs, sel.mv.labels)
	pl := place(append(files, patterns...), rel, sel.strict)
	if kind := callName(call); sel.refuse[kind] {
		if pl != outside && pl != noFiles {
			sel.rep.warnf("refused-kind-skipped", pos, "%s %q is under %s/ but kind %s is never moved", kind, name, rel, kind)
		}
		return false
	}
	if v := firstUsed(call, sel.fileVars); v != "" {
		if sel.excluded[name] {
			sel.rep.warnf("files-in-variable-excluded", pos, "%q gets files under %s/ from the top-level variable %s; it stays", name, rel, v)
		} else {
			sel.rep.errorf("files-in-variable", pos, "%q gets files under %s/ from the top-level variable %s; move the variable with --move_statements, inline it, or pass --exclude=%s", name, rel, v, name)
		}
		return false
	}
	if sel.excluded[name] {
		if pl == straddles {
			sel.rep.warnf("straddling-excluded", pos, "%q has files both inside and outside %s/; it stays and will reference //%s files", name, rel, sel.mv.labels.dst)
		}
		return false
	}
	switch pl {
	case inside:
		return true
	case nested:
		sel.rep.warnf("nested-skipped", pos, "%q has files in subdirectories of %s/; skipped because of --strict_source_directories", name, rel)
	case straddles:
		sel.rep.errorf("straddling-rule", pos, "%q has files both inside and outside %s/; split it by hand, or pass --exclude=%s", name, rel, name)
	case noFiles:
		if hasAnyPrefix(name, sel.namePrefixes) {
			return true
		}
		sel.rep.infof("file-less", pos, "%q has no files and is not selected by directory", name)
		if stripPrefix(name, sel.prefixes) != name {
			sel.rep.infof("include-hint", pos, "%q matches a --rename_strip_package_prefix; consider --include=%s or --include_name_prefix", name, name)
		}
	}
	return false
}

// extraTargets returns the targets for --extra_names.
func (o planOptions) extraTargets() []target {
	aliasTargets := toSet(o.aliasTargets)
	var extra []target
	for _, spec := range o.extraNames {
		old, newName, ok := strings.Cut(spec, "=")
		if !ok {
			newName = stripPrefix(old, o.prefixes)
		}
		extra = append(extra, target{Name: old, NewName: newName, Alias: aliasTargets[old]})
	}
	return extra
}

// checkAliasTargets reports --alias_targets that name nothing in moving.
func (o planOptions) checkAliasTargets(moving []target, rep *report) {
	for _, name := range o.aliasTargets {
		if !slices.ContainsFunc(moving, func(t target) bool { return t.Name == name }) {
			rep.errorf("alias-not-moved", "", "--alias_targets=%s names nothing that moves", name)
		}
	}
}

// varsWithFilesUnder returns the top-level variables of the source file, other
// than moved ones, whose values name files under the destination directory.
func varsWithFilesUnder(mv *move) map[string]bool {
	vars := map[string]bool{}
	for _, st := range mv.src.file.Stmt {
		as, ok := st.(*build.AssignExpr)
		if !ok || mv.stmtNames[identName(as.LHS)] {
			continue
		}
		walkAny(as.RHS, func(str *build.StringExpr, _ strKind) {
			if f, ok := fileName(str.Value, fileString, mv.labels); ok && strings.HasPrefix(f, mv.labels.rel+"/") {
				vars[identName(as.LHS)] = true
			}
		})
	}
	return vars
}

// firstUsed returns the alphabetically first identifier in vars that e uses, or "".
func firstUsed(e build.Expr, vars map[string]bool) string {
	for _, id := range slices.Sorted(maps.Keys(identsUsed([]build.Expr{e}))) {
		if vars[id] {
			return id
		}
	}
	return ""
}

func hasAnyPrefix(s string, prefixes []string) bool {
	return slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(s, p) })
}

// warnComprehensions reports top-level comprehensions that mention the
// destination directory and aren't moved by --move_statements.
func warnComprehensions(mv *move, rep *report) {
	rel := mv.labels.rel
	for _, st := range mv.src.file.Stmt {
		c, ok := st.(*build.Comprehension)
		if !ok || slices.Contains(mv.stmts, st) {
			continue
		}
		mentions := false
		walkAny(c, func(str *build.StringExpr, _ strKind) {
			mentions = mentions || strings.Contains(str.Value, rel+"/")
		})
		if mentions {
			rep.warnf("comprehension-skipped", posOf(mv.src.path, c), "comprehension mentions %s/ but is not moved; use --move_statements", rel)
		}
	}
}

// checkTargets validates the targets and extra names of a config against the
// source package.
func checkTargets(ws *workspace, cfg *config, mv *move, rep *report) error {
	dstNames, err := dstRuleNames(ws, cfg.DstPackage)
	if err != nil {
		return err
	}
	tc := &targetChecker{
		cfg:      cfg,
		mv:       mv,
		rep:      rep,
		refuse:   toSet(cfg.RefuseRuleKinds),
		dstNames: dstNames,
		seenOld:  map[string]bool{},
		seenNew:  map[string]bool{},
	}
	for _, name := range slices.Sorted(maps.Keys(mv.stmtNames)) {
		tc.checkName(target{Name: name, NewName: name}, "")
	}
	for _, t := range cfg.Targets {
		call := mv.src.rules[t.Name]
		if call == nil {
			rep.errorf("unknown-target", "", "no rule named %q in //%s", t.Name, cfg.SrcPackage)
			continue
		}
		pos := posOf(mv.src.path, call)
		tc.checkRule(t, call, pos)
		tc.checkName(t, pos)
	}
	for _, t := range cfg.ExtraNames {
		if mv.src.rules[t.Name] != nil {
			rep.errorf("extra-name-is-rule", "", "%q is a rule in //%s; list it as a target instead", t.Name, cfg.SrcPackage)
		}
		tc.checkName(t, "")
	}
	return nil
}

// targetChecker validates one target at a time, remembering the names seen so
// far to detect duplicates and collisions.
type targetChecker struct {
	cfg              *config
	mv               *move
	rep              *report
	refuse           map[string]bool // rule kinds that never move
	dstNames         map[string]bool // rules already in the destination package
	seenOld, seenNew map[string]bool
}

// checkName checks that the new name is valid and unique.
func (tc *targetChecker) checkName(t target, pos position) {
	if t.NewName == "" || strings.HasPrefix(t.NewName, ".") || strings.HasPrefix(t.NewName, "/") {
		tc.rep.errorf("bad-new-name", pos, "invalid new name %q for %q", t.NewName, t.Name)
	}
	if tc.seenOld[t.Name] {
		tc.rep.errorf("duplicate-target", pos, "%q is listed twice", t.Name)
	}
	if tc.seenNew[t.NewName] {
		tc.rep.errorf("collision", pos, "more than one rule would be named %q in //%s", t.NewName, tc.cfg.DstPackage)
	}
	if tc.dstNames[t.NewName] {
		tc.rep.errorf("collision", pos, "//%s already has a rule named %q", tc.cfg.DstPackage, t.NewName)
	}
	tc.seenOld[t.Name], tc.seenNew[t.NewName] = true, true
}

// checkRule checks that the rule has the kind the config says (if any), may
// be moved, and has all of its files under the destination directory.
func (tc *targetChecker) checkRule(t target, call *build.CallExpr, pos position) {
	kind := callName(call)
	if t.Kind != "" && kind != t.Kind {
		tc.rep.errorf("kind-mismatch", pos, "%q is a %s, but the config says %s", t.Name, kind, t.Kind)
	}
	if tc.refuse[kind] {
		tc.rep.errorf("refused-kind", pos, "%q is a %s, which is never moved", t.Name, kind)
	}
	rel := tc.mv.labels.rel
	files, patterns := ruleFiles(call, tc.mv.attrs, tc.mv.labels)
	switch place(append(files, patterns...), rel, tc.cfg.StrictSourceDirectories) {
	case straddles, outside:
		tc.rep.errorf("directory-rule", pos, "%q has files outside %s/", t.Name, rel)
	case nested:
		tc.rep.errorf("directory-rule", pos, "%q has files in subdirectories of %s/ (--strict_source_directories)", t.Name, rel)
	}
}

// stripPrefix removes the longest matching prefix from name.
func stripPrefix(name string, prefixes []string) string {
	best := ""
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) && len(p) > len(best) {
			best = p
		}
	}
	return name[len(best):]
}
