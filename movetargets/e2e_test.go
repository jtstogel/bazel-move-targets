package movetargets

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bazelbuild/buildtools/build"
)

// e2eTools are the external tools TestE2E uses when they are on PATH.
type e2eTools struct {
	bazel, buildifier string // paths, or "" when absent
}

// TestE2E runs single and sequential moves on a self-contained Bazel
// workspace and checks invariants, buildifier formatting, and bazel builds
// before and after each move.
func TestE2E(t *testing.T) {
	tools := e2eTools{bazel: lookPath("bazel"), buildifier: lookPath("buildifier")}
	if tools.bazel == "" {
		t.Log("bazel not on PATH: skipping the builds")
	}
	if tools.buildifier == "" {
		t.Log("buildifier not on PATH: skipping its format check")
	}

	stdlib := []string{
		"--src_package=//libc",
		"--dst_package=//libc/src/stdlib",
		"--rename_strip_package_prefix=stdlib_",
		"--alias_for_rule_kind=libc_function",
		"--package_comment=LLVM libc stdlib targets.",
	}
	cpp := []string{
		"--src_package=//libc",
		"--dst_package=//libc/src/__support/CPP",
		"--rename_strip_package_prefix=__support_cpp_",
	}
	compiler := []string{
		"--src_package=//libc",
		"--dst_package=//libc/src/compiler",
	}
	scenarios := []struct {
		name  string
		moves [][]string
	}{
		{name: "stdlib", moves: [][]string{stdlib}},
		{name: "cpp", moves: [][]string{cpp}},
		{name: "compiler", moves: [][]string{compiler}},
		{name: "cpp_then_stdlib", moves: [][]string{cpp, stdlib}},
	}
	root := t.TempDir()
	if tools.bazel != "" {
		t.Cleanup(func() {
			cmd := exec.Command(tools.bazel, "shutdown")
			cmd.Dir = root
			_ = cmd.Run()
		})
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			runScenario(t, root, sc.moves, tools)
		})
	}
}

// runScenario resets the workspace, applies the moves in order, and checks
// each one.
func runScenario(t *testing.T, root string, moves [][]string, tools e2eTools) {
	t.Helper()
	resetTestWorkspace(t, root)
	if tools.bazel != "" {
		runCmd(t, root, tools.bazel, "build", "--symlink_prefix=/", "//...")
	}
	for _, sel := range moves {
		mv := runMove(t, root, sel)
		if tools.buildifier != "" {
			runCmd(t, root, tools.buildifier, append([]string{"--mode=check"}, mv.changedBuildFiles()...)...)
		}
		checks := []struct {
			name     string
			offenses []string
		}{
			{name: "changed files are formatted", offenses: unformattedFiles(mv)},
			{name: "no stale references", offenses: staleReferences(mv)},
			{name: "moved rules arrived", offenses: missingRules(t, mv)},
			{name: "rule count", offenses: ruleCountMismatch(t, mv)},
			{name: "nothing left to select", offenses: leftToSelect(t, root, sel)},
		}
		for _, c := range checks {
			if len(c.offenses) > 0 {
				slices.Sort(c.offenses)
				t.Errorf("after move %v, %s: %d offenses, first ones:\n  %s", sel, c.name, len(c.offenses), strings.Join(c.offenses[:min(5, len(c.offenses))], "\n  "))
			}
		}
	}
	if tools.bazel != "" {
		runCmd(t, root, tools.bazel, "build", "--symlink_prefix=/", "//...")
	}
}

// moveResult is the workspace before and after one plan and apply.
type moveResult struct {
	cfg           *config
	before, after map[string]string
}

func (mv moveResult) srcFile() string { return mv.cfg.SrcPackage + "/BUILD.bazel" }
func (mv moveResult) dstFile() string { return mv.cfg.DstPackage + "/BUILD.bazel" }

func (mv moveResult) changedBuildFiles() []string {
	var files []string
	for p, data := range mv.after {
		if mv.before[p] != data && isBuildFile(p) {
			files = append(files, p)
		}
	}
	slices.Sort(files)
	return files
}

func runMove(t *testing.T, root string, sel []string) moveResult {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	before := readTree(t, root)
	runMain(t, append([]string{"plan", "--workspace_root=" + root, "--out=" + cfgPath}, sel...))
	runMain(t, []string{"apply", "--workspace_root=" + root, "--config=" + cfgPath})
	cfg, err := readConfig(cfgPath)
	if err != nil {
		t.Fatalf("readConfig(%q): %v", cfgPath, err)
	}
	if len(cfg.Targets) == 0 {
		t.Fatalf("Main(plan %v) selected 0 targets, want > 0", sel)
	}
	return moveResult{cfg: cfg, before: before, after: readTree(t, root)}
}

// unformattedFiles returns the changed BUILD files that buildtools would format differently.
func unformattedFiles(mv moveResult) []string {
	var unformatted []string
	for _, p := range mv.changedBuildFiles() {
		data := []byte(mv.after[p])
		if f, err := build.ParseBuild(p, data); err != nil || !bytes.Equal(build.Format(f), data) {
			unformatted = append(unformatted, p)
		}
	}
	return unformatted
}

// staleReferences returns the lines that still spell a moved rule's old label.
func staleReferences(mv moveResult) []string {
	var stale []string
	for _, tg := range mv.cfg.Targets {
		absolute := "//" + mv.cfg.SrcPackage + ":" + tg.Name + `"`
		for p, data := range mv.after {
			if isBuildFile(p) {
				stale = append(stale, linesWhere(p, data, func(l string) bool { return strings.Contains(l, absolute) })...)
			}
		}
		relative, bare, aliasName := `":`+tg.Name+`"`, `"`+tg.Name+`"`, `name = "`+tg.Name+`"`
		stale = append(stale, linesWhere(mv.srcFile(), mv.after[mv.srcFile()], func(l string) bool {
			return strings.Contains(l, relative) || (strings.Contains(l, bare) && !strings.Contains(l, aliasName))
		})...)
	}
	return stale
}

// missingRules returns the moved rules absent from the destination, and the
// aliases absent from the source.
func missingRules(t *testing.T, mv moveResult) []string {
	t.Helper()
	dstRules := topLevelRules(t, mv.dstFile(), mv.after[mv.dstFile()])
	srcRules := topLevelRules(t, mv.srcFile(), mv.after[mv.srcFile()])
	var missing []string
	for _, tg := range mv.cfg.Targets {
		if dstRules[tg.NewName] == nil {
			missing = append(missing, fmt.Sprintf("%s: no rule %q", mv.dstFile(), tg.NewName))
		}
		if !tg.Alias {
			continue
		}
		want := "//" + mv.cfg.DstPackage + ":" + tg.NewName
		if c := srcRules[tg.Name]; c == nil || build.NewRule(c).AttrString("actual") != want {
			missing = append(missing, fmt.Sprintf("%s: no alias %q with actual %q", mv.srcFile(), tg.Name, want))
		}
	}
	return missing
}

// ruleCountMismatch reports, as a single offense, when the source file lost
// more or fewer rules than the moved ones, counting the aliases left behind.
func ruleCountMismatch(t *testing.T, mv moveResult) []string {
	t.Helper()
	aliases := 0
	for _, tg := range mv.cfg.Targets {
		if tg.Alias {
			aliases++
		}
	}
	got := len(topLevelRules(t, mv.srcFile(), mv.after[mv.srcFile()])) - aliases + len(mv.cfg.Targets)
	want := len(topLevelRules(t, mv.srcFile(), mv.before[mv.srcFile()]))
	if got != want {
		return []string{fmt.Sprintf("rule count in //%s (+moved -aliases) = %d, want %d", mv.cfg.SrcPackage, got, want)}
	}
	return nil
}

// leftToSelect returns the rules that planning the same move again would select.
func leftToSelect(t *testing.T, root string, sel []string) []string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	runMain(t, append([]string{"plan", "--workspace_root=" + root, "--out=" + cfgPath}, sel...))
	again, err := readConfig(cfgPath)
	if err != nil {
		t.Fatalf("readConfig(%q): %v", cfgPath, err)
	}
	var left []string
	for _, tg := range again.Targets {
		left = append(left, tg.Name)
	}
	return left
}

// resetTestWorkspace populates root with a clean self-contained Bazel workspace.
func resetTestWorkspace(t *testing.T, root string) {
	t.Helper()
	for p := range readTree(t, root) {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			t.Fatalf("Remove(%q): %v", p, err)
		}
	}
	files := map[string]string{
		"MODULE.bazel": "",
		"libc/libc_build_rules.bzl": `"""Rule macros for testing."""

def libc_support_library(name, srcs = [], hdrs = [], deps = [], visibility = None):
    native.filegroup(
        name = name,
        srcs = srcs + hdrs + deps,
        visibility = visibility,
    )

def libc_function(name, srcs = [], hdrs = [], deps = [], visibility = None):
    native.filegroup(
        name = name,
        srcs = srcs + hdrs + deps,
        visibility = visibility,
    )
`,
		"libc/BUILD.bazel": `# License header.

load(":libc_build_rules.bzl", "libc_function", "libc_support_library")

package(default_visibility = ["//visibility:public"])

exports_files([
    "other.txt",
    "src/stdlib/exported.txt",
])

# ===----------------------------------------------------------------------===#
# __support/CPP
# ===----------------------------------------------------------------------===#

libc_support_library(
    name = "__support_cpp_type_traits",
    hdrs = ["src/__support/CPP/type_traits.h"],
)

libc_support_library(
    name = "__support_cpp_limits",
    hdrs = ["src/__support/CPP/limits.h"],
    deps = [":__support_cpp_type_traits"],
)

# ===----------------------------------------------------------------------===#
# compiler
# ===----------------------------------------------------------------------===#

libc_function(
    name = "__stack_chk_fail",
    srcs = ["src/compiler/__stack_chk_fail.cpp"],
    hdrs = ["src/compiler/__stack_chk_fail.h"],
)

# ===----------------------------------------------------------------------===#
# string
# ===----------------------------------------------------------------------===#

libc_support_library(
    name = "string_utils",
    srcs = ["src/string/utils.cpp"],
    hdrs = ["src/string/utils.h"],
    deps = [":__support_cpp_type_traits"],
)

# ===----------------------------------------------------------------------===#
# stdlib
# ===----------------------------------------------------------------------===#

libc_support_library(
    name = "stdlib_utils",
    srcs = ["src/stdlib/utils.cpp"],
    hdrs = ["src/stdlib/utils.h"],
    deps = [
        ":__support_cpp_limits",
        ":string_utils",
    ],
)

libc_function(
    name = "atoi",
    srcs = ["src/stdlib/atoi.cpp"],
    hdrs = ["src/stdlib/atoi.h"],
    deps = [":stdlib_utils"],
)

libc_function(
    name = "strtol",
    srcs = ["src/stdlib/strtol.cpp"],
    hdrs = ["src/stdlib/strtol.h"],
    deps = [":stdlib_utils"],
)
`,
		"libc/test/BUILD.bazel": `load("//libc:libc_build_rules.bzl", "libc_support_library")

package(default_visibility = ["//visibility:public"])

libc_support_library(
    name = "stdlib_tests",
    srcs = [
        "stdlib_test.cpp",
        "//libc:src/stdlib/exported.txt",
    ],
    deps = [
        "//libc:__stack_chk_fail",
        "//libc:__support_cpp_limits",
        "//libc:atoi",
        "//libc:stdlib_utils",
        "//libc:strtol",
    ],
)
`,
		"libc/other.txt":                         "",
		"libc/src/stdlib/strtol.h":               "",
		"libc/src/__support/CPP/type_traits.h":   "",
		"libc/src/__support/CPP/limits.h":        "",
		"libc/src/compiler/__stack_chk_fail.cpp": "",
		"libc/src/compiler/__stack_chk_fail.h":   "",
		"libc/src/string/utils.cpp":              "",
		"libc/src/string/utils.h":                "",
		"libc/src/stdlib/exported.txt":           "",
		"libc/src/stdlib/utils.cpp":              "",
		"libc/src/stdlib/utils.h":                "",
		"libc/src/stdlib/atoi.cpp":               "",
		"libc/src/stdlib/atoi.h":                 "",
		"libc/src/stdlib/strtol.cpp":             "",
		"libc/test/stdlib_test.cpp":              "",
	}
	writeTree(t, root, files)
}

// lookPath returns the path of a program on PATH, or "".
func lookPath(name string) string {
	p, _ := exec.LookPath(name)
	return p
}

func runCmd(t *testing.T, dir, bin string, args ...string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s failed: %v\n%s", filepath.Base(bin), strings.Join(args, " "), err, out)
	}
}

func runMain(t *testing.T, args []string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := Main(args, &stdout, &stderr); code != 0 {
		t.Fatalf("bazel-move-targets %s: exit %d\n%s", args[0], code, stderr.String())
	}
	t.Logf("bazel-move-targets %s:\n%s", args[0], stderr.String())
}

func isBuildFile(p string) bool {
	base := path.Base(filepath.ToSlash(p))
	return base == "BUILD" || base == "BUILD.bazel"
}

func topLevelRules(t *testing.T, p, data string) map[string]*build.CallExpr {
	t.Helper()
	f, err := build.ParseBuild(p, []byte(data))
	if err != nil {
		t.Fatalf("ParseBuild(%q): %v", p, err)
	}
	rules, _ := namedRules(f)
	return rules
}

// linesWhere returns "path:line" for each non-comment line of data that satisfies bad.
func linesWhere(p, data string, bad func(line string) bool) []string {
	var pos []string
	for i, line := range strings.Split(data, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") && bad(line) {
			pos = append(pos, fmt.Sprintf("%s:%d", p, i+1))
		}
	}
	return pos
}
