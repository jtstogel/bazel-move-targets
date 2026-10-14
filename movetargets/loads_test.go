package movetargets

import (
	"testing"

	"github.com/bazelbuild/buildtools/build"
	"github.com/google/go-cmp/cmp"
)

func parseFile(t *testing.T, src string) *build.File {
	t.Helper()
	f, err := build.ParseBuild("BUILD", []byte(src))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	return f
}

func TestLoadBindings(t *testing.T) {
	f := parseFile(t, `
load(":defs.bzl", "a", b = "real_b")
load("//other:x.bzl", "c")
load("@repo//:y.bzl", "d")
`)
	want := map[string]binding{
		"a": {module: "//libc:defs.bzl", name: "a"},
		"b": {module: "//libc:defs.bzl", name: "real_b"},
		"c": {module: "//other:x.bzl", name: "c"},
		"d": {module: "@repo//:y.bzl", name: "d"},
	}
	if diff := cmp.Diff(want, loadBindings(f, "libc"), cmp.AllowUnexported(binding{})); diff != "" {
		t.Errorf("loadBindings(f, %q) mismatch (-want +got):\n%s", "libc", diff)
	}
}

func TestIdentsUsed(t *testing.T) {
	f := parseFile(t, `
load(":defs.bzl", "unused_load")
X = 1
r(name = "n", srcs = HDRS + select({"a": Y}), copts = f())
`)
	want := map[string]bool{"r": true, "HDRS": true, "select": true, "Y": true, "f": true}
	if diff := cmp.Diff(want, identsUsed(f.Stmt)); diff != "" {
		t.Errorf("identsUsed(f.Stmt) mismatch (-want +got):\n%s", diff)
	}
}

func TestNewLoads(t *testing.T) {
	bindings := map[string]binding{
		"lib":  {module: "//libc:defs.bzl", name: "lib"},
		"b":    {module: "//libc:defs.bzl", name: "real_b"},
		"sel":  {module: "@skylib//lib:selects.bzl", name: "selects"},
		"skip": {module: "//libc:defs.bzl", name: "skip"},
	}
	f := &build.File{Type: build.TypeBuild}
	for _, l := range newLoads(map[string]bool{"lib": true, "b": true, "sel": true}, bindings) {
		f.Stmt = append(f.Stmt, l)
	}
	want := `load("@skylib//lib:selects.bzl", sel = "selects")
load("//libc:defs.bzl", "lib", b = "real_b")
`
	if diff := cmp.Diff(want, string(build.Format(f))); diff != "" {
		t.Errorf("newLoads() mismatch (-want +got):\n%s", diff)
	}
}

func TestMergeLoads(t *testing.T) {
	f := parseFile(t, `load("//libc:defs.bzl", "a")

package(default_visibility = ["//visibility:public"])
`)
	loads := newLoads(map[string]bool{"a": true, "b": true, "c": true}, map[string]binding{
		"a": {module: "//libc:defs.bzl", name: "a"},
		"b": {module: "//libc:defs.bzl", name: "b"},
		"c": {module: "//other:o.bzl", name: "c"},
	})
	var rep report
	mergeLoads(f, "libc/src/stdlib", loads, "BUILD", &rep)
	want := `load("//libc:defs.bzl", "a", "b")
load("//other:o.bzl", "c")

package(default_visibility = ["//visibility:public"])
`
	if diff := cmp.Diff(want, string(build.Format(f))); diff != "" {
		t.Errorf("mergeLoads() mismatch (-want +got):\n%s", diff)
	}
	if rep.hasErrors() {
		t.Errorf("mergeLoads() reported errors: %+v", rep.items)
	}
}

func TestMergeLoadsConflict(t *testing.T) {
	f := parseFile(t, `load("//other:defs.bzl", "a")
`)
	loads := newLoads(map[string]bool{"a": true}, map[string]binding{
		"a": {module: "//libc:defs.bzl", name: "a"},
	})
	var rep report
	mergeLoads(f, "libc/src/stdlib", loads, "BUILD", &rep)
	if !rep.hasErrors() {
		t.Errorf("mergeLoads() with a conflicting load reported no error; file:\n%s", build.Format(f))
	}
}

func TestPruneLoads(t *testing.T) {
	f := parseFile(t, `load(":defs.bzl", "gone", "kept", "unrelated")
load(":other.bzl", "gone_too")

kept(name = "x")
`)
	pruneLoads(f, map[string]bool{"gone": true, "kept": true, "gone_too": true})
	want := `load(":defs.bzl", "kept", "unrelated")

kept(name = "x")
`
	if diff := cmp.Diff(want, string(build.Format(f))); diff != "" {
		t.Errorf("pruneLoads() mismatch (-want +got):\n%s", diff)
	}
}
