package movetargets

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestStripPrefix(t *testing.T) {
	prefixes := []string{"__support_", "__support_cpp_", "__support_CPP_"}
	tests := []struct {
		name, want string
	}{
		{name: "__support_cpp_string", want: "string"},
		{name: "__support_CPP_bit", want: "bit"},
		{name: "__support_common", want: "common"},
		{name: "atoi", want: "atoi"},
		{name: "__support_", want: ""},
	}
	for _, tc := range tests {
		if got := stripPrefix(tc.name, prefixes); got != tc.want {
			t.Errorf("stripPrefix(%q, %q) = %q; want %q", tc.name, prefixes, got, tc.want)
		}
	}
}

func TestExpandAutoPrefixes(t *testing.T) {
	tests := []struct {
		dst  string
		want []string
	}{
		{dst: "libc/src/__support/CPP", want: []string{"__support_CPP_", "__support_cpp_", "CPP_", "cpp_"}},
		{dst: "libc/src/sys/epoll", want: []string{"sys_epoll_", "sys_epoll_", "epoll_", "epoll_"}},
		{dst: "libc/include/llvm-libc-types", want: []string{"include_llvm_libc_types_", "include_llvm_libc_types_", "llvm_libc_types_", "llvm_libc_types_"}},
	}
	for _, tc := range tests {
		got := expandAutoPrefixes([]string{"x_", "auto"}, autoPrefixDir("libc", tc.dst))
		if diff := cmp.Diff(append([]string{"x_"}, tc.want...), got); diff != "" {
			t.Errorf("expandAutoPrefixes(%q) mismatch (-want +got):\n%s", tc.dst, diff)
		}
	}
}

func TestParseRanges(t *testing.T) {
	got, err := parseRanges([]string{"3-3", "10-20"})
	if err != nil {
		t.Fatalf("parseRanges({\"3-3\", \"10-20\"}): %v", err)
	}
	if diff := cmp.Diff([]lineRange{{First: 3, Last: 3}, {First: 10, Last: 20}}, got); diff != "" {
		t.Errorf("parseRanges({\"3-3\", \"10-20\"}) mismatch (-want +got):\n%s", diff)
	}
	for _, bad := range []string{"3", "0-2", "5-4", "a-b", "10-20abc", "x10-20", "10-"} {
		if _, err := parseRanges([]string{bad}); err == nil {
			t.Errorf("parseRanges(%q) succeeded; want an error", bad)
		}
	}
}

func TestStringListSet(t *testing.T) {
	var l stringList
	for _, v := range []string{"a,b", " c , ", "", "d"} {
		if err := l.Set(v); err != nil {
			t.Fatalf("Set(%q): %v", v, err)
		}
	}
	if diff := cmp.Diff([]string{"a", "b", "c", "d"}, []string(l)); diff != "" {
		t.Errorf("stringList after Set calls mismatch (-want +got):\n%s", diff)
	}
}
