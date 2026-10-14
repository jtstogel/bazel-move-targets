package movetargets

import (
	"fmt"
	"strings"

	"github.com/bazelbuild/buildtools/build"
)

// movedStatements returns the top-level statements of s that lie inside the
// line ranges, in file order.
func movedStatements(s *srcPackage, ranges []lineRange, rep *report) []build.Expr {
	var moved []build.Expr
	seen := map[build.Expr]bool{} // ranges may overlap
	for _, r := range ranges {
		movable, overlapping := statementsInRange(s, r, rep)
		if overlapping == 0 {
			rep.errorf("empty-statement-range", "", "--move_statements=%d-%d contains no statement", r.First, r.Last)
		}
		for _, st := range movable {
			if !seen[st] {
				seen[st] = true
				moved = append(moved, st)
			}
		}
	}
	return moved
}

// statementsInRange returns the statements that can move with r, and how
// many statements overlap r at all. Overlapping statements that can't move
// are reported.
func statementsInRange(s *srcPackage, r lineRange, rep *report) (movable []build.Expr, overlapping int) {
	for _, st := range s.file.Stmt {
		if !overlaps(st, r) {
			continue
		}
		overlapping++
		if category, msg := rejectStatement(st, r); category != "" {
			rep.errorf(category, posOf(s.path, st), "%s", msg)
			continue
		}
		movable = append(movable, st)
	}
	return movable, overlapping
}

// overlaps reports whether any line of st is in r.
func overlaps(st build.Expr, r lineRange) bool {
	first, last := stmtLines(st)
	return last >= r.First && first <= r.Last
}

// rejectStatement returns why a statement overlapping r can't move: the range
// cuts through it, or it is a load, package() or licenses().
func rejectStatement(st build.Expr, r lineRange) (category, msg string) {
	first, last := stmtLines(st)
	if first < r.First || last > r.Last {
		return "statement-range-split", fmt.Sprintf("--move_statements=%d-%d cuts through the statement on lines %d-%d", r.First, r.Last, first, last)
	}
	if _, ok := st.(*build.LoadStmt); ok || isCall(st, "package") || isCall(st, "licenses") {
		return "statement-range-kind", fmt.Sprintf("--move_statements=%d-%d includes a load, package() or licenses()", r.First, r.Last)
	}
	return "", ""
}

func stmtLines(st build.Expr) (int, int) {
	if cb, ok := st.(*build.CommentBlock); ok && len(cb.After) > 0 {
		return cb.After[0].Start.Line, cb.After[len(cb.After)-1].Start.Line
	}
	start, end := st.Span()
	return start.Line, end.Line
}

func isCall(st build.Expr, fn string) bool {
	c, ok := st.(*build.CallExpr)
	return ok && callName(c) == fn
}

// statementNames returns the rule names and variables defined by moved
// statements. They keep their names in the destination.
func statementNames(stmts []build.Expr) map[string]bool {
	names := map[string]bool{}
	for _, st := range stmts {
		switch st := st.(type) {
		case *build.CallExpr:
			if n := ruleName(st); n != "" {
				names[n] = true
			}
		case *build.AssignExpr:
			names[identName(st.LHS)] = true
		}
	}
	return names
}

// moveStatement rewrites a verbatim-moved statement to live in the
// destination: labels are respelled, globs re-rooted, and other strings
// (including parts of concatenations) that start with the destination
// directory lose that prefix.
func moveStatement(st build.Expr, m *labelMap, a attrKinds) {
	rerootGlobs(st, m.rel)
	concat := concatParts(st)
	a.forEachString(st, func(str *build.StringExpr, kind strKind) {
		if concat[str] || kind == anyString {
			if rest, ok := strings.CutPrefix(str.Value, m.rel+"/"); ok {
				str.Value = rest
				return
			}
		}
		if !concat[str] {
			str.Value, _ = m.rewriteString(str.Value, kind, m.src, m.dst)
		}
	})
	if _, ok := st.(*build.CommentBlock); !ok {
		st.Comment().After = nil
	}
}

func concatParts(e build.Expr) map[*build.StringExpr]bool {
	parts := map[*build.StringExpr]bool{}
	build.Walk(e, func(x build.Expr, _ []build.Expr) {
		if b, ok := x.(*build.BinaryExpr); ok && b.Op == "+" {
			for _, side := range []build.Expr{b.X, b.Y} {
				if s, ok := side.(*build.StringExpr); ok {
					parts[s] = true
				}
			}
		}
	})
	return parts
}
