// Package interactive provides project scoring and session-state persistence
// for the magus x shorthand command.
package interactive

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/types"
)

// ScoredProject pairs a project with its ranking score from ScoreProjects.
type ScoredProject struct {
	P     *types.Project
	Score int
}

// ScoreProjects keeps every project that contains every filter token as a
// substring (AND), then ranks by leaf-anchored longest match against the
// first token. With no filters every project is kept and ranked alphabetically.
func ScoreProjects(all []*types.Project, filters []string) []ScoredProject {
	tokens := make([]string, 0, len(filters))
	for _, f := range filters {
		t := strings.ToLower(strings.TrimSpace(f))
		if t != "" {
			tokens = append(tokens, t)
		}
	}

	out := make([]ScoredProject, 0, len(all))
	for _, p := range all {
		path := strings.ToLower(p.Path)
		ok := true
		for _, t := range tokens {
			if !strings.Contains(path, t) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		score := 0
		if len(tokens) > 0 {
			score = LeafScore(p.Path, tokens[0])
		}
		out = append(out, ScoredProject{P: p, Score: score})
	}
	slices.SortStableFunc(out, func(a, b ScoredProject) int {
		if a.Score != b.Score {
			return cmp.Compare(b.Score, a.Score)
		}
		return cmp.Compare(a.P.Path, b.P.Path)
	})
	return out
}

// stateDir is <XDG state>/magus/x, holding one file per project: the target last
// run there.
//
// A DIRECTORY rather than the single x-state.json it replaces, for the reason
// connectors.d next to it is one. A map in one document made every pick a
// read-modify-write of every other project's entry, so two pickers finishing at once
// interleaved into it and both renamed it into place - and the read only ever wanted
// the current project's entry anyway. Nothing here reads another project's file, so
// there is nothing left to serialize, and a corrupt file costs one re-pick in one
// project instead of the whole map.
//
// Nothing prunes it. The entries are one small file per project the user has ever
// picked in, which is bounded by how many projects a person works in; an age-based
// sweep would delete exactly the long-tail entry - the project returned to after
// months - that remembering is for.
func stateDir() (string, error) {
	dir, err := config.UserStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "magus", "x"), nil
}

// targetPath is the file recording the last target for the project rooted at dir.
//
// The absolute dir is hashed rather than embedded, the same construction and width
// workspaceLockKey and symbols.IndexPath use to name a directory after an absolute
// path: it is the identity that matters, a path is not a legal single filename, and a
// digest keeps a listing of this directory from reproducing the user's disk layout.
// Not canonicalized through EvalSymlinks the way the symbols index is, because both
// sides of this one are the same Project.Dir in one process rather than two producers
// deriving a dir independently.
func targetPath(dir string) (string, error) {
	root, err := stateDir()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	sum := sha256.Sum256([]byte(filepath.Clean(abs)))
	return filepath.Join(root, hex.EncodeToString(sum[:8])), nil
}

// LastTarget returns the target last run for the project rooted at dir, "" when there
// is none. Every failure yields "" rather than an error: the value only pre-highlights
// a picker row, so a missing, unreadable, or truncated file must not stop x opening.
func LastTarget(dir string) string {
	path, err := targetPath(dir)
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// SaveLastTarget records target as the last one run for the project rooted at dir.
//
// Atomic even though the file holds one short line: two pickers finishing in the SAME
// project still race for it, and a plain write truncates before it fills, so the
// shorter name lands inside the longer one ("ci\nerage-badge-...", measured) and the
// next pick pre-highlights that.
func SaveLastTarget(dir, target string) error {
	path, err := targetPath(dir)
	if err != nil {
		return err
	}
	return file.WriteFileAtomic(path, []byte(target+"\n"), 0o644)
}
