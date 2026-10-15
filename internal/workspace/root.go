package workspace

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// workspaceMarker is the ONE file that declares a workspace root.
//
// Deliberately a single name, not a set. The earlier attempt listed go.mod here too,
// on the theory that it only appears at a repo's top: false in any multi-module repo,
// including this one (libs/diagnostics/go.mod, libs/gopherbuzz/go.mod). That reintroduced the
// exact defect it was meant to fix, one level down: running from libs/diagnostics made
// libs/diagnostics the workspace, so it locked libs/diagnostics/.magus/locks while a root run locked
// the root's, and the two stopped excluding each other despite touching the same files.
const workspaceMarker = "magus.yaml"

// projectMarkers mark a PROJECT, a unit INSIDE a workspace, and say nothing about
// where the workspace begins.
//
// Separating these from the workspace marker is the fix. Treating a magusfile as a
// root meant the walk halted at the first one it met, so running from a subproject
// silently redefined the workspace as that subproject: `magus ls` from a nested
// directory reported one project instead of the real set, and `affected` answered
// confidently about a workspace that did not exist.
var projectMarkers = []string{"magusfiles", "magusfile.buzz", "go.mod"}

// dirHasMarker reports whether dir carries any of the named markers. A stat error
// other than "not found" is reported as PRESENT: refusing to walk past a directory we
// cannot read is safer than silently continuing into whatever is above it and
// adopting an unrelated tree as the workspace.
func dirHasMarker(dir string, markers []string) bool {
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil || !errors.Is(err, fs.ErrNotExist) {
			return true
		}
	}
	return false
}

// FindRoot walks up from dir (or cwd when empty) to find the workspace root.
//
// The NEAREST magus.yaml wins, because it is the only file that declares "the
// workspace starts here" and the closest declaration is the governing one, the same
// rule .git follows, and the reason a git worktree nested inside its parent repo
// resolves to itself rather than being swallowed by the parent.
//
// Absent any magus.yaml, the root is the outermost CONTIGUOUS project marker.
// Contiguous so a stray magusfile in an unrelated ancestor (a home directory, /tmp)
// cannot silently adopt everything beneath it, which an unbounded walk would allow.
func FindRoot(dir string) (string, error) {
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd
	}
	cur, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	var contiguous string
	runBroken := false
	for {
		// Nearest wins: return the moment a declaration is found.
		if dirHasMarker(cur, []string{workspaceMarker}) {
			return cur, nil
		}
		if !runBroken {
			if dirHasMarker(cur, projectMarkers) {
				contiguous = cur
			} else if contiguous != "" {
				runBroken = true
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}

	if contiguous != "" {
		return contiguous, nil
	}
	return "", types.DiagnosticErrorf(types.NoWorkspaceRoot,
		"could not locate workspace root (no magus.yaml, magusfiles/, magusfile.buzz, or go.mod found); "+
			"run `%s` to bootstrap one", hint.Init)
}
