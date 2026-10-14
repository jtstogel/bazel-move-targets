package movetargets

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/bazelbuild/buildtools/build"
)

// plannedMove is a move whose targets passed validation.
type plannedMove struct {
	*move
	cfg *config
}

// prepareMove loads the source package and validates cfg against it.
// Diagnostics go to rep; the result is errReported when rep has errors.
func prepareMove(ws *workspace, cfg *config, localRepos []string, rep *report) (*plannedMove, error) {
	if len(cfg.Targets)+len(cfg.ExtraNames)+len(cfg.Statements) == 0 {
		return nil, errors.New("the config lists no targets")
	}
	mv, err := loadMove(ws, cfg, localRepos, rep)
	if err != nil {
		return nil, err
	}
	if err := checkTargets(ws, cfg, mv, rep); err != nil {
		return nil, err
	}
	if rep.hasErrors() {
		return nil, errReported
	}
	pm := &plannedMove{move: mv, cfg: cfg}
	checkStayingGlobs(pm, rep)
	checkMovedGlobs(ws, pm, rep)
	if rep.hasErrors() {
		return nil, errReported
	}
	return pm, nil
}

// changes are the new contents of changed files: workspace files by path
// relative to the root, and extra files by the path they were given as.
type changes struct {
	workspace, extra map[string][]byte
}

// apply executes cfg and returns the changed files. Diagnostics go to rep;
// the result is errReported when rep has errors.
func apply(ws *workspace, cfg *config, localRepos []string, extra extraFiles, rep *report) (changes, error) {
	pm, err := prepareMove(ws, cfg, localRepos, rep)
	if err != nil {
		return changes{}, err
	}
	s, m, a := pm.src, pm.labels, pm.attrs
	bindings := loadBindings(s.file, m.src)
	loaded := usedBindings(pm.moved, bindings)
	srcStmts := s.file.Stmt // before editSrc removes the moved ones
	exports := editSrc(pm, loaded, rep)
	in := dstInput{
		srcStmts: srcStmts,
		exports:  exports,
		loads:    newLoads(loaded, bindings),
		vars:     stayingVars(srcStmts, pm.stmtNames),
	}
	dstPath, dstFile, dstOld, err := buildDst(ws, pm, in, rep)
	if err != nil {
		return changes{}, err
	}

	out := changes{workspace: map[string][]byte{}, extra: map[string][]byte{}}
	if err := rewriteWorkspace(ws, m, a, out.workspace, rep); err != nil {
		return changes{}, err
	}
	rewriteBzl(ws, m, out.workspace, rep)
	if err := rewriteExtraFiles(extra, m, out.extra, rep); err != nil {
		return changes{}, err
	}
	if rep.hasErrors() {
		return changes{}, errReported
	}
	addIfChanged(out.workspace, s.path, s.data, s.file)
	addIfChanged(out.workspace, dstPath, dstOld, dstFile)
	rep.summary = append(rep.summary, summarize(cfg, len(pm.stmts)))
	return out, nil
}

// usedBindings returns the load-bound names that stmts use.
func usedBindings(stmts []build.Expr, bindings map[string]binding) map[string]bool {
	used := map[string]bool{}
	for id := range identsUsed(stmts) {
		if _, ok := bindings[id]; ok {
			used[id] = true
		}
	}
	return used
}

// stayingVars returns the top-level variables of stmts other than moved ones.
func stayingVars(stmts []build.Expr, moved map[string]bool) map[string]bool {
	vars := map[string]bool{}
	for _, st := range stmts {
		if as, ok := st.(*build.AssignExpr); ok && !moved[identName(as.LHS)] {
			vars[identName(as.LHS)] = true
		}
	}
	return vars
}

func summarize(cfg *config, stmts int) string {
	renamed, aliased := 0, 0
	for _, t := range slices.Concat(cfg.Targets, cfg.ExtraNames) {
		if t.NewName != t.Name {
			renamed++
		}
		if t.Alias {
			aliased++
		}
	}
	msg := fmt.Sprintf("Moved %d rules from //%s to //%s (%d renamed, %d aliased).", len(cfg.Targets), cfg.SrcPackage, cfg.DstPackage, renamed, aliased)
	if stmts > 0 || len(cfg.ExtraNames) > 0 {
		msg += fmt.Sprintf(" Also moved %d statements and %d extra names.", stmts, len(cfg.ExtraNames))
	}
	return msg
}

// checkStayingGlobs reports globs in statements that stay whose patterns
// could match files under the destination directory, which will belong to
// the new package.
func checkStayingGlobs(pm *plannedMove, rep *report) {
	for _, st := range pm.src.file.Stmt {
		if slices.Contains(pm.moved, st) {
			continue
		}
		for _, p := range includePatterns(st) {
			if globCanReach(p.Value, pm.labels.rel) {
				rep.errorf("glob-reaches-destination", posOf(pm.src.path, p), "glob pattern %q can match files under %s/, which will belong to //%s", p.Value, pm.labels.rel, pm.labels.dst)
			}
		}
	}
}

// checkMovedGlobs reports moved files and glob patterns that fall in an
// existing package below the destination, and glob patterns that don't
// start with the destination directory.
func checkMovedGlobs(ws *workspace, pm *plannedMove, rep *report) {
	gc := &globChecker{pm: pm, subs: ws.subpackagesBelow(pm.labels.dst), rep: rep}
	for _, st := range pm.moved {
		name := "statement"
		if c, ok := st.(*build.CallExpr); ok {
			name = fmt.Sprintf("%q", ruleName(c))
			gc.checkFiles(c, name)
		}
		for _, g := range globs(st) {
			gc.checkGlob(g, name)
		}
	}
}

// globChecker checks moved files and globs against the packages below the
// destination.
type globChecker struct {
	pm   *plannedMove
	subs []string // packages below the destination, relative to it
	rep  *report
}

// reachedBy returns the subpackage that a destination-relative pattern could
// match a file in, or "".
func (gc *globChecker) reachedBy(pattern string) string {
	for _, sub := range gc.subs {
		if globCanReach(pattern, sub) {
			return sub
		}
	}
	return ""
}

func (gc *globChecker) checkFiles(c *build.CallExpr, name string) {
	m := gc.pm.labels
	files, _ := ruleFiles(c, gc.pm.attrs, m)
	for _, f := range files {
		if sub := gc.reachedBy(strings.TrimPrefix(f, m.rel+"/")); sub != "" {
			gc.rep.errorf("file-in-subpackage", posOf(gc.pm.src.path, c), "%s uses %s, which is in the existing package //%s/%s", name, f, m.dst, sub)
		}
	}
}

func (gc *globChecker) checkGlob(g *build.CallExpr, name string) {
	rel := gc.pm.labels.rel
	include, exclude := globPatterns(g)
	for _, p := range include {
		rest, ok := strings.CutPrefix(p.Value, rel+"/")
		if !ok {
			gc.rep.errorf("glob-outside-destination", posOf(gc.pm.src.path, p), "glob pattern %q in %s does not start with %s/", p.Value, name, rel)
			continue
		}
		if sub := gc.reachedBy(rest); sub != "" {
			gc.rep.errorf("glob-reaches-subpackage", posOf(gc.pm.src.path, p), "glob pattern %q in %s can reach the existing package //%s/%s", p.Value, name, gc.pm.labels.dst, sub)
		}
	}
	for _, p := range exclude {
		if !strings.HasPrefix(p.Value, rel+"/") {
			gc.rep.warnf("glob-exclude-outside-destination", posOf(gc.pm.src.path, p), "exclude pattern %q in %s does not start with %s/ and will match nothing after the move", p.Value, name, rel)
		}
	}
}
