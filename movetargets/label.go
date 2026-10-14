package movetargets

import (
	"regexp"
	"strings"

	"github.com/bazelbuild/buildtools/labels"
)

func isAbsolute(s string) bool {
	return strings.HasPrefix(s, "//") || strings.HasPrefix(s, "@")
}

// hasLabelPrefix reports whether s is spelled like a label regardless of attribute.
func hasLabelPrefix(s string) bool {
	return isAbsolute(s) || strings.HasPrefix(s, ":")
}

// parseLabel parses "//p:n", "//p", "@r//p:n" and ":n" (relative to curPkg).
// Unlike labels.ParseRelative, it rejects strings that aren't labels.
func parseLabel(s, curPkg string) (labels.Label, bool) {
	if !hasLabelPrefix(s) || strings.ContainsAny(s, " \t\n\"'$") || strings.HasSuffix(s, ":") {
		return labels.Label{}, false
	}
	if strings.HasPrefix(s, "@") && !strings.Contains(s, "//") {
		return labels.Label{}, false
	}
	l := labels.ParseRelative(s, curPkg)
	if l.Target == "" || l.Target == "." {
		return labels.Label{}, false
	}
	return l, true
}

// resolve interprets a string of the given kind in package pkg as a label.
func resolve(s string, kind strKind, pkg string) (labels.Label, bool) {
	if hasLabelPrefix(s) {
		return parseLabel(s, pkg)
	}
	if kind == anyString || s == "" || strings.ContainsAny(s, " \t\n\"'$:") {
		return labels.Label{}, false
	}
	return labels.Label{Package: pkg, Target: s}, true
}

// fileIn spells l as a bare path when it is in pkg of the main repository.
func fileIn(l labels.Label, pkg string) string {
	if l.Repository == "" && l.Package == pkg {
		return l.Target
	}
	return l.Format()
}

// mapKind says what a label maps to, ordered by how much the label changes
// so that rewriteMakeVars can keep the strongest kind it sees.
type mapKind int

const (
	unmapped mapKind = iota
	mappedTarget
	mappedFile
)

// labelMap maps labels in the source package to their new place.
type labelMap struct {
	src, dst, rel string
	renamed       map[string]string // old rule name -> new rule name
	srcRules      map[string]bool   // literal rule names in the source package
	localRepos    map[string]bool   // repository names that mean this workspace
}

func (m *labelMap) isLocal(repo string) bool {
	return repo == "" || m.localRepos[repo]
}

func (m *labelMap) mapLabel(l labels.Label) (labels.Label, mapKind) {
	if l.Package != m.src || !m.isLocal(l.Repository) {
		return l, unmapped
	}
	if n, ok := m.renamed[l.Target]; ok {
		return labels.Label{Repository: l.Repository, Package: m.dst, Target: n}, mappedTarget
	}
	if rest, ok := strings.CutPrefix(l.Target, m.rel+"/"); ok && rest != "" && !m.srcRules[l.Target] {
		return labels.Label{Repository: l.Repository, Package: m.dst, Target: rest}, mappedFile
	}
	return l, unmapped
}

var makeVarRE = regexp.MustCompile(`\$\((location|locations|execpath|execpaths|rootpath|rootpaths|rlocationpath|rlocationpaths) ([^)]+)\)`)

// rewriteString maps a string of the given kind found in package pkg and
// spells the result as it should appear in package outPkg.
func (m *labelMap) rewriteString(s string, kind strKind, pkg, outPkg string) (string, mapKind) {
	if strings.Contains(s, "$(") {
		return m.rewriteMakeVars(s, pkg, outPkg)
	}
	l, ok := resolve(s, kind, pkg)
	if !ok {
		return s, unmapped
	}
	nl, mk := m.mapLabel(l)
	if mk == unmapped && !needsRespelling(s, l, pkg, outPkg) {
		return s, unmapped
	}
	if kind == fileString {
		return fileIn(nl, outPkg), mk
	}
	return nl.FormatRelative(outPkg), mk
}

// needsRespelling reports whether an unmapped label must be respelled because
// the statement holding it moves from pkg to outPkg: relative labels become
// absolute, and absolute labels into outPkg itself become relative.
func needsRespelling(s string, l labels.Label, pkg, outPkg string) bool {
	if pkg == outPkg {
		return false
	}
	return !isAbsolute(s) || (l.Repository == "" && l.Package == outPkg)
}

func (m *labelMap) rewriteMakeVars(s, pkg, outPkg string) (string, mapKind) {
	result := unmapped
	out := makeVarRE.ReplaceAllStringFunc(s, func(v string) string {
		sub := makeVarRE.FindStringSubmatch(v)
		arg, mk := m.rewriteString(sub[2], labelString, pkg, outPkg)
		result = max(result, mk)
		return "$(" + sub[1] + " " + arg + ")"
	})
	return out, result
}
