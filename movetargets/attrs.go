package movetargets

import (
	"github.com/bazelbuild/buildtools/build"
)

// strKind says how a bare string is interpreted.
type strKind int

const (
	anyString   strKind = iota // only prefixed strings ("//", "@", ":") are labels
	labelString                // bare strings are relative labels
	fileString                 // bare strings are files (or rules) in the package
)

// attrKinds classifies rule attributes.
type attrKinds struct {
	label, file     map[string]bool
	output          map[string]bool // attributes that name the files a rule generates
	packageRelative []string        // attributes whose meaning depends on the package directory
}

func newAttrKinds(labelAttrs, fileAttrs, packageRelativeAttrs, outputAttrs []string) attrKinds {
	return attrKinds{
		label:           toSet(labelAttrs),
		file:            toSet(fileAttrs),
		output:          toSet(outputAttrs),
		packageRelative: packageRelativeAttrs,
	}
}

// outputs returns the files a rule declares it generates: the literal strings
// of its output attributes, whole or as list elements.
func (a attrKinds) outputs(c *build.CallExpr) []*build.StringExpr {
	var outs []*build.StringExpr
	for _, arg := range c.List {
		kw, ok := arg.(*build.AssignExpr)
		if !ok || !a.output[identName(kw.LHS)] {
			continue
		}
		if s, ok := kw.RHS.(*build.StringExpr); ok {
			outs = append(outs, s)
		}
		outs = append(outs, listStrings(kw.RHS)...)
	}
	return outs
}

func (a attrKinds) kindOf(attr string) strKind {
	switch {
	case a.file[attr]:
		return fileString
	case a.label[attr]:
		return labelString
	}
	return anyString
}

type stringFunc func(s *build.StringExpr, kind strKind)

// forEachString visits the strings of a top-level statement that may hold labels.
// Rule names, load statements and glob patterns are skipped.
func (a attrKinds) forEachString(stmt build.Expr, fn stringFunc) {
	switch st := stmt.(type) {
	case *build.CallExpr:
		for _, arg := range st.List {
			kw, ok := arg.(*build.AssignExpr)
			if !ok {
				walkValue(arg, anyString, fn)
				continue
			}
			name := identName(kw.LHS)
			if name != "name" {
				walkValue(kw.RHS, a.kindOf(name), fn)
			}
		}
	case *build.Comprehension:
		if body, ok := st.Body.(*build.CallExpr); ok {
			a.forEachString(body, fn)
		} else {
			walkValue(st.Body, anyString, fn)
		}
		for _, c := range st.Clauses {
			walkValue(c, anyString, fn)
		}
	case *build.AssignExpr:
		walkValue(st.RHS, anyString, fn)
	}
}

// walkValue visits the strings in an attribute value without evaluating it.
func walkValue(e build.Expr, kind strKind, fn stringFunc) {
	switch e := e.(type) {
	case *build.StringExpr:
		fn(e, kind)
	case *build.ListExpr:
		for _, x := range e.List {
			walkValue(x, kind, fn)
		}
	case *build.TupleExpr:
		for _, x := range e.List {
			walkValue(x, kind, fn)
		}
	case *build.BinaryExpr:
		walkValue(e.X, kind, fn)
		walkValue(e.Y, kind, fn)
	case *build.ParenExpr:
		walkValue(e.X, kind, fn)
	case *build.DictExpr:
		walkDict(e, anyString, kind, fn)
	case *build.CallExpr:
		switch callName(e) {
		case "glob": // Patterns are paths, not labels; ruleFiles reads them.
		case "select", "selects.with_or":
			walkSelect(e, kind, fn)
		default:
			walkAny(e, fn)
		}
	default:
		walkAny(e, fn)
	}
}

// walkSelect visits a select() call: dict keys are labels, values have the
// attribute's kind.
func walkSelect(c *build.CallExpr, kind strKind, fn stringFunc) {
	for _, arg := range c.List {
		if d, ok := arg.(*build.DictExpr); ok {
			walkDict(d, labelString, kind, fn)
		} else {
			walkValue(arg, anyString, fn)
		}
	}
}

func walkDict(d *build.DictExpr, keyKind, valueKind strKind, fn stringFunc) {
	for _, kv := range d.List {
		walkValue(kv.Key, keyKind, fn)
		walkValue(kv.Value, valueKind, fn)
	}
}

func walkAny(e build.Expr, fn stringFunc) {
	build.Walk(e, func(x build.Expr, _ []build.Expr) {
		if s, ok := x.(*build.StringExpr); ok {
			fn(s, anyString)
		}
	})
}

func callName(c *build.CallExpr) string {
	return build.NewRule(c).Kind()
}

func identName(e build.Expr) string {
	if id, ok := e.(*build.Ident); ok {
		return id.Name
	}
	return ""
}

func ruleName(c *build.CallExpr) string {
	return build.NewRule(c).AttrString("name")
}

func toSet(xs []string) map[string]bool {
	s := make(map[string]bool, len(xs))
	for _, x := range xs {
		s[x] = true
	}
	return s
}

// listStrings returns the string literals that are direct elements of a list.
func listStrings(e build.Expr) []*build.StringExpr {
	var strs []*build.StringExpr
	if l, ok := e.(*build.ListExpr); ok {
		for _, x := range l.List {
			if s, ok := x.(*build.StringExpr); ok {
				strs = append(strs, s)
			}
		}
	}
	return strs
}
