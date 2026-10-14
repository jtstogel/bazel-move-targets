// The bazel-move-targets command moves Bazel rules from a package into a subpackage.
package main

import (
	"os"

	"github.com/jtstogel/bazel-move-targets/movetargets"
)

func main() {
	os.Exit(movetargets.Main(os.Args[1:], os.Stdout, os.Stderr))
}
