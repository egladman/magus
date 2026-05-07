package interactive

import (
	"cmp"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/project"
)

// ScoredFile pairs a workspace-relative file path with its leaf-anchored
// match score. Path uses forward slashes.
type ScoredFile struct {
	Path  string
	Score int // higher is better; only meaningful relative to other results from the same call
}

// SearchFiles walks wsRoot and returns regular files matching all filter tokens (AND, case-insensitive)
// and matchFn, ranked by LeafScore. Returns nil,nil when filters is empty and matchFn is nil.
func SearchFiles(ctx context.Context, wsRoot string, filters []string, matchFn func(absPath string) bool) ([]ScoredFile, error) {
	tokens := make([]string, 0, len(filters))
	for _, f := range filters {
		t := strings.ToLower(strings.TrimSpace(f))
		if t != "" {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) == 0 && matchFn == nil {
		return nil, nil
	}

	var out []ScoredFile
	err := filepath.WalkDir(wsRoot, func(absPath string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			// Don't prune the root itself — only subdirectories.
			if absPath != wsRoot && project.IsIgnoreDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		if matchFn != nil && !matchFn(absPath) {
			return nil
		}

		rel, rerr := filepath.Rel(wsRoot, absPath)
		if rerr != nil {
			return nil //nolint:nilerr // skip entries whose relative path can't be computed
		}
		rel = filepath.ToSlash(rel)
		lcRel := strings.ToLower(rel)

		for _, t := range tokens {
			if !strings.Contains(lcRel, t) {
				return nil
			}
		}

		score := 0
		if len(tokens) > 0 {
			score = LeafScore(rel, tokens[0])
		}
		out = append(out, ScoredFile{Path: rel, Score: score})
		return nil
	})
	if err != nil {
		return nil, err
	}

	slices.SortStableFunc(out, func(a, b ScoredFile) int {
		if a.Score != b.Score {
			return cmp.Compare(b.Score, a.Score)
		}
		return cmp.Compare(a.Path, b.Path)
	})
	if out == nil {
		return []ScoredFile{}, nil
	}
	return out, nil
}
