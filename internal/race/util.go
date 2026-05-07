package race

import (
	"os"
	"strings"
)

// shouldSkipDir reports whether a directory name should be excluded from
// recursive watching. These are high-churn directories that produce noise
// without containing project source files.
func shouldSkipDir(name string) bool {
	switch name {
	case ".git", "node_modules", ".pnpm-store", ".turbo",
		"vendor", ".cache", "__pycache__", ".mypy_cache",
		".pytest_cache", "target", "dist", "build":
		return true
	}
	return strings.HasPrefix(name, ".")
}

func isDir(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.IsDir()
}

func readDirNames(dir string) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	return entries, nil
}
