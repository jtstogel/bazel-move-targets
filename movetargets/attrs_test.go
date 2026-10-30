package movetargets

import (
	"testing"

	"github.com/bazelbuild/buildtools/build"
	"github.com/google/go-cmp/cmp"
)

func parseStmt(t *testing.T, src string) build.Expr {
	t.Helper()
	f, err := build.ParseBuild("test", []byte(src))
	if err != nil {
		t.Fatalf("parsing %q: %v", src, err)
	}
	if len(f.Stmt) != 1 {
		t.Fatalf("parsing %q: got %d statements, want 1", src, len(f.Stmt))
	}
	return f.Stmt[0]
}

var kindNames = map[strKind]string{anyString: "any", labelString: "label", fileString: "file"}

func TestForEachString(t *testing.T) {
	tests := []struct {
		name, src string
		want      []string // "kind:value"
	}{
		{
			name: "attribute_kinds",
			src:  `r(name = "n", srcs = ["a.cpp"], deps = [":d"], copts = ["-O2"])`,
			want: []string{"file:a.cpp", "label::d", "any:-O2"},
		},
		{
			name: "select_keys_are_labels",
			src:  `r(name = "n", deps = [":a"] + select({":cond": ["b"], "//conditions:default": []}))`,
			want: []string{"label::a", "label::cond", "label:b", "label://conditions:default"},
		},
		{
			name: "selects.with_or",
			src:  `r(name = "n", hdrs = selects.with_or({("x", "y"): ["h.h"]}))`,
			want: []string{"label:x", "label:y", "file:h.h"},
		},
		{
			name: "plain_dict_keys",
			src:  `config_setting(name = "n", flag_values = {":mpfr": "disable"})`,
			want: []string{"any::mpfr", "any:disable"},
		},
		{
			name: "glob_skipped",
			src:  `r(name = "n", srcs = glob(["*.cpp"]) + ["x.cpp"])`,
			want: []string{"file:x.cpp"},
		},
		{
			name: "positional_arguments",
			src:  `exports_files(["a.h"], visibility = ["//visibility:public"])`,
			want: []string{"any:a.h", "any://visibility:public"},
		},
		{
			name: "other_calls",
			src:  `r(name = "n", deps = "//libc:x".replace("x", "y"))`,
			want: []string{"any://libc:x", "any:x", "any:y"},
		},
		{
			name: "comprehension",
			src:  `[r(name = n, deps = [":a"]) for n in ["p", "q"]]`,
			want: []string{"label::a", "any:p", "any:q"},
		},
		{
			name: "assignment",
			src:  `HDRS = ["a.h", ":b"]`,
			want: []string{"any:a.h", "any::b"},
		},
		{
			name: "load",
			src:  `load(":defs.bzl", "r")`,
		},
	}
	a := newAttrKinds(defaultLabelAttrs(), defaultFileAttrs(), nil, nil)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			a.forEachString(parseStmt(t, tc.src), func(s *build.StringExpr, kind strKind) {
				got = append(got, kindNames[kind]+":"+s.Value)
			})
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("forEachString(%s) mismatch (-want +got):\n%s", tc.src, diff)
			}
		})
	}
}

func TestOutputs(t *testing.T) {
	a := newAttrKinds(nil, nil, nil, defaultOutputAttrs())
	tests := []struct {
		src  string
		want []string
	}{
		{src: `copy_file(name = "c", src = "a.h", out = "staging/a.h")`, want: []string{"staging/a.h"}},
		{src: `genrule(name = "g", outs = ["x.h", "y.h"], cmd = "")`, want: []string{"x.h", "y.h"}},
		{src: `copy_file(name = "c", out = "staging/" + name)`},
		{src: `cc_library(name = "l", srcs = ["a.cpp"])`},
	}
	for _, tc := range tests {
		var got []string
		for _, s := range a.outputs(parseStmt(t, tc.src).(*build.CallExpr)) {
			got = append(got, s.Value)
		}
		if diff := cmp.Diff(tc.want, got); diff != "" {
			t.Errorf("outputs(%s) mismatch (-want +got):\n%s", tc.src, diff)
		}
	}
}
