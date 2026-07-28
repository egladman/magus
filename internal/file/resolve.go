package file

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// workspaceScheme is the URI prefix a project reference may carry. Callers
// render it via types.WorkspaceRef on the way out; ResolveProject consumes
// it on the way in, so no caller strips it by hand.
const workspaceScheme = "workspace://"

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

// ResolveProject canonicalises a project reference to a workspace-relative,
// forward-slash path.
//
// The explicit "workspace://<path>" form and the plain "<path>" form are
// accepted interchangeably, so a path copied out of machine-readable output
// can be pasted straight back into a command. A bare "workspace://" is the
// root alias. The "" and "/" all-projects sentinels pass through untouched
// for the caller to fan out. Everything else resolves through [Resolve]:
// dot-relative against anchor, bare paths workspace-relative, absolute or
// escaping paths rejected.
//
// This is the one place a project reference is normalized; add new rules
// here rather than in callers.
func ResolveProject(input, anchor string) (string, error) {
	if input == "" || input == "/" {
		return input, nil
	}
	if rest, found := strings.CutPrefix(input, workspaceScheme); found {
		if rest == "" {
			return ".", nil
		}
		input = rest
	}
	return Resolve(input, anchor)
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
