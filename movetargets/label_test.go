package movetargets

import (
	"testing"

	"github.com/bazelbuild/buildtools/labels"
)

func TestParseLabel(t *testing.T) {
	tests := []struct {
		in     string
		want   labels.Label
		wantOK bool
	}{
		{in: "//libc:foo", want: labels.Label{Package: "libc", Target: "foo"}, wantOK: true},
		{in: "//libc/src/math", want: labels.Label{Package: "libc/src/math", Target: "math"}, wantOK: true},
		{in: "//:foo", want: labels.Label{Target: "foo"}, wantOK: true},
		{in: "@llvm-project//libc:foo", want: labels.Label{Repository: "llvm-project", Package: "libc", Target: "foo"}, wantOK: true},
		{in: "@@llvm-project//libc", want: labels.Label{Repository: "llvm-project", Package: "libc", Target: "libc"}, wantOK: true},
		{in: "@//libc:foo", want: labels.Label{Package: "libc", Target: "foo"}, wantOK: true},
		{in: ":foo", want: labels.Label{Package: "cur", Target: "foo"}, wantOK: true},
		{in: "//libc:shared/rpc.h", want: labels.Label{Package: "libc", Target: "shared/rpc.h"}, wantOK: true},
		{in: "foo"},
		{in: "@platforms"},
		{in: "@repo:foo"},
		{in: "//libc:"},
		{in: ":"},
		{in: "//libc:a b"},
		{in: "$(location :x)"},
	}
	for _, tc := range tests {
		got, ok := parseLabel(tc.in, "cur")
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("parseLabel(%q, %q) = %v, %v; want %v, %v", tc.in, "cur", got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		in     string
		kind   strKind
		want   labels.Label
		wantOK bool
	}{
		{in: "foo", kind: labelString, want: labels.Label{Package: "cur", Target: "foo"}, wantOK: true},
		{in: "src/foo.h", kind: fileString, want: labels.Label{Package: "cur", Target: "src/foo.h"}, wantOK: true},
		{in: "foo", kind: anyString},
		{in: ":foo", kind: anyString, want: labels.Label{Package: "cur", Target: "foo"}, wantOK: true},
		{in: "-DFOO", kind: labelString, want: labels.Label{Package: "cur", Target: "-DFOO"}, wantOK: true},
		{in: "a b", kind: labelString},
		{in: "", kind: fileString},
	}
	for _, tc := range tests {
		got, ok := resolve(tc.in, tc.kind, "cur")
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("resolve(%q, %v, %q) = %v, %v; want %v, %v", tc.in, tc.kind, "cur", got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestFileIn(t *testing.T) {
	tests := []struct {
		l    labels.Label
		want string
	}{
		{l: labels.Label{Package: "libc", Target: "foo"}, want: "foo"},
		{l: labels.Label{Package: "libc", Target: "x/y.h"}, want: "x/y.h"},
		{l: labels.Label{Package: "libc/src/math", Target: "math"}, want: "//libc/src/math"},
		{l: labels.Label{Repository: "llvm-project", Package: "libc", Target: "foo"}, want: "@llvm-project//libc:foo"},
	}
	for _, tc := range tests {
		if got := fileIn(tc.l, "libc"); got != tc.want {
			t.Errorf("fileIn(%v, %q) = %q; want %q", tc.l, "libc", got, tc.want)
		}
	}
}

func testLabelMap() *labelMap {
	return &labelMap{
		src:        "libc",
		dst:        "libc/src/stdlib",
		rel:        "src/stdlib",
		renamed:    map[string]string{"atoi": "atoi", "__support_stdlib_util": "util"},
		srcRules:   map[string]bool{"atoi": true, "__support_stdlib_util": true, "common": true, "src/stdlib/odd_rule": true},
		localRepos: map[string]bool{"llvm-project": true},
	}
}

func TestMapLabel(t *testing.T) {
	m := testLabelMap()
	tests := []struct {
		in       labels.Label
		want     labels.Label
		wantKind mapKind
	}{
		{
			in:       labels.Label{Package: "libc", Target: "atoi"},
			want:     labels.Label{Package: "libc/src/stdlib", Target: "atoi"},
			wantKind: mappedTarget,
		},
		{
			in:       labels.Label{Package: "libc", Target: "__support_stdlib_util"},
			want:     labels.Label{Package: "libc/src/stdlib", Target: "util"},
			wantKind: mappedTarget,
		},
		{
			in:       labels.Label{Repository: "llvm-project", Package: "libc", Target: "atoi"},
			want:     labels.Label{Repository: "llvm-project", Package: "libc/src/stdlib", Target: "atoi"},
			wantKind: mappedTarget,
		},
		{
			in:       labels.Label{Package: "libc", Target: "src/stdlib/linux/abort.cpp"},
			want:     labels.Label{Package: "libc/src/stdlib", Target: "linux/abort.cpp"},
			wantKind: mappedFile,
		},
		{
			in:       labels.Label{Package: "libc", Target: "src/stdlib/odd_rule"},
			want:     labels.Label{Package: "libc", Target: "src/stdlib/odd_rule"},
			wantKind: unmapped,
		},
		{
			in:       labels.Label{Package: "libc", Target: "common"},
			want:     labels.Label{Package: "libc", Target: "common"},
			wantKind: unmapped,
		},
		{
			in:       labels.Label{Package: "libc", Target: "src/stdlibx/a.h"},
			want:     labels.Label{Package: "libc", Target: "src/stdlibx/a.h"},
			wantKind: unmapped,
		},
		{
			in:       labels.Label{Package: "libc", Target: "src/stdlib/"},
			want:     labels.Label{Package: "libc", Target: "src/stdlib/"},
			wantKind: unmapped,
		},
		{
			in:       labels.Label{Repository: "other", Package: "libc", Target: "atoi"},
			want:     labels.Label{Repository: "other", Package: "libc", Target: "atoi"},
			wantKind: unmapped,
		},
		{
			in:       labels.Label{Package: "libc/test", Target: "atoi"},
			want:     labels.Label{Package: "libc/test", Target: "atoi"},
			wantKind: unmapped,
		},
	}
	for _, tc := range tests {
		got, kind := m.mapLabel(tc.in)
		if got != tc.want || kind != tc.wantKind {
			t.Errorf("mapLabel(%v) = %v, %v; want %v, %v", tc.in, got, kind, tc.want, tc.wantKind)
		}
	}
}

func TestRewriteString(t *testing.T) {
	m := testLabelMap()
	tests := []struct {
		name, in    string
		kind        strKind
		pkg, outPkg string
		want        string
	}{
		{name: "relative_in_source", in: ":atoi", kind: labelString, pkg: "libc", outPkg: "libc", want: "//libc/src/stdlib:atoi"},
		{name: "bare_in_source", in: "atoi", kind: labelString, pkg: "libc", outPkg: "libc", want: "//libc/src/stdlib:atoi"},
		{name: "renamed_absolute", in: "//libc:__support_stdlib_util", kind: labelString, pkg: "libc/test", outPkg: "libc/test", want: "//libc/src/stdlib:util"},
		{name: "keeps_repo", in: "@llvm-project//libc:atoi", kind: labelString, pkg: "flang-rt", outPkg: "flang-rt", want: "@llvm-project//libc/src/stdlib:atoi"},
		{name: "unmapped_unchanged", in: "//libc:common", kind: labelString, pkg: "libc/test", outPkg: "libc/test", want: "//libc:common"},
		{name: "any_string_bare_ignored", in: "atoi", kind: anyString, pkg: "libc", outPkg: "libc", want: "atoi"},
		{name: "moved_rule_to_moved_rule", in: ":atoi", kind: labelString, pkg: "libc", outPkg: "libc/src/stdlib", want: ":atoi"},
		{name: "moved_rule_to_staying_rule", in: ":common", kind: labelString, pkg: "libc", outPkg: "libc/src/stdlib", want: "//libc:common"},
		{name: "moved_bare_to_staying_rule", in: "common", kind: labelString, pkg: "libc", outPkg: "libc/src/stdlib", want: "//libc:common"},
		{name: "moved_file", in: "src/stdlib/linux/abort.cpp", kind: fileString, pkg: "libc", outPkg: "libc/src/stdlib", want: "linux/abort.cpp"},
		{name: "moved_file_label", in: ":src/stdlib/abort.h", kind: fileString, pkg: "libc", outPkg: "libc/src/stdlib", want: "abort.h"},
		{name: "leftover_file", in: "src/stdlib/abort.h", kind: fileString, pkg: "libc", outPkg: "libc", want: "//libc/src/stdlib:abort.h"},
		{name: "absolute_stays_absolute", in: "//libc:common", kind: labelString, pkg: "libc", outPkg: "libc/src/stdlib", want: "//libc:common"},
		{name: "absolute_into_own_package", in: "//libc/src/stdlib:x", kind: labelString, pkg: "libc", outPkg: "libc/src/stdlib", want: ":x"},
		{name: "visibility_spec", in: "//visibility:public", kind: labelString, pkg: "libc", outPkg: "libc/src/stdlib", want: "//visibility:public"},
		{name: "make_variable", in: "$(location :atoi) --x=$(execpath common)", kind: anyString, pkg: "libc", outPkg: "libc", want: "$(location //libc/src/stdlib:atoi) --x=$(execpath common)"},
		{name: "other_make_variable", in: "$(SRCS)", kind: anyString, pkg: "libc", outPkg: "libc", want: "$(SRCS)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := m.rewriteString(tc.in, tc.kind, tc.pkg, tc.outPkg); got != tc.want {
				t.Errorf("rewriteString(%q, %v, %q, %q) = %q; want %q", tc.in, tc.kind, tc.pkg, tc.outPkg, got, tc.want)
			}
		})
	}
}
