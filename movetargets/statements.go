package movetargets

import "github.com/bazelbuild/buildtools/build"

func movedStatements(s *srcPackage, ranges []lineRange, rep *report) []build.Expr {
	return nil
}

func statementNames(stmts []build.Expr) map[string]bool {
	return map[string]bool{}
}

func moveStatement(st build.Expr, m *labelMap, a attrKinds) {
}
