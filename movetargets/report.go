package movetargets

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/bazelbuild/buildtools/build"
)

type severity int

const (
	sevError severity = iota
	sevWarning
	sevInfo
)

// position locates a diagnostic: "path:line", "path", or "" for none.
type position string

type item struct {
	sev      severity
	category string
	pos      position
	msg      string
}

type report struct {
	items    []item
	summary  []string
	rewrites map[string]int // path -> number of strings rewritten
}

func (r *report) add(sev severity, category string, pos position, format string, args ...any) {
	r.items = append(r.items, item{
		sev:      sev,
		category: category,
		pos:      pos,
		msg:      fmt.Sprintf(format, args...),
	})
}

func (r *report) errorf(category string, pos position, format string, args ...any) {
	r.add(sevError, category, pos, format, args...)
}

func (r *report) warnf(category string, pos position, format string, args ...any) {
	r.add(sevWarning, category, pos, format, args...)
}

func (r *report) infof(category string, pos position, format string, args ...any) {
	r.add(sevInfo, category, pos, format, args...)
}

func (r *report) hasErrors() bool {
	return slices.ContainsFunc(r.items, func(it item) bool { return it.sev == sevError })
}

func (r *report) countRewrites(filePath string, n int) {
	if n == 0 {
		return
	}
	if r.rewrites == nil {
		r.rewrites = map[string]int{}
	}
	r.rewrites[filePath] += n
}

// spanner is a syntax node or comment with a source span.
type spanner interface {
	Span() (start, end build.Position)
}

// posOf returns the position of a syntax node or comment in filePath.
func posOf(filePath string, e spanner) position {
	start, _ := e.Span()
	return position(fmt.Sprintf("%s:%d", filePath, start.Line))
}

func (r *report) print(w io.Writer, verbose bool) {
	for _, s := range r.summary {
		fmt.Fprintln(w, s)
	}
	if len(r.rewrites) > 0 {
		fmt.Fprintf(w, "Rewrote references in %d files:\n", len(r.rewrites))
		for _, p := range slices.Sorted(maps.Keys(r.rewrites)) {
			fmt.Fprintf(w, "  %s: %d\n", p, r.rewrites[p])
		}
	}
	r.printItems(w, sevError, "Errors")
	r.printItems(w, sevWarning, "Warnings")
	if verbose {
		r.printItems(w, sevInfo, "Info")
	}
}

func (r *report) printItems(w io.Writer, sev severity, title string) {
	var items []item
	for _, it := range r.items {
		if it.sev == sev {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		return
	}
	slices.SortStableFunc(items, func(a, b item) int { return strings.Compare(a.category, b.category) })
	fmt.Fprintf(w, "%s (%d):\n", title, len(items))
	for _, it := range items {
		pos := ""
		if it.pos != "" {
			pos = string(it.pos) + ": "
		}
		fmt.Fprintf(w, "  %s[%s] %s\n", pos, it.category, it.msg)
	}
}
