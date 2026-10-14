package movetargets

import (
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// unifiedDiff returns a patch -p1 style diff; before is nil for a new file.
func unifiedDiff(filePath string, before, after []byte) (string, error) {
	from := "a/" + filePath
	if before == nil {
		from = "/dev/null"
	}
	return difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        splitLines(string(before)),
		B:        splitLines(string(after)),
		FromFile: from,
		ToFile:   "b/" + filePath,
		Context:  3,
	})
}

// splitLines splits s after each newline. Unlike difflib.SplitLines, it adds no extra line.
func splitLines(s string) []string {
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
