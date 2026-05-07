package file

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// Resolve canonicalises input to a repo-relative, forward-slash path.
// Dot-relative inputs ("./foo", "../api") are resolved against anchor; bare inputs are workspace-relative.
// Absolute paths and paths escaping the workspace root are rejected.
func Resolve(input, anchor string) (string, error) {
	in := filepath.ToSlash(input)
	if in == "" {
		return "", fmt.Errorf("magus: empty project path")
	}
	if path.IsAbs(in) || hasDriveLetter(in) {
		return "", fmt.Errorf("magus: project path %q must be repo-relative, not absolute", input)
	}
	// Dot-relative inputs resolve against the anchor; bare inputs are
	// workspace-relative. The escape check applies to both: a bare input like
	// "foo/../../bar" cleans to "../bar" and must be rejected too.
	cleaned := path.Clean(in)
	if isRelativeMarker(in) {
		cleaned = path.Clean(path.Join(anchor, in))
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("magus: project path %q escapes workspace root from %q", input, anchor)
	}
	return cleaned, nil
}

func isRelativeMarker(p string) bool {
	return p == "." || p == ".." ||
		strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../")
}

func hasDriveLetter(p string) bool {
	if len(p) < 2 || p[1] != ':' {
		return false
	}
	c := p[0]
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}
