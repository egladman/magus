package vcs

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// contentReader is what [DifferingPaths] reads, which every backend answers.
type contentReader interface {
	types.TreeReporter
	types.RevisionFileReader
}

// DifferingPaths is those of paths that differ between revisions a and b of
// dir's repository, cleaned and in order. A path neither holds reads the same,
// and a path one side holds differs. A backend declining the tree diff reads
// each path, which cannot see a mode change. An error means no answer: the
// tree diff failed or ctx ended.
func DifferingPaths(ctx context.Context, r contentReader, dir, a, b string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	differ, err := r.DiffTrees(ctx, dir, a, b)
	var declined *types.VCSUnsupportedError
	if err != nil && !errors.As(err, &declined) {
		return nil, err
	}
	readEach := err != nil
	var out []string
	for _, changed := range paths {
		p := cleanPath(changed)
		if !readEach {
			if slices.Contains(differ, p) {
				out = append(out, p)
			}
			continue
		}
		ours, oursErr := r.ReadFileAt(ctx, dir, a, p)
		theirs, theirsErr := r.ReadFileAt(ctx, dir, b, p)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if (oursErr != nil) != (theirsErr != nil) || ours != theirs {
			out = append(out, p)
		}
	}
	return out, nil
}

func cleanPath(p string) string {
	return path.Clean(filepath.ToSlash(strings.TrimSpace(p)))
}
