package race

import (
	"bytes"
	"os/exec"
	"strings"
	"sync"
)

// gitFilter checks whether a path is tracked by git. Tracked files are
// the only ones eligible for race findings — tool-generated files,
// caches, and build artefacts are excluded by definition.
type gitFilter struct {
	root    string
	once    sync.Once
	tracked map[string]struct{}
}

func newGitFilter(root string) *gitFilter {
	return &gitFilter{root: root}
}

// Allow returns true if path is committed to git (appears in --cached output)
// and should be considered for race detection.
func (f *gitFilter) Allow(path string) bool {
	f.once.Do(f.build)
	_, ok := f.tracked[path]
	return ok
}

func (f *gitFilter) build() {
	f.tracked = make(map[string]struct{})
	out, err := exec.Command("git", "-C", f.root, "ls-files", "--cached", "-z").Output() //nolint:noctx // one-shot git probe at filter init; no ctx is plumbed here
	if err != nil {
		// Not a git repo or git unavailable; allow nothing (no race findings).
		return
	}
	for _, rel := range bytes.Split(out, []byte{0}) {
		if len(rel) == 0 {
			continue
		}
		abs := f.root + "/" + strings.TrimPrefix(string(rel), "./")
		f.tracked[abs] = struct{}{}
	}
}
