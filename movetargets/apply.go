package movetargets

// changes are the new contents of changed files: workspace files by path
// relative to the root, and extra files by the path they were given as.
type changes struct {
	workspace, extra map[string][]byte
}

func apply(ws *workspace, cfg *config, localRepos []string, extra extraFiles, rep *report) (changes, error) {
	return changes{}, nil
}
