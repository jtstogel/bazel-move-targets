package movetargets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestTrimPkg(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{in: "//libc/src/stdlib", want: "libc/src/stdlib"},
		{in: "//libc/", want: "libc"},
		{in: "libc", want: "libc"},
		{in: "//", want: ""},
	} {
		if got := trimPkg(tc.in); got != tc.want {
			t.Errorf("trimPkg(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRelDir(t *testing.T) {
	tests := []struct {
		src, dst, want string
		wantOK         bool
	}{
		{src: "libc", dst: "libc/src/stdlib", want: "src/stdlib", wantOK: true},
		{src: "", dst: "libc", want: "libc", wantOK: true},
		{src: "libc", dst: "libc"},
		{src: "libc", dst: "libcx/y"},
		{src: "libc/src", dst: "libc"},
	}
	for _, tc := range tests {
		got, ok := relDir(tc.src, tc.dst)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("relDir(%q, %q) = %q, %v; want %q, %v", tc.src, tc.dst, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestConfigRoundTrip(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg := &config{
		SrcPackage: "//libc/",
		DstPackage: "//libc/src/stdlib/",
		Targets: []target{
			{Name: "atoi", NewName: "atoi", Kind: "cc_library"},
		},
	}
	data, err := cfg.marshal()
	if err != nil {
		t.Fatalf("marshal(): %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", cfgPath, err)
	}
	got, err := readConfig(cfgPath)
	if err != nil {
		t.Fatalf("readConfig(%q): %v", cfgPath, err)
	}
	want := &config{
		SrcPackage: "libc",
		DstPackage: "libc/src/stdlib",
		Targets: []target{
			{Name: "atoi", NewName: "atoi", Kind: "cc_library"},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("readConfig(%q) diff (-want +got):\n%s", cfgPath, diff)
	}
}
