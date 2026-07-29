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

// resolveTwoMode canonicalises input to a repo-relative, forward-slash path.
// Dot-relative inputs ("./foo", "../api") are resolved against anchor; bare inputs are workspace-relative.
// Absolute paths and paths escaping the workspace root are rejected.
//
// Deliberately unexported. Which mode a bare path takes is the whole question,
// and a generically-named exported entry point is the one every caller reaches
// for by default: that is how an import path, which has no workspace-relative
// mode, silently mis-anchored and broke graph builds. Callers go through the
// entry point named for the surface their string came from - ResolveDependsOn,
// ResolveProject, or ResolveImport - so the mode is chosen by the name, once.
func resolveTwoMode(input, anchor string) (string, error) {
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

// ResolveDependsOn canonicalises a project path hand-written in a magusfile's
// depends_on list. Both spellings are a deliberate affordance for a human author:
// repo-relative ("libs/foo") and dot-relative ("../foo") both work.
//
// This is the one surface that wants that ambiguity. A path produced by magus
// itself - a project import, a CLI reference - has exactly one correct reading
// and uses the entry point named for it.
func ResolveDependsOn(input, anchor string) (string, error) {
	return resolveTwoMode(input, anchor)
}

// ResolveProject canonicalises a project reference to a workspace-relative,
// forward-slash path.
//
// The explicit "workspace://<path>" form and the plain "<path>" form are
// accepted interchangeably, so a path copied out of machine-readable output
// can be pasted straight back into a command. A bare "workspace://" is the
// root alias. The "" and "/" all-projects sentinels pass through untouched
// for the caller to fan out. Everything else resolves through the same two-mode reading as [ResolveDependsOn]:
// dot-relative against anchor, bare paths workspace-relative, absolute or
// escaping paths rejected.
//
// This is the one place a CLI-supplied project reference is normalized; add new
// rules for that surface here rather than in callers. It is not the only entry
// point: an `import "project/<path>"` path anchors unconditionally and goes
// through [ResolveImport] instead.
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
	return resolveTwoMode(input, anchor)
}

// ResolveImport canonicalises a path relative to the importing magusfile's
// directory: the path written in an `import "project/<path>"`, or that path with
// a file suffix appended (what the `.file(rel)` member passes).
//
// It always anchors, which is what makes it its own entry point rather than a
// call to resolveTwoMode. That two-mode reading takes a BARE input as
// workspace-relative and so silently mis-anchors the common descendant form; an
// import path has no such mode, because the module loader has always resolved it
// against the importing file's directory. Regression cases: TestResolveImport.
func ResolveImport(input, anchor string) (string, error) {
	// An import path is written in Buzz source and is always forward-slash, so a
	// backslash is a mistake on every platform, not just a Windows separator.
	// Converting unconditionally (rather than via filepath.ToSlash, a no-op off
	// Windows) makes the absolute check below reject a rooted `\foo` everywhere,
	// so this function does not resolve differently per GOOS.
	in := strings.ReplaceAll(input, `\`, "/")
	if in == "" {
		return "", fmt.Errorf("magus: empty import path")
	}
	if path.IsAbs(in) || hasDriveLetter(in) {
		return "", fmt.Errorf("magus: import path %q must be relative to the importing magusfile, not absolute", input)
	}
	cleaned := path.Clean(path.Join(anchor, in))
	// An import's escape is measured from the importing project's directory, not
	// from the workspace root, so this check anchors first and tests after.
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("magus: import path %q escapes workspace root from %q", input, anchor)
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
