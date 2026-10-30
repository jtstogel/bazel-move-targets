package movetargets

import (
	"maps"
	"slices"
	"strings"

	"github.com/bazelbuild/buildtools/build"
)

type binding struct {
	module, name string // name is the symbol's name in module
}

// loadBindings maps each locally bound name to its load, with modules made absolute.
func loadBindings(f *build.File, pkg string) map[string]binding {
	b := map[string]binding{}
	for _, st := range f.Stmt {
		if l, ok := st.(*build.LoadStmt); ok {
			for i, to := range l.To {
				b[to.Name] = binding{module: absModule(l.Module.Value, pkg), name: l.From[i].Name}
			}
		}
	}
	return b
}

// absModule makes a package-relative module like ":defs.bzl" absolute.
func absModule(module, pkg string) string {
	if strings.HasPrefix(module, ":") {
		return "//" + pkg + module
	}
	return module
}

// identsUsed returns the identifiers read by stmts, ignoring loads and assignment targets.
func identsUsed(stmts []build.Expr) map[string]bool {
	used := map[string]bool{}
	for _, st := range stmts {
		if _, ok := st.(*build.LoadStmt); ok {
			continue
		}
		build.Walk(st, func(x build.Expr, stack []build.Expr) {
			if id, ok := x.(*build.Ident); ok && !isAssignmentTarget(x, stack) {
				used[id.Name] = true
			}
		})
	}
	return used
}

// isAssignmentTarget reports whether x is the left-hand side of its parent.
func isAssignmentTarget(x build.Expr, stack []build.Expr) bool {
	if len(stack) == 0 {
		return false
	}
	as, ok := stack[len(stack)-1].(*build.AssignExpr)
	return ok && as.LHS == x
}

// newLoads returns one load statement per module for the given local names, sorted by module.
func newLoads(names map[string]bool, bindings map[string]binding) []*build.LoadStmt {
	byModule := map[string]*build.LoadStmt{}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		b := bindings[name]
		l := byModule[b.module]
		if l == nil {
			l = &build.LoadStmt{Module: &build.StringExpr{Value: b.module}, ForceCompact: true}
			byModule[b.module] = l
		}
		l.From = append(l.From, &build.Ident{Name: b.name})
		l.To = append(l.To, &build.Ident{Name: name})
	}
	var loads []*build.LoadStmt
	for _, m := range slices.Sorted(maps.Keys(byModule)) {
		loads = append(loads, byModule[m])
	}
	return loads
}

// mergeLoads adds loads to f, extending existing loads of the same module.
// A name that f already loads from another module is reported.
func mergeLoads(f *build.File, pkg string, loads []*build.LoadStmt, filePath string, rep *report) {
	existing := loadBindings(f, pkg)
	var added []build.Expr
	for _, l := range loads {
		target := findLoad(f, pkg, l.Module.Value)
		isNew := target == nil
		if isNew {
			target = &build.LoadStmt{Module: l.Module, ForceCompact: true}
		}
		if addMissing(target, l, existing, filePath, rep) && isNew {
			added = append(added, target)
		}
	}
	f.Stmt = slices.Insert(f.Stmt, loadInsertionIndex(f), added...)
}

// addMissing copies the symbols of l that existing doesn't bind into target,
// and reports whether it copied any.
func addMissing(target, l *build.LoadStmt, existing map[string]binding, filePath string, rep *report) bool {
	n := 0
	for i, to := range l.To {
		if b, ok := existing[to.Name]; ok {
			if b.module != l.Module.Value {
				rep.errorf("load-conflict", position(filePath), "already loads %s from %s; the moved rules need it from %s", to.Name, b.module, l.Module.Value)
			}
			continue
		}
		target.From = append(target.From, l.From[i])
		target.To = append(target.To, to)
		n++
	}
	return n > 0
}

// loadInsertionIndex returns where new loads go: after the last load, or
// after the leading header comment when there are none.
func loadInsertionIndex(f *build.File) int {
	at := 0
	for i, st := range f.Stmt {
		if _, ok := st.(*build.LoadStmt); ok {
			at = i + 1
		}
	}
	if at > 0 {
		return at
	}
	for at < len(f.Stmt) {
		if _, ok := f.Stmt[at].(*build.CommentBlock); !ok {
			break
		}
		at++
	}
	return at
}

func findLoad(f *build.File, pkg, module string) *build.LoadStmt {
	for _, st := range f.Stmt {
		if l, ok := st.(*build.LoadStmt); ok && absModule(l.Module.Value, pkg) == module {
			return l
		}
	}
	return nil
}

// pruneLoads removes the candidate symbols that f no longer uses, and loads left empty.
func pruneLoads(f *build.File, candidates map[string]bool) {
	used := identsUsed(f.Stmt)
	var stmts []build.Expr
	for _, st := range f.Stmt {
		l, ok := st.(*build.LoadStmt)
		if !ok {
			stmts = append(stmts, st)
			continue
		}
		var from, to []*build.Ident
		for i := range l.To {
			if candidates[l.To[i].Name] && !used[l.To[i].Name] {
				continue
			}
			from, to = append(from, l.From[i]), append(to, l.To[i])
		}
		l.From, l.To = from, to
		if len(to) > 0 {
			stmts = append(stmts, l)
		}
	}
	f.Stmt = stmts
}
