package vcs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The git hooks an earlier magus settled a merge-shaped operation in.
const (
	HookPreMergeCommit = "pre-merge-commit"
	HookPreCommit      = "pre-commit"
	HookPostCommit     = "post-commit"
	HookPostRewrite    = "post-rewrite"
	HookPostApplypatch = "post-applypatch"
)

// SettleHooks are the hooks an earlier magus wrote a blocking magus-regenerate section
// into, and InstallRegenHook now strips it from.
var SettleHooks = []string{HookPreMergeCommit, HookPreCommit, HookPostCommit, HookPostRewrite, HookPostApplypatch}

// SettleHooksStale names the hooks of root's repository, post-merge and SettleHooks, that
// still carry the magus-regenerate section, in that order; none when all are clean.
// Outside a git repository nothing is stale, since nothing could be installed.
func SettleHooksStale(ctx context.Context, root string) ([]string, error) {
	paths, ok, err := resolveGitRepoPaths(ctx, root)
	if err != nil || !ok {
		return nil, err
	}
	var stale []string
	for _, name := range append([]string{"post-merge"}, SettleHooks...) {
		present, err := managedSectionPresent(filepath.Join(paths.hooksDir, name), regenMarkers)
		if err != nil {
			return nil, err
		}
		if present {
			stale = append(stale, name)
		}
	}
	return stale, nil
}

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

// settledTreeFile, in the worktree's git dir, names the tree a settle left staged for
// a commit git had not made yet. The settle of a clean merge stops git's own commit, and
// the `git commit` the person runs next fires pre-commit on that same tree; the record
// is what tells it so, and it answers one commit.
const settledTreeFile = "magus-settled-tree"

// HookRecordSettledTree writes the tree the operation's index holds now.
func HookRecordSettledTree(ctx context.Context, root string, op HookOperation) error {
	tree, err := gitOutput(ctx, root, op.gitOpts(gitOpts{}), "write-tree")
	if err != nil {
		return fmt.Errorf("git write-tree: %w", err)
	}
	if err := os.WriteFile(filepath.Join(op.gitDir, settledTreeFile), []byte(tree+"\n"), 0o644); err != nil {
		return fmt.Errorf("vcs: write %s: %w", settledTreeFile, err)
	}
	return nil
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

func readTrimmed(gitDir string, elem ...string) string {
	data, err := os.ReadFile(filepath.Join(append([]string{gitDir}, elem...)...))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
