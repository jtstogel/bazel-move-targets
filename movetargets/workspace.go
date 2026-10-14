package movetargets

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bazelbuild/buildtools/build"
)

type workspace struct {
	root       string
	buildFiles map[string]string // package -> BUILD file path, relative to root
	bzlFiles   []string          // relative to root, sorted
}

func loadWorkspace(root string) (*workspace, error) {
	found := workspaceFiles{buildFiles: map[string]string{}}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && p != root && skipDir(d.Name()):
			return filepath.SkipDir
		case d.IsDir():
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		found.add(filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning workspace: %w", err)
	}
	slices.Sort(found.bzlFiles)
	return &workspace{root: root, buildFiles: found.buildFiles, bzlFiles: found.bzlFiles}, nil
}

// workspaceFiles collects the BUILD and .bzl files of a tree being walked.
type workspaceFiles struct {
	buildFiles map[string]string
	bzlFiles   []string
}

// add records rel if it is a BUILD or .bzl file. BUILD.bazel wins over BUILD.
func (wf *workspaceFiles) add(rel string) {
	switch name := path.Base(rel); {
	case name == "BUILD.bazel", name == "BUILD" && wf.buildFiles[pkgOf(rel)] == "":
		wf.buildFiles[pkgOf(rel)] = rel
	case strings.HasSuffix(name, ".bzl"):
		wf.bzlFiles = append(wf.bzlFiles, rel)
	}
}

// skipDir reports whether a directory can't hold workspace files: hidden
// directories and bazel-* output links.
func skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "bazel-")
}

// pkgOf returns the package directory of a root-relative file path.
func pkgOf(file string) string {
	if d := path.Dir(file); d != "." {
		return d
	}
	return ""
}

func (ws *workspace) packages() []string {
	return slices.Sorted(maps.Keys(ws.buildFiles))
}

func (ws *workspace) read(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(ws.root, filepath.FromSlash(rel)))
}

func (ws *workspace) parse(rel string) (*build.File, []byte, error) {
	data, err := ws.read(rel)
	if err != nil {
		return nil, nil, err
	}
	f, err := build.Parse(rel, data)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", rel, err)
	}
	return f, data, nil
}

// subpackagesBelow returns the packages under pkg, relative to pkg.
func (ws *workspace) subpackagesBelow(pkg string) []string {
	var subs []string
	for _, p := range ws.packages() {
		if rest, ok := strings.CutPrefix(p, pkg+"/"); ok {
			subs = append(subs, rest)
		}
	}
	return subs
}
