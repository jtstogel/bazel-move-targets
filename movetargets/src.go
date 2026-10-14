package movetargets

import (
	"slices"

	"github.com/bazelbuild/buildtools/build"
	"github.com/bazelbuild/buildtools/labels"
)

// srcStmt is a statement of the edited source file, or the gap left by a
// removed one until empty sections are cleaned up.
type srcStmt struct {
	expr    build.Expr
	deleted bool
}

// editSrc removes the moved rules and statements from the source file, leaving
// aliases where configured, and returns the exports_files calls that move to
// the destination. loaded lists the load-bound symbols used by what moved.
func editSrc(pm *plannedMove, loaded map[string]bool, rep *report) []*build.CallExpr {
	s, m := pm.src, pm.labels
	moved := map[string]target{}
	for _, r := range pm.rules {
		moved[r.target.Name] = r.target
	}
	var stmts []srcStmt
	for _, st := range s.file.Stmt {
		stmts = append(stmts, replacementFor(st, s, moved, pm.stmts, m.dst)...)
	}
	for _, t := range pm.cfg.ExtraNames {
		if t.Alias {
			stmts = append(stmts, srcStmt{expr: newAlias(nil, t, m.dst)})
		}
	}
	s.file.Stmt = dropEmptySections(stmts, s.path, rep)
	warnMovedVarsUsed(s, pm.stmts, m.dst, rep)
	exports := moveExports(s.file, m)
	rewriteRefs(s.file, s.path, m.src, m, pm.attrs, rep)
	pruneLoads(s.file, loaded)
	return exports
}

// replacementFor returns what takes a statement's place in the source file:
// the statement itself if it stays; otherwise its trailing comments, then an
// alias or a deletion mark.
func replacementFor(st build.Expr, s *srcPackage, moved map[string]target, movedStmts []build.Expr, dst string) []srcStmt {
	var t target
	isTarget := false
	if c, ok := st.(*build.CallExpr); ok && s.rules[ruleName(c)] == c {
		t, isTarget = moved[ruleName(c)]
	}
	if !isTarget && !slices.Contains(movedStmts, st) {
		return []srcStmt{{expr: st}}
	}
	var out []srcStmt
	if _, ok := st.(*build.CommentBlock); !ok && len(st.Comment().After) > 0 {
		// Keep comments that trail the statement, e.g. the next section's header.
		out = append(out, srcStmt{expr: &build.CommentBlock{Comments: build.Comments{After: st.Comment().After}}})
	}
	if t.Alias {
		return append(out, srcStmt{expr: newAlias(build.NewRule(st.(*build.CallExpr)).Attr("visibility"), t, dst)})
	}
	return append(out, srcStmt{deleted: true})
}

func warnMovedVarsUsed(s *srcPackage, movedStmts []build.Expr, dst string, rep *report) {
	vars := map[string]bool{}
	for _, st := range movedStmts {
		if as, ok := st.(*build.AssignExpr); ok {
			vars[identName(as.LHS)] = true
		}
	}
	for _, st := range s.file.Stmt {
		if v := firstUsed(st, vars); v != "" {
			rep.warnf("moved-var-still-used", posOf(s.path, st), "this statement uses %s, which moved to //%s", v, dst)
		}
	}
}

func newAlias(visibility build.Expr, t target, dst string) *build.CallExpr {
	args := []build.Expr{
		kwarg("name", &build.StringExpr{Value: t.Name}),
		kwarg("actual", &build.StringExpr{Value: labels.Label{Package: dst, Target: t.NewName}.Format()}),
	}
	if visibility != nil {
		args = append(args, kwarg("visibility", clone(visibility)))
	}
	return &build.CallExpr{X: &build.Ident{Name: "alias"}, List: args}
}

func kwarg(name string, value build.Expr) *build.AssignExpr {
	return &build.AssignExpr{LHS: &build.Ident{Name: name}, Op: "=", RHS: value}
}

// clone deep-copies an expression by printing and re-parsing it.
func clone(e build.Expr) build.Expr {
	f, err := build.ParseBuild("clone", []byte(build.FormatString(e)))
	if err != nil || len(f.Stmt) != 1 {
		panic("cannot re-parse " + build.FormatString(e))
	}
	return f.Stmt[0]
}

// dropEmptySections removes deleted statements, and comment blocks whose
// section (up to the next comment block) was entirely deleted.
func dropEmptySections(stmts []srcStmt, filePath string, rep *report) []build.Expr {
	var out []build.Expr
	for i, st := range stmts {
		if st.deleted {
			continue
		}
		if cb, ok := st.expr.(*build.CommentBlock); ok && len(cb.After) > 0 && sectionDeleted(stmts[i+1:]) {
			first := cb.After[0]
			rep.warnf("deleted-section-header", posOf(filePath, first), "removed the header %q; its section is now empty", first.Token)
			continue
		}
		out = append(out, st.expr)
	}
	return out
}

// sectionDeleted reports whether every statement up to the next comment
// block was deleted, and there was at least one.
func sectionDeleted(rest []srcStmt) bool {
	n := 0
	for _, st := range rest {
		if st.deleted {
			n++
			continue
		}
		if _, ok := st.expr.(*build.CommentBlock); ok {
			break
		}
		return false
	}
	return n > 0
}

// moveExports removes exported files under the destination directory from the
// exports_files calls in f, and returns equivalent calls for the destination.
func moveExports(f *build.File, m *labelMap) []*build.CallExpr {
	var dstCalls []*build.CallExpr
	var stmts []build.Expr
	for _, st := range f.Stmt {
		c, ok := st.(*build.CallExpr)
		if !ok || callName(c) != "exports_files" {
			stmts = append(stmts, st)
			continue
		}
		dstCall, emptied := splitExports(c, m)
		if dstCall != nil {
			dstCalls = append(dstCalls, dstCall)
		}
		if !emptied {
			stmts = append(stmts, st)
		}
	}
	f.Stmt = stmts
	return dstCalls
}

// splitExports moves the files under the destination out of an exports_files
// call into a new call for the destination package, with the same other
// arguments. It returns nil if nothing moves, and whether c is now empty.
func splitExports(c *build.CallExpr, m *labelMap) (dstCall *build.CallExpr, emptied bool) {
	if len(c.List) == 0 {
		return nil, false
	}
	list, ok := c.List[0].(*build.ListExpr)
	if !ok {
		return nil, false
	}
	keep, moved := partitionExports(list.List, m)
	if len(moved) == 0 {
		return nil, false
	}
	args := []build.Expr{&build.ListExpr{List: moved}}
	for _, arg := range c.List[1:] {
		args = append(args, clone(arg))
	}
	dstCall = &build.CallExpr{X: &build.Ident{Name: "exports_files"}, List: args}
	list.List = keep
	if len(keep) == 0 {
		dstCall.Before = c.Before
		return dstCall, true
	}
	return dstCall, false
}

// partitionExports splits exported names into those that stay and those that
// move, the latter respelled for the destination.
func partitionExports(names []build.Expr, m *labelMap) (keep, moved []build.Expr) {
	for _, x := range names {
		if str, ok := x.(*build.StringExpr); ok {
			if nl, mk := m.mapLabel(labels.Label{Package: m.src, Target: str.Value}); mk == mappedFile {
				moved = append(moved, &build.StringExpr{Value: nl.Target})
				continue
			}
		}
		keep = append(keep, x)
	}
	return keep, moved
}
