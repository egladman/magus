package vcs

import (
	"context"
	"errors"
	"fmt"
	"maps"
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
// tree diff or a file read failed, or ctx ended.
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
		if oursErr != nil && !errors.Is(oursErr, ErrObjectMissing) {
			return nil, fmt.Errorf("read %s at %s: %w", p, a, oursErr)
		}
		if theirsErr != nil && !errors.Is(theirsErr, ErrObjectMissing) {
			return nil, fmt.Errorf("read %s at %s: %w", p, b, theirsErr)
		}
		if (oursErr != nil) != (theirsErr != nil) || ours != theirs {
			out = append(out, p)
		}
	}
	return out, nil
}

// revisionReader is what [DifferingPathsByRevision] reads.
type revisionReader interface {
	contentReader
	FindCommit(ctx context.Context, dir, rev string) (types.Commit, error)
}

// manyTreesDiffer is a backend that compares one revision against many in a
// fixed number of processes.
type manyTreesDiffer interface {
	// diffTreesAgainst is DiffTrees between head and each of revisions the
	// repository holds. One it lacks has no entry; a head it lacks is an error.
	diffTreesAgainst(ctx context.Context, root, head string, revisions []string) (map[string][]string, error)
}

// DifferingPathsByRevision is, for each revision paths names, those of its
// paths that differ between it and head, as [DifferingPaths] answers each
// pair. A revision the repository lacks has no entry; a head it lacks is an
// error, as is ctx ending. git answers every revision in two processes;
// another backend is asked a revision at a time.
func DifferingPathsByRevision(ctx context.Context, r revisionReader, dir, head string, paths map[string][]string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(paths) == 0 {
		return out, nil
	}
	revisions := slices.Sorted(maps.Keys(paths))
	if many, ok := r.(manyTreesDiffer); ok {
		differ, err := many.diffTreesAgainst(ctx, dir, head, revisions)
		if err != nil {
			return nil, err
		}
		for rev, changed := range differ {
			out[rev] = pathsAmong(changed, paths[rev])
		}
		return out, nil
	}
	if _, err := r.FindCommit(ctx, dir, head); err != nil {
		return nil, fmt.Errorf("find %s: %w", head, err)
	}
	for _, rev := range revisions {
		if _, err := r.FindCommit(ctx, dir, rev); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		differ, err := DifferingPaths(ctx, r, dir, rev, head, paths[rev])
		if err != nil {
			return nil, err
		}
		out[rev] = differ
	}
	return out, nil
}

// pathsAmong is those of paths, cleaned and in order, that changed names.
func pathsAmong(changed, paths []string) []string {
	differ := make(map[string]bool, len(changed))
	for _, p := range changed {
		differ[p] = true
	}
	var out []string
	for _, p := range paths {
		if p = cleanPath(p); differ[p] {
			out = append(out, p)
		}
	}
	return out
}

func cleanPath(p string) string {
	return path.Clean(filepath.ToSlash(strings.TrimSpace(p)))
}
