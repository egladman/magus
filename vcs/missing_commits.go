package vcs

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// workReader is what missingCommits reads, which every backend answers.
type workReader interface {
	types.RangeReporter
	types.TreeReporter
	types.RevisionFileReader
}

// missingCommits is MissingCommits for every backend. It merges nothing, so
// no merge driver runs. Only commits since the two diverged can be copies of
// head's, so it reads no further back.
func missingCommits(ctx context.Context, r workReader, dir, branch, head string) (int, error) {
	commits, err := r.RangeCommits(ctx, dir, branch, head, nil)
	if err != nil {
		return 0, err
	}
	if len(commits) == 0 || contentOnBranch(ctx, r, dir, branch, head) {
		return 0, nil
	}
	onBranch, err := r.RangeCommits(ctx, dir, head, branch, nil)
	if err != nil {
		return 0, err
	}
	copies := make(map[string]bool, len(onBranch))
	for _, c := range onBranch {
		copies[replayIdentity(c)] = true
	}
	missing := 0
	for _, c := range commits {
		if len(c.Parents) < 2 && !copies[replayIdentity(c)] {
			missing++
		}
	}
	return missing, nil
}

// contentOnBranch reports whether every file head changed since it diverged
// reads the same at branch. A squash of several branches holds no commit of
// any of them. A file either side cannot read leaves it unproven.
func contentOnBranch(ctx context.Context, r workReader, dir, branch, head string) bool {
	paths, err := r.RangeFiles(ctx, dir, branch, head, nil)
	if err != nil || len(paths) == 0 {
		return false
	}
	// One call against a read per file: a wide branch took seconds.
	if differ, err := r.DiffTrees(ctx, dir, branch, head); err == nil {
		return !slices.ContainsFunc(paths, func(changed string) bool {
			return slices.Contains(differ, cleanPath(changed))
		})
	}
	for _, changed := range paths {
		p := cleanPath(changed)
		ours, err := r.ReadFileAt(ctx, dir, head, p)
		if err != nil {
			return false
		}
		if theirs, err := r.ReadFileAt(ctx, dir, branch, p); err != nil || theirs != ours {
			return false
		}
	}
	return true
}

func cleanPath(p string) string {
	return path.Clean(filepath.ToSlash(strings.TrimSpace(p)))
}

// replayIdentity reads a fixup as the commit it folds into.
func replayIdentity(c types.Commit) string {
	return fmt.Sprintf("%s\x00%s", c.Author.Email, strings.TrimPrefix(c.Subject, "fixup! "))
}
