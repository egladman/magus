package vcs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The git hooks that settle a merge-shaped operation, and the operation each sees.
// A merge git makes the commit for itself, a commit concluding a merge, cherry-pick or
// revert git stopped on, the commit of a clean cherry-pick or revert, a finished rebase,
// and the last patch of an am series. Each fires once the tree is whole: every path is
// merged, nothing is unmerged or unwritten, which the merge driver, invoked per file
// mid-merge, never sees.
const (
	HookPreMergeCommit = "pre-merge-commit"
	HookPreCommit      = "pre-commit"
	HookPostCommit     = "post-commit"
	HookPostRewrite    = "post-rewrite"
	HookPostApplypatch = "post-applypatch"
)

// SettleHooks are the hooks InstallRegenHook writes, in the order git fires them across
// one merge.
var SettleHooks = []string{HookPreMergeCommit, HookPreCommit, HookPostCommit, HookPostRewrite, HookPostApplypatch}

// SettleHooksMissing names the settle hooks of root's repository that carry no
// magus-regenerate section, in SettleHooks order; none when every one is installed.
// Outside a git repository nothing is missing, since nothing could be installed.
func SettleHooksMissing(ctx context.Context, root string) ([]string, error) {
	paths, ok, err := gitRepoPathsOf(ctx, root)
	if err != nil || !ok {
		return nil, err
	}
	var missing []string
	for _, name := range SettleHooks {
		present, err := managedSectionPresent(filepath.Join(paths.hooksDir, name), regenMarkers)
		if err != nil {
			return nil, err
		}
		if !present {
			missing = append(missing, name)
		}
	}
	return missing, nil
}

// HookEvent is one invocation of a settle hook: which hook, its positional arguments,
// and the index git exported to it.
type HookEvent struct {
	Hook string
	Args []string
	// IndexFile is GIT_INDEX_FILE as git exported it to the hook, made absolute against
	// the hook's working directory; "" when git exported none. `git commit -a` and
	// `git commit <path>` build the commit from a temporary index named there, and a
	// stage that ignores it lands in the wrong one.
	IndexFile string
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
	// indexFile is the index a stage writes; see HookEvent.IndexFile.
	indexFile string
	// gitDir is the worktree's git directory, absolute.
	gitDir string
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
		return fmt.Errorf("vcs: git write-tree: %w", err)
	}
	if err := os.WriteFile(filepath.Join(op.gitDir, settledTreeFile), []byte(tree+"\n"), 0o644); err != nil {
		return fmt.Errorf("vcs: write %s: %w", settledTreeFile, err)
	}
	return nil
}

// HookTreeSettled reports whether the operation's index holds the tree the last
// HookRecordSettledTree wrote, and forgets that record either way.
func HookTreeSettled(ctx context.Context, root string, op HookOperation) (bool, error) {
	path := filepath.Join(op.gitDir, settledTreeFile)
	recorded := readTrimmed(op.gitDir, settledTreeFile)
	if recorded == "" {
		return false, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("vcs: remove %s: %w", settledTreeFile, err)
	}
	tree, err := gitOutput(ctx, root, op.gitOpts(gitOpts{}), "write-tree")
	if err != nil {
		return false, fmt.Errorf("vcs: git write-tree: %w", err)
	}
	return tree == recorded, nil
}

// GitHookOperation reads which operation ev belongs to and what it changed, without
// touching the tree. ok is false when the hook fired for something no settle applies to:
// an ordinary commit, a pick inside a rebase (post-rewrite settles the whole rebase once
// it finishes), a cherry-pick or revert with picks still to come, an amend, or a patch of
// an am series before the last. A hook name this package does not install is an error.
func GitHookOperation(ctx context.Context, root string, ev HookEvent) (HookOperation, bool, error) {
	gitDir, err := gitOutput(ctx, root, gitOpts{}, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return HookOperation{}, false, fmt.Errorf("vcs: git rev-parse: %w", err)
	}
	state := func(name string) bool {
		_, err := os.Stat(filepath.Join(gitDir, name))
		return err == nil
	}
	rebasing := state("rebase-merge") || state("rebase-apply")
	op := HookOperation{indexFile: ev.IndexFile, gitDir: gitDir}
	switch ev.Hook {
	case HookPreMergeCommit:
		op.Kind, op.CommitPending = "merge", true
	case HookPreCommit:
		switch {
		case rebasing:
			return HookOperation{}, false, nil
		case state("MERGE_HEAD"):
			op.Kind = "merge"
		case state("CHERRY_PICK_HEAD"):
			op.Kind = "cherry-pick"
		case state("REVERT_HEAD"):
			op.Kind = "revert"
		default:
			return HookOperation{}, false, nil
		}
		op.CommitPending = true
	case HookPostCommit:
		switch {
		case rebasing:
			return HookOperation{}, false, nil
		case state("CHERRY_PICK_HEAD"):
			op.Kind = "cherry-pick"
		case state("REVERT_HEAD"):
			op.Kind = "revert"
		default:
			// A clean single revert writes no REVERT_HEAD, so it is indistinguishable
			// from an ordinary commit here and is not settled.
			return HookOperation{}, false, nil
		}
		// Staging output after a pick with more picks to come would make the next one
		// refuse over a dirty index. The last pick's todo still lists itself.
		if pending, err := sequencerPending(gitDir); err != nil {
			return HookOperation{}, false, err
		} else if pending > 1 {
			return HookOperation{}, false, nil
		}
	case HookPostRewrite:
		if len(ev.Args) == 0 || ev.Args[0] != "rebase" {
			return HookOperation{}, false, nil
		}
		op.Kind = "rebase"
	case HookPostApplypatch:
		next, last := readTrimmed(gitDir, "rebase-apply", "next"), readTrimmed(gitDir, "rebase-apply", "last")
		if next == "" || next != last {
			return HookOperation{}, false, nil
		}
		op.Kind = "am"
	default:
		return HookOperation{}, false, fmt.Errorf("vcs: %q is not a hook magus settles a merge from (one of %s)", ev.Hook, strings.Join(SettleHooks, ", "))
	}
	op.Changed, err = hookChangedFiles(ctx, root, gitDir, op)
	if err != nil {
		return HookOperation{}, false, err
	}
	return op, true, nil
}

// hookChangedFiles lists what op changed. Before the commit that is the index against
// HEAD; after it, the commits the operation made: a rebase's replayed commits (from the
// `onto` its state dir still names during post-rewrite), an am series from where it
// started, one pick or revert from its parent.
func hookChangedFiles(ctx context.Context, root, gitDir string, op HookOperation) ([]string, error) {
	// --no-renames for the reason ChangedFiles gives: a rename changes the project the
	// file left too.
	args := []string{"diff", "--name-only", "--no-renames", "--ignore-submodules=none"}
	switch {
	case op.CommitPending:
		args = append(args, "--cached", "HEAD")
	case op.Kind == "rebase":
		onto := readTrimmed(gitDir, "rebase-merge", "onto")
		if onto == "" {
			onto = readTrimmed(gitDir, "rebase-apply", "onto")
		}
		if onto == "" {
			// The state dir is gone, so what upstream changed is counted too: a
			// superset, never less than the replayed commits.
			onto = "ORIG_HEAD"
		}
		args = append(args, onto, "HEAD")
	case op.Kind == "am":
		args = append(args, "ORIG_HEAD", "HEAD")
	default:
		args = []string{"diff-tree", "--no-commit-id", "--name-only", "-r", "--no-renames", "HEAD"}
	}
	out, err := gitOutput(ctx, root, op.gitOpts(gitOpts{}), args...)
	if err != nil {
		return nil, fmt.Errorf("vcs: git %s: %w", strings.Join(args[:2], " "), err)
	}
	return splitLines([]byte(out)), nil
}

// HookDirtyFiles lists the paths modified or untracked in root as the operation's index
// sees them: what a regeneration run after GitHookOperation wrote.
func HookDirtyFiles(ctx context.Context, root string, op HookOperation) ([]string, error) {
	out, err := gitOutput(ctx, root, op.gitOpts(gitOpts{KeepLeadingSpace: true}), "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return nil, fmt.Errorf("vcs: git status: %w", err)
	}
	return gitStatusPaths(splitStatusLines(out)), nil
}

// HookStage stages paths into the operation's index, so a commit git has not made yet
// carries them and one it has made can be amended with them. Paths are literal.
func HookStage(ctx context.Context, root string, op HookOperation, paths []string) error {
	for _, chunk := range gitPathChunks(paths) {
		argv := slices.Concat([]string{"add", "--"}, chunk)
		if _, err := gitOutput(ctx, root, op.gitOpts(gitOpts{Literal: true}), argv...); err != nil {
			return fmt.Errorf("vcs: git add: %w", err)
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

// sequencerPending counts the picks or reverts a multi-commit sequence still has to
// do, the one being committed included; 0 with no sequencer state.
func sequencerPending(gitDir string) (int, error) {
	f, err := os.Open(filepath.Join(gitDir, "sequencer", "todo"))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("vcs: read sequencer todo: %w", err)
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		word, _, _ := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		switch word {
		case "pick", "p", "revert":
			n++
		}
	}
	return n, sc.Err()
}

func readTrimmed(gitDir string, elem ...string) string {
	data, err := os.ReadFile(filepath.Join(append([]string{gitDir}, elem...)...))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
