package file

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// NormalizeWorkspacePath canonicalises a path-SHAPED string to the workspace-relative,
// forward-slash form magus names files by, so the spellings a human actually produces -
// shell tab-completion's "./a/b", an editor's "Copy Path" absolute, a Windows-side
// agent's "a\b" - all name what a bare "a/b" names.
//
// ok is false when input is not path-shaped, or when it is a path that cannot be placed
// inside root; the returned string is then input, unchanged. Leaving it alone is the
// point: a path from a different checkout that was rewritten into a plausible relative
// path would name a DIFFERENT file, which is worse than finding nothing.
//
// Path-shaped means a separator or a drive letter. A bare term ("guard_shell.go",
// "hint") is never touched, and neither is anything holding "://" - path.Clean collapses
// a URL's double slash.
//
// A leading "/" that is not a real path under root is read as anchored AT the workspace
// root, but only when that reading names something that exists; likewise the backslash
// translation. root may be empty, which limits this to the shapes needing no workspace
// on disk (dot-relative and interior "..").
func NormalizeWorkspacePath(input, root string) (string, bool) {
	if input == "" || strings.Contains(input, "://") {
		return input, false
	}
	if !strings.ContainsAny(input, `/\`) && !hasDriveLetter(input) {
		return input, false
	}
	// Backslash only when there is no forward slash to contradict it. A backslash is a
	// legal character in a POSIX filename and an escape in a regex, and no Windows path
	// mixes the two separators, so a term holding both is read as-is. Even then the
	// rewrite has to land on something real, which is what keeps a Buzz namespace
	// (magus\project) and a regex fragment out of the path grammar.
	if strings.ContainsRune(input, '\\') && !strings.ContainsRune(input, '/') {
		rel, ok := normalizeSlashed(strings.ReplaceAll(input, `\`, "/"), root)
		if !ok || !existsUnder(root, rel) {
			return input, false
		}
		return rel, true
	}
	return normalizeSlashed(input, root)
}

// normalizeSlashed canonicalises a forward-slash path, splitting on whether it is rooted.
func normalizeSlashed(in, root string) (string, bool) {
	if path.IsAbs(in) || hasDriveLetter(in) {
		return normalizeRooted(in, root)
	}
	// resolveAmbiguous with a "." anchor is exactly the bare/dot-relative reading wanted
	// here, escape rejection included; the absolute case it refuses is handled above.
	rel, err := resolveAmbiguous(in, ".")
	if err != nil {
		return in, false
	}
	return rel, true
}

// normalizeRooted places an absolute path inside root, preferring the literal reading.
func normalizeRooted(in, root string) (string, bool) {
	if root != "" {
		if rel, err := filepath.Rel(filepath.Clean(root), filepath.FromSlash(path.Clean(in))); err == nil {
			s := filepath.ToSlash(rel)
			if s != "." && s != ".." && !strings.HasPrefix(s, "../") {
				return s, true
			}
		}
	}
	anchored := strings.TrimPrefix(stripDriveLetter(path.Clean(in)), "/")
	if anchored == "" || anchored == "." || !existsUnder(root, anchored) {
		return in, false
	}
	return anchored, true
}

// existsUnder reports whether rel names an existing entry under root. Lstat, not Stat: a
// symlink is a node in the graph and a match for `magus where`, so it counts as real.
func existsUnder(root, rel string) bool {
	if root == "" {
		return false
	}
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

func stripDriveLetter(p string) string {
	if hasDriveLetter(p) {
		return p[2:]
	}
	return p
}
