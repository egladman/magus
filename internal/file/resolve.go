package file

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"strings"
)

// workspaceScheme is the RETIRED URI prefix a project reference may carry.
// A bare workspace-relative path is the only spelling magus teaches now;
// ResolveProject still consumes the scheme, with a warning, so no caller
// strips it by hand. types.WorkspaceRef is a separate const and still renders
// the scheme on the way OUT - that surface is not retired by this one.
const workspaceScheme = "workspace://"

// resolveAmbiguous canonicalises input to a repo-relative, forward-slash path,
// guessing which of two readings a bare path wanted.
// Dot-relative inputs ("./foo", "../api") are resolved against anchor; bare inputs are workspace-relative.
// Absolute paths and paths escaping the workspace root are rejected.
//
// Deliberately unexported. Which mode a bare path takes is the whole question,
// and a generically-named exported entry point is the one every caller reaches
// for by default: that is how an import path, which has no workspace-relative
// mode, silently mis-anchored and broke graph builds. Callers go through the
// entry point named for the surface their string came from - ResolveDependsOn,
// ResolveProject, or ResolveImport - so the mode is chosen by the name, once.
func resolveAmbiguous(input, anchor string) (string, error) {
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
	return resolveAmbiguous(input, anchor)
}

// Nit deliberately left: ResolveDependsOn is a one-line pass-through today. It stays
// a named entry point rather than collapsing into the shared body, because the point
// of this family is that a caller picks by SURFACE and never sees the ambiguous
// reading by default. Inlining it would put the only human-authored surface back on
// the generic name.

// ResolveProject canonicalises a project reference to a workspace-relative,
// forward-slash path.
//
// A plain "<path>" is the canonical spelling. The "" and "/" all-projects
// sentinels pass through untouched for the caller to fan out. Everything else
// takes the same ambiguous reading as [ResolveDependsOn]: dot-relative against
// anchor, bare paths workspace-relative, absolute or escaping paths rejected.
//
// The retired "workspace://<path>" form still parses, with a warning; a bare
// "workspace://" is still the root alias.
//
// This is the one place a CLI-supplied project reference is normalized; add new
// rules for that surface here rather than in callers. It is not the only entry
// point: an `import "project/<path>"` path anchors unconditionally and goes
// through [ResolveImport] instead.
func ResolveProject(ctx context.Context, input, anchor string) (string, error) {
	if input == "" || input == "/" {
		return input, nil
	}
	if rest, found := strings.CutPrefix(input, workspaceScheme); found {
		// compat(until: no shipped docs or skills teach workspace://): supports a ref
		// written against the scheme - an older script, a saved command, a doc page
		// read before it was retired - so it keeps resolving rather than failing as a
		// bare relative path with a stray "workspace:" segment.
		//
		// Observe dropping it is safe when the deprecation notes themselves have aged
		// out: `grep -rn "workspace://" docs/ CONTRIBUTING.md cmd/magus/skills/` finds
		// nothing. Today it finds exactly two, both saying the scheme is deprecated and
		// a bare path replaced it - that pair IS the window this branch covers. The
		// hits under types/ are a different thing: the OUTPUT rendering
		// (types.WorkspaceRef), which this branch does not serve.
		suggest := rest
		if suggest == "" {
			suggest = "."
		}
		slog.WarnContext(ctx, "magus: workspace:// is deprecated; a bare workspace-relative path means the same thing",
			"ref", input, "use", suggest)
		if rest == "" {
			return ".", nil
		}
		input = rest
	}
	return resolveAmbiguous(input, anchor)
}

// ResolveImport canonicalises a path relative to the importing magusfile's
// directory: the path written in an `import "project/<path>"`, or that path with
// a file suffix appended (what the `.file(rel)` member passes).
//
// It always anchors, which is what makes it its own entry point rather than a
// call to resolveAmbiguous. That two-mode reading takes a BARE input as
// workspace-relative and so silently mis-anchors the common descendant form; an
// import path has no such mode, because the module loader has always resolved it
// against the importing file's directory, so a bare "guides/agents" imported from
// docs/ means docs/guides/agents and never a top-level guides/agents.
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
	// Anchor first, then test: the input is measured from the importing magusfile's
	// directory, and what it must not escape is the WORKSPACE root, which is what a
	// remaining "../" after cleaning means.
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
