package movetargets

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestLoadWorkspace(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"libc/BUILD.bazel",
		"libc/src/stdlib/BUILD.bazel",
		"libc/rules.bzl",
		".git/BUILD.bazel",
		"bazel-out/pkg/BUILD.bazel",
	} {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte("# empty\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", full, err)
		}
	}

	ws, err := loadWorkspace(root)
	if err != nil {
		t.Fatalf("loadWorkspace(%q): %v", root, err)
	}
	if diff := cmp.Diff([]string{"libc", "libc/src/stdlib"}, ws.packages()); diff != "" {
		t.Errorf("ws.packages() diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"src/stdlib"}, ws.subpackagesBelow("libc")); diff != "" {
		t.Errorf("ws.subpackagesBelow(%q) diff (-want +got):\n%s", "libc", diff)
	}
	if diff := cmp.Diff([]string{"libc/rules.bzl"}, ws.bzlFiles); diff != "" {
		t.Errorf("ws.bzlFiles diff (-want +got):\n%s", diff)
	}
}

func TestReportPrint(t *testing.T) {
	var rep report
	rep.warnf("test-warn", "libc/BUILD.bazel:4", "warning message")
	rep.errorf("test-err", "libc/BUILD.bazel:2", "error message")
	rep.countRewrites("libc/BUILD.bazel", 3)
	if !rep.hasErrors() {
		t.Errorf("rep.hasErrors() = false, want true")
	}
	var buf bytes.Buffer
	rep.print(&buf, false)
	out := buf.String()
	for _, sub := range []string{
		"Rewrote references in 1 files:",
		"Warnings (1):",
		"  libc/BUILD.bazel:4: [test-warn] warning message",
		"Errors (1):",
		"  libc/BUILD.bazel:2: [test-err] error message",
	} {
		if !strings.Contains(out, sub) {
			t.Errorf("rep.print(false) output = %q, want substring %q", out, sub)
		}
	}
}
