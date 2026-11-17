package review

import (
	"os"
	"path"
	"path/filepath"
	"slices"
)

// OpensDir reports whether some added file sits in a directory holding no file the change did
// not add. Subdirectories are left to the graph, which knows which of their files are new.
func OpensDir(root string, added map[string]bool) bool {
	for file := range added {
		dir := path.Dir(file)
		if dir == "." {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
		if err != nil {
			continue
		}
		if !slices.ContainsFunc(entries, func(e os.DirEntry) bool {
			return !e.IsDir() && !added[path.Join(dir, e.Name())]
		}) {
			return true
		}
	}
	return false
}
