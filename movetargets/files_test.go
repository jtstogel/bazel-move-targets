package movetargets

import (
	"testing"

	"github.com/bazelbuild/buildtools/build"
	"github.com/google/go-cmp/cmp"
)

func TestGlobCanReach(t *testing.T) {
	tests := []struct {
		pattern, dir string
		want         bool
	}{
		{pattern: "src/__support/math/**/*.h", dir: "src/__support/math/foo", want: true},
		{pattern: "src/__support/math/**/*.h", dir: "src/stdlib", want: false},
		{pattern: "src/*/x.h", dir: "src/stdlib", want: true},
		{pattern: "src/stdlib/*.h", dir: "src/stdlib", want: true},
		{pattern: "src/stdlib.h", dir: "src/stdlib", want: false},
		{pattern: "src/stdlib", dir: "src/stdlib", want: false},
		{pattern: "**/*.h", dir: "src/stdlib", want: true},
		{pattern: "*.h", dir: "src/stdlib", want: false},
		{pattern: "src/std*/a.h", dir: "src/stdlib", want: true},
		{pattern: "x/foo.h", dir: "x", want: true},
		{pattern: "y/foo.h", dir: "x", want: false},
	}
	for _, tc := range tests {
		if got := globCanReach(tc.pattern, tc.dir); got != tc.want {
			t.Errorf("globCanReach(%q, %q) = %v; want %v", tc.pattern, tc.dir, got, tc.want)
		}
	}
}

func TestPlace(t *testing.T) {
	tests := []struct {
		name   string
		paths  []string
		strict bool
		want   placement
	}{
		{name: "no_files", want: noFiles},
		{name: "all_inside", paths: []string{"src/stdlib/a.cpp", "src/stdlib/a.h"}, want: inside},
		{name: "subdirectory_relaxed", paths: []string{"src/stdlib/linux/abort.cpp"}, want: inside},
		{name: "subdirectory_strict", paths: []string{"src/stdlib/linux/abort.cpp"}, strict: true, want: nested},
		{name: "recursive_glob_strict", paths: []string{"src/stdlib/**"}, strict: true, want: nested},
		{name: "direct_strict", paths: []string{"src/stdlib/a.cpp"}, strict: true, want: inside},
		{name: "outside", paths: []string{"src/string/a.cpp"}, want: outside},
		{name: "prefix_is_not_a_directory", paths: []string{"src/stdlibx/a.cpp"}, want: outside},
		{name: "straddles", paths: []string{"src/stdlib/a.cpp", "src/string/a.h"}, want: straddles},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := place(tc.paths, "src/stdlib", tc.strict); got != tc.want {
				t.Errorf("place(%q, %q, strict=%v) = %v; want %v", tc.paths, "src/stdlib", tc.strict, got, tc.want)
			}
		})
	}
}

func TestRuleFiles(t *testing.T) {
	call := parseStmt(t, `libc_function(
    name = "f",
    srcs = ["src/stdlib/f.cpp", ":src/stdlib/g.cpp", "common", "some_macro_target"] + glob(["src/stdlib/x/*.h"], exclude = ["src/stdlib/x/y.h"]),
    hdrs = select({":cond": ["//libc:src/stdlib/f.h"], "//conditions:default": ["@other//libc:src/z.h"]}),
    deps = ["src/not_a_file.h"],
)`).(*build.CallExpr)
	m := testLabelMap()
	files, patterns := ruleFiles(call, newAttrKinds(defaultLabelAttrs(), defaultFileAttrs(), nil, nil), m)
	if diff := cmp.Diff([]string{"src/stdlib/f.cpp", "src/stdlib/g.cpp", "src/stdlib/f.h"}, files); diff != "" {
		t.Errorf("ruleFiles() files mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"src/stdlib/x/*.h"}, patterns); diff != "" {
		t.Errorf("ruleFiles() patterns mismatch (-want +got):\n%s", diff)
	}
}
