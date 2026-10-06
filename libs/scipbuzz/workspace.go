package scipbuzz

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// findWorkspaceRoot returns the nearest directory at or above dir holding
// magus.yaml, or dir itself when none does.
func findWorkspaceRoot(dir string) string {
	for d := dir; ; {
		if st, err := os.Stat(filepath.Join(d, "magus.yaml")); err == nil && st.Mode().IsRegular() {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}

// skippedDir reports whether discovery leaves a directory below the project root
// out: dot-directories, fixtures and dependency trees, and a nested project, which
// its own magusfile.buzz says is indexed on its own.
func skippedDir(path, name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "testdata", "node_modules", "vendor":
		return true
	}
	_, err := os.Stat(filepath.Join(path, "magusfile.buzz"))
	return err == nil
}

// discover returns the .buzz files under root as sorted slash paths relative to it.
func discover(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skippedDir(path, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || filepath.Ext(path) != ".buzz" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	slices.Sort(files)
	return files, err
}
