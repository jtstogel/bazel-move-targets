package movetargets

import (
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/bazelbuild/buildtools/build"
)

// dstInput is what buildDst needs from the source file besides the move.
type dstInput struct {
	srcStmts []build.Expr      // the source file's statements before editing
	exports  []*build.CallExpr // exports_files calls for the destination
	loads    []*build.LoadStmt // loads needed by the moved rules and statements
	vars     map[string]bool   // top-level variables that stay in the source file
}

// buildDst creates or extends the destination BUILD file. It returns the
// file's path, the new file and its old contents (nil if it is new).
func buildDst(ws *workspace, pm *plannedMove, in dstInput, rep *report) (string, *build.File, []byte, error) {
	for _, r := range pm.rules {
		moveRule(r, pm, in.vars, rep)
	}
	moved := movedExprs(pm, in)
	if p, ok := ws.buildFiles[pm.labels.dst]; ok {
		return appendToExisting(ws, p, pm, in, moved, rep)
	}
	p := pm.labels.dst + "/" + path.Base(pm.src.path)
	return p, newDstFile(pm, in, moved, p), nil, nil
}

// movedExprs returns the exports, statements and rules respelled for the
// destination, in that order: statements may define variables the rules use.
func movedExprs(pm *plannedMove, in dstInput) []build.Expr {
	var moved []build.Expr
	for _, c := range in.exports {
		moveStrings(c, pm.labels, pm.attrs)
		moved = append(moved, c)
	}
	for _, st := range pm.stmts {
		moveStatement(st, pm.labels, pm.attrs)
		moved = append(moved, st)
	}
	for _, r := range pm.rules {
		moved = append(moved, r.call)
	}
	return moved
}

// appendToExisting adds the moved statements to the destination package's
// existing BUILD file, after rewriting its references and merging the loads.
func appendToExisting(ws *workspace, p string, pm *plannedMove, in dstInput, moved []build.Expr, rep *report) (string, *build.File, []byte, error) {
	f, data, err := ws.parse(p)
	if err != nil {
		return "", nil, nil, err
	}
	rewriteRefs(f, p, pm.labels.dst, pm.labels, pm.attrs, rep)
	mergeLoads(f, pm.labels.dst, in.loads, p, rep)
	f.Stmt = append(f.Stmt, moved...)
	return p, f, data, nil
}

// newDstFile builds a BUILD file from the source's header comment, the
// --package_comment, the loads, package() and licenses(), then moved.
func newDstFile(pm *plannedMove, in dstInput, moved []build.Expr, p string) *build.File {
	f := &build.File{Path: p, Type: build.TypeBuild}
	if len(in.srcStmts) > 0 {
		if cb, ok := in.srcStmts[0].(*build.CommentBlock); ok {
			f.Stmt = append(f.Stmt, cb)
		}
	}
	if pm.cfg.PackageComment != "" {
		comment := build.Comment{Token: "# " + pm.cfg.PackageComment}
		f.Stmt = append(f.Stmt, &build.CommentBlock{Comments: build.Comments{After: []build.Comment{comment}}})
	}
	for _, l := range in.loads {
		f.Stmt = append(f.Stmt, l)
	}
	for _, fn := range []string{
		"package",
		"licenses",
	} {
		if c := findCall(in.srcStmts, fn); c != nil {
			c = clone(c).(*build.CallExpr)
			moveStrings(c, pm.labels, pm.attrs)
			f.Stmt = append(f.Stmt, c)
		}
	}
	f.Stmt = append(f.Stmt, moved...)
	return f
}

// moveStrings respells the strings of a statement moved from the source
// package to the destination.
func moveStrings(stmt build.Expr, m *labelMap, a attrKinds) {
	a.forEachString(stmt, func(str *build.StringExpr, kind strKind) {
		str.Value, _ = m.rewriteString(str.Value, kind, m.src, m.dst)
	})
}

// moveRule renames a moved rule and rewrites it to live in the destination.
func moveRule(r movedRule, pm *plannedMove, vars map[string]bool, rep *report) {
	c, t := r.call, r.target
	pos := posOf(pm.src.path, c)
	warnPackageRelative(c, pos, pm.attrs, rep)
	if !t.Alias && !hasFileAttr(c, pm.attrs) {
		rep.warnf("no-file-attrs", pos, "%q has no file attributes; its macro may compute package-relative paths", t.Name)
	}
	for _, id := range slices.Sorted(maps.Keys(identsUsed([]build.Expr{c}))) {
		if vars[id] {
			rep.warnf("dangling-toplevel-var", pos, "%q uses the top-level variable %s, which stays in //%s", t.Name, id, pm.labels.src)
		}
	}
	if name, ok := build.NewRule(c).Attr("name").(*build.StringExpr); ok {
		name.Value = t.NewName
	}
	rerootGlobs(c, pm.labels.rel)
	for _, out := range pm.attrs.outputs(c) {
		out.Value = strings.TrimPrefix(out.Value, pm.labels.rel+"/")
	}
	moveStrings(c, pm.labels, pm.attrs)
	c.After = nil // Trailing comments stay in the source file.
}

// warnPackageRelative warns about attributes whose meaning is relative to the
// package directory.
func warnPackageRelative(c *build.CallExpr, pos position, a attrKinds, rep *report) {
	r := build.NewRule(c)
	for _, attr := range a.packageRelative {
		if r.Attr(attr) != nil {
			rep.warnf("package-relative-attr", pos, "%q sets %s, which is relative to the package; check it by hand", r.Name(), attr)
		}
	}
	if copts := r.Attr("copts"); copts != nil {
		walkAny(copts, func(s *build.StringExpr, _ strKind) {
			if strings.HasPrefix(s.Value, "-I") {
				rep.warnf("package-relative-attr", pos, "%q has copt %q, which may be relative to the package; check it by hand", r.Name(), s.Value)
			}
		})
	}
}

func hasFileAttr(c *build.CallExpr, a attrKinds) bool {
	for _, key := range build.NewRule(c).AttrKeys() {
		if a.kindOf(key) == fileString {
			return true
		}
	}
	return false
}

// findCall returns the first top-level call of the given function, or nil.
func findCall(stmts []build.Expr, fn string) *build.CallExpr {
	for _, st := range stmts {
		if c, ok := st.(*build.CallExpr); ok && callName(c) == fn {
			return c
		}
	}
	return nil
}
