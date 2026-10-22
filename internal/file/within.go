package file

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrOutsideRoot is Within's refusal of a path that resolves outside its root.
var ErrOutsideRoot = errors.New("resolves outside the root")

// Within resolves path through every symlink, a relative path against root, and returns
// it when it lies inside root, itself resolved. A path that does not exist is an error,
// and one outside root wraps ErrOutsideRoot.
func Within(root, path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve root %s: %w", root, err)
	}
	name := path
	if !filepath.IsAbs(name) {
		name = filepath.Join(resolved, name)
	}
	name, err = filepath.EvalSymlinks(name)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	rel, err := filepath.Rel(resolved, name)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s (%s) under %s: %w", path, name, resolved, ErrOutsideRoot)
	}
	return name, nil
}
