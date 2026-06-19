// Package interactive provides project scoring and session-state persistence
// for the magus x shorthand command.
package interactive

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/codec"
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

// State is the persisted interactive-session state.
// LastTarget is keyed by absolute project Dir.
type State struct {
	LastTarget map[string]string `json:"lastTarget,omitempty"`
}

// StatePath returns the path to the State file under XDG_STATE_HOME.
func StatePath() (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "magus", "x-state.json"), nil
}

// LoadState reads the persisted State. A missing file is not an error.
// A corrupt file returns a parse error; callers that want reset-on-corrupt
// behavior should treat a non-nil error as an empty State.
func LoadState() (State, error) {
	var s State
	path, err := StatePath()
	if err != nil {
		return s, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	if err := codec.Unmarshal(b, &s); err != nil {
		return s, err
	}
	return s, nil
}

// SaveState atomically writes s to the State file.
func SaveState(s State) error {
	path, err := StatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := codec.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
