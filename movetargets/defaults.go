package movetargets

import (
	"slices"
	"strings"
)

// The default attribute and rule-kind lists. The bazel* lists cover Bazel's
// native rules and common macros; the libc* ones are LLVM-libc's, which this
// tool was built for. Flags extend the combined lists.
var (
	bazelLabelAttrs = []string{
		"actual",
		"additional_deps",
		"data",
		"dep",
		"deps",
		"exports",
		"implementation_deps",
		"plugins",
		"runtime_deps",
		"target_compatible_with",
		"targets",
		"tests",
	}
	bazelFileAttrs = []string{
		"hdrs",
		"main",
		"srcs",
		"textual_hdrs",
	}
	bazelRefuseKinds = []string{
		"bool_flag",
		"bool_setting",
		"config_setting",
		"genrule",
		"int_flag",
		"string_flag",
		"string_list_flag",
		"string_setting",
	}
	bazelPackageRelativeAttrs = []string{
		"includes",
		"strip_include_prefix",
		"include_prefix",
		"imports",
	}
	bazelOutputAttrs = []string{
		"out",
		"outs",
	}

	libcLabelAttrs = []string{
		"libc_functions",
	}
	libcRefuseKinds = []string{
		"libc_generated_header",
	}
)

func defaultLabelAttrs() []string           { return slices.Concat(bazelLabelAttrs, libcLabelAttrs) }
func defaultFileAttrs() []string            { return slices.Clone(bazelFileAttrs) }
func defaultRefuseKinds() []string          { return slices.Concat(bazelRefuseKinds, libcRefuseKinds) }
func defaultPackageRelativeAttrs() []string { return slices.Clone(bazelPackageRelativeAttrs) }
func defaultOutputAttrs() []string          { return slices.Clone(bazelOutputAttrs) }

// autoPrefixDir returns the directory that --rename_strip_package_prefix=auto
// derives prefixes from: dst relative to src, without LLVM-libc's src/ layer,
// which its rule names omit (//libc:__support_cpp_x lives in src/__support/CPP).
func autoPrefixDir(src, dst string) string {
	rel, _ := relDir(src, dst)
	return strings.TrimPrefix(rel, "src/")
}
