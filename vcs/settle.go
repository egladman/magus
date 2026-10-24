package vcs

import (
	"context"
	"fmt"
	"slices"
)

// HookOperation is the merge-shaped operation a hook event belongs to, and the paths
// it changed, which bound what has to regenerate.
type HookOperation struct {
	// Kind is merge, cherry-pick, revert, rebase or am.
	Kind string
	// Changed are repository-relative slash paths the operation changed: the index
	// against HEAD while git has not committed yet, the commits it made otherwise.
	Changed []string
	// CommitPending reports that git has not made the commit yet, so a path staged
	// before the hook returns lands in it.
	CommitPending bool
	// indexFile is the index a stage writes: the one git exported to the hook, absolute.
	indexFile string
	// gitDir is the worktree's git directory, absolute.
	gitDir string
	// base is the revision the operation started from (the commit before it, or the start of its sequence).
	base string
}

// hookChangedFiles lists what op changed: the index against its base while git has not
// committed yet, HEAD against its base otherwise.
func hookChangedFiles(ctx context.Context, root string, op HookOperation) ([]string, error) {
	// --no-renames for the reason ChangedFiles gives: a rename changes the project the
	// file left too.
	args := []string{"diff", "--name-only", "--no-renames", "--ignore-submodules=none"}
	if op.CommitPending {
		args = append(args, "--cached", op.base)
	} else {
		args = append(args, op.base, "HEAD")
	}
	out, err := gitOutput(ctx, root, op.gitOpts(gitOpts{}), args...)
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	return splitLines([]byte(out)), nil
}

// HookDirtyFiles lists the paths whose working tree differs from the operation's index,
// untracked ones included: what a regeneration run after the operation wrote, and so
// what HookStage has left to add. A path the index already holds as the tree has it is
// not listed, since staging it again adds nothing and, for one the merge deleted, names
// a path git add cannot match.
func HookDirtyFiles(ctx context.Context, root string, op HookOperation) ([]string, error) {
	out, err := gitOutput(ctx, root, op.gitOpts(gitOpts{KeepLeadingSpace: true}), "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	lines := slices.DeleteFunc(splitStatusLines(out), func(line string) bool {
		return len(line) < 2 || line[1] == ' '
	})
	return gitStatusPaths(lines), nil
}

// HookStage stages paths into the operation's index, so a commit git has not made yet
// carries them and one it has made can be amended with them. Paths are literal.
func HookStage(ctx context.Context, root string, op HookOperation, paths []string) error {
	for _, chunk := range gitPathChunks(paths) {
		argv := slices.Concat([]string{"add", "--"}, chunk)
		if _, err := gitOutput(ctx, root, op.gitOpts(gitOpts{Literal: true}), argv...); err != nil {
			return fmt.Errorf("git add: %w", err)
		}
	}
	return nil
}

// gitOpts adds the operation's index to o. gitEnviron scrubs GIT_INDEX_FILE from the
// process environment, which is right for every other caller and wrong inside a hook.
func (op HookOperation) gitOpts(o gitOpts) gitOpts {
	if op.indexFile != "" {
		o.Env = append(o.Env, "GIT_INDEX_FILE="+op.indexFile)
	}
	return o
}
