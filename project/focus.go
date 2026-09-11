package project

import (
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// SharedRoots are the paths in focus for every project, whatever a session was
// pointed at. Two rules, and an entry earns its place only by being UPSTREAM OF
// EVERY PROJECT with no depends_on edge to say so:
//
//   - every file directly at the workspace root, which Contains matches by DEPTH
//     rather than by name: the workspace declaration (magusfile.buzz), the resolved
//     config (magus.yaml), the instruction and routing surface (AGENTS.md,
//     MAGUS.md), and whatever tree-wide manifest the ecosystem keeps there (go.mod,
//     package.json). A file at depth zero belongs to no subtree; it describes the
//     workspace. Naming them one by one would go stale on the first workspace that
//     keeps a different one.
//   - .claude/skills, the directory `magus agent install` writes installed skills
//     to. The same instruction surface as AGENTS.md, one level down, and telling an
//     agent its own instructions are out of scope is the one advisory that would be
//     actively wrong. It is a PATH, the single host-specific step magus owns;
//     nothing here branches on a host name.
//
// A per-host instruction file is deliberately absent: a host loads its own before
// the first tool call, so it never reaches a read hook, and enumerating the ones
// that exist would put a host list in magus.
//
// NOT on the list: the root project's tree. In a workspace whose root project owns
// most of the files, treating everything under the root as shared would leave the
// focus set equal to the workspace and the whole rule inert.
var SharedRoots = []string{".claude/skills"}

// Focus is the read lane a session stands in: the project holding its working
// directory (or the projects a lease was leased), everything those projects
// depend on transitively, and SharedRoots.
//
// It is the mirror of the affected set, and the two run in opposite directions.
// Affected runs FORWARD from a change to everything it could break, which is the
// reverse closure. Focus runs BACKWARD from where a session stands to everything
// it legitimately needs to read. A sibling project is in neither: what a sibling
// does is not an input to this project's work, so reading it is how a session
// picks up practices and code quality this project never chose.
type Focus struct {
	// Seeds are the projects the focus was computed FROM: one for a working
	// directory, one or more for a lease's declared paths.
	Seeds []string
	// Projects is Seeds plus the upstream closure and each seed's nested
	// projects, sorted. Membership in this set is what Contains answers.
	Projects []string
	// owners is every project path in the workspace, longest first, so Owner
	// resolves by prefix walk. A map keyed on path could not express nesting,
	// and nesting is what decides which of two projects owns a file.
	owners []string
}

// FocusAt computes the focus of the project holding dir, which may be absolute or
// workspace-relative. It reports false when dir is outside the workspace or no
// project holds it, and a caller with no focus must stay silent rather than guess.
func FocusAt(w types.WorkspaceReader, dir string) (Focus, bool) {
	rel, ok := workspaceRel(w.Root(), dir)
	if !ok {
		return Focus{}, false
	}
	f := Focus{owners: ownerPaths(w)}
	seed := f.Owner(rel)
	if seed == "" {
		return Focus{}, false
	}
	f.Seeds = []string{seed}
	f.Projects = closure(w, f.owners, f.Seeds)
	return f, true
}

// FocusForPaths computes the focus of the projects that own paths, which is how a
// lease's declared boundary becomes a read lane. Entries may be globs: the literal
// prefix is what names a project, since a wildcard cannot.
func FocusForPaths(w types.WorkspaceReader, paths []string) (Focus, bool) {
	f := Focus{owners: ownerPaths(w)}
	for _, p := range paths {
		rel, ok := workspaceRel(w.Root(), types.LiteralPrefix(p))
		if !ok {
			continue
		}
		if owner := f.Owner(rel); owner != "" && !slices.Contains(f.Seeds, owner) {
			f.Seeds = append(f.Seeds, owner)
		}
	}
	if len(f.Seeds) == 0 {
		return Focus{}, false
	}
	slices.Sort(f.Seeds)
	f.Projects = closure(w, f.owners, f.Seeds)
	return f, true
}

// Contains reports whether a workspace-relative path is inside the focus.
//
// A path no project owns returns TRUE, which is not a gap: nothing declares it, so
// there is no project whose lane it could be outside of, and an advisory fired on a
// path magus cannot attribute is one fired on a guess. The root project counts as
// nobody here: it catches by containment whatever no subproject declares, so a
// root-owned path is the workspace's own, not a sibling's.
func (f Focus) Contains(rel string) bool {
	rel = cleanRel(rel)
	if rel == "" || rel == "." {
		return true
	}
	if !strings.Contains(rel, "/") {
		return true
	}
	for _, s := range SharedRoots {
		if rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	owner := f.Owner(rel)
	return owner == "" || owner == "." || slices.Contains(f.Projects, owner)
}

// Owner returns the project that holds a workspace-relative path: the longest
// project path that prefixes it, which is the same rule ClassifyFiles attributes
// by, so one file cannot be owned by one project here and another there.
func (f Focus) Owner(rel string) string {
	rel = cleanRel(rel)
	for _, p := range f.owners {
		if p == "." {
			return "."
		}
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return p
		}
	}
	return ""
}

// closure walks depends_on from each seed and collects what it reaches, plus the
// projects NESTED inside a seed.
//
// Nesting is included because a project inside the directory a session was pointed
// at is inside what it was pointed at; excluding it would advise against reading a
// subdirectory of your own tree. The root project is the exception, and it has to
// be: every project is nested in ".", so treating the root's descendants as focus
// would make a root-level session's focus the whole workspace.
func closure(w types.WorkspaceReader, owners, seeds []string) []string {
	seen := map[string]bool{}
	stack := slices.Clone(seeds)
	for _, seed := range seeds {
		if seed == "." {
			continue
		}
		for _, p := range owners {
			if strings.HasPrefix(p, seed+"/") {
				stack = append(stack, p)
			}
		}
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		if p := w.Get(cur); p != nil {
			stack = append(stack, p.DependsOn...)
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// ownerPaths lists every project path longest first, so a prefix walk finds the
// innermost owner. "." sorts last because it prefixes everything.
func ownerPaths(w types.WorkspaceReader) []string {
	all := w.All()
	paths := make([]string, 0, len(all))
	for _, p := range all {
		paths = append(paths, p.Path)
	}
	slices.SortFunc(paths, func(a, b string) int {
		switch {
		case a == b:
			return 0
		case a == ".":
			return 1
		case b == ".":
			return -1
		case len(a) != len(b):
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	return paths
}

// workspaceRel resolves an incoming path to a workspace-relative slash path,
// reporting false when it lands outside. A relative path is read against the
// workspace root, which is the form both a lease declaration and a classified
// file already carry.
//
// Symlinks are resolved on the incoming side only, matching Where: a workspace Root
// is already symlink-free, while an absolute path arriving from a shell or a host is
// whatever spelling that caller had. On macOS the two differ for anything under
// /tmp, and comparing them literally puts a path INSIDE the workspace outside it.
// A path that does not exist cannot be resolved and stands as written, which is
// right for a lease declaration naming a directory nobody has created yet.
func workspaceRel(root, p string) (string, bool) {
	if root == "" || strings.TrimSpace(p) == "" {
		return "", false
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	rel, err := filepath.Rel(root, filepath.Clean(abs))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// cleanRel normalizes a workspace-relative path to the spelling project paths use.
func cleanRel(rel string) string {
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	if rel == "" {
		return ""
	}
	return path.Clean(rel)
}
