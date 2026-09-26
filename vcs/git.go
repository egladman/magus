package vcs

import (
	"archive/tar"
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/types"
)

type gitVCS struct{}

func (v gitVCS) Name() string     { return "git" }
func (v gitVCS) Claims() []string { return []string{".git"} }
func (v gitVCS) Base() string     { return "origin/main" }

// ReviewCommand reads the INDEX, which is git's alone: it is the only one of the four
// with a staging area between the working copy and a commit.
func (v gitVCS) ReviewCommand() string { return "git diff --cached --stat" }

// ParentRef is the first parent of the checked-out commit. `^` rather than `~1`:
// they are the same for a linear commit and differ on a merge, where `^` is the
// branch being merged INTO, which is the side a CI run wants to measure from.
func (v gitVCS) ParentRef() string { return "HEAD^" }

// IsSecondaryCheckout reports whether dir is a linked git worktree: its .git is a
// FILE whose gitdir points under another repo's .git/worktrees/. The main checkout
// has a .git DIRECTORY, and a submodule's gitdir points under .git/modules/, so
// neither is treated as a linked worktree.
func (v gitVCS) IsSecondaryCheckout(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return false // absent, or a directory (the main checkout); either way not linked
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return false
	}
	return strings.Contains(filepath.ToSlash(strings.TrimSpace(rest)), "/.git/worktrees/")
}

// OtherCheckouts implements types.CheckoutLister from the files `git worktree list` reads:
// the primary checkout is the parent of a non-bare common dir, and each linked
// worktree's admin dir holds a gitdir file naming its .git. A bare repository's own
// common dir names no checkout (matching its basename against ".git" would only ever
// recognize the non-bare shape), so the primary is recognized structurally instead:
// its parent is a checkout of common exactly when ITS OWN common dir is common too.
// That also covers the "bare repo alongside a root checkout" layout, where the bare
// dir sits inside the primary rather than beside it, and the primary is discovered
// the same way a linked worktree is: by resolving its own gitdir chain.
func (v gitVCS) OtherCheckouts(root string) ([]string, error) {
	common := gitCommonDir(root)
	self, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	var out []string
	add := func(dir string) {
		real, err := filepath.EvalSymlinks(dir)
		if err != nil || real == self || slices.Contains(out, real) {
			return
		}
		if fi, err := os.Stat(real); err == nil && fi.IsDir() {
			out = append(out, real)
		}
	}
	if parent := filepath.Dir(common); gitCommonDir(parent) == common {
		add(parent)
	}
	entries, err := os.ReadDir(filepath.Join(common, "worktrees"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(common, "worktrees", e.Name(), "gitdir"))
		if err != nil {
			continue
		}
		add(filepath.Dir(strings.TrimSpace(string(b))))
	}
	return out, nil
}

// ConfiguredRemote implements types.RemoteConfigReporter by reading .git/config, the
// same answer `git remote get-url origin` gives without starting git. It resolves a
// linked worktree or submodule to the shared config first, since only that one carries
// remotes.
func (v gitVCS) ConfiguredRemote(dir string) (string, error) {
	if u := gitConfigRemote(gitCommonDir(dir)); u != "" {
		return u, nil
	}
	return "", types.ErrVCSUnsupported
}

func (v gitVCS) Root(ctx context.Context, dir string) (string, error) {
	return gitOutput(ctx, dir, gitOpts{}, "rev-parse", "--show-toplevel")
}

// ChangedFiles lists the files changed against base, diffing the merge-base of base and
// HEAD against the WORKING TREE rather than base...HEAD. The three-dot form is
// commit-to-commit and silently ignores uncommitted work, so editing without committing
// reported "0 projects affected". With a clean tree the two are equal, so CI is
// unchanged.
//
// NOT purely a read: in a shallow clone whose merge base is missing it fetches more
// history first (see recoverMergeBase). A full clone is never fetched into.
func (v gitVCS) ChangedFiles(ctx context.Context, dir, base string) ([]string, error) {
	if err := checkRev(base); err != nil {
		return nil, err
	}
	mergeBase, err := gitOutput(ctx, dir, gitOpts{}, "merge-base", base, "HEAD")
	if err != nil {
		recovered := v.recoverMergeBase(ctx, dir, base)
		if recovered == "" {
			// Report the original failure, not the recovery's: a shallow clone that could
			// not be deepened is still, to the caller, a repository with no merge base.
			return nil, fmt.Errorf("git merge-base: %w", err)
		}
		mergeBase = recovered
	}
	// --no-renames because a rename's old path is a change to its project too: under
	// diff.renames (git's default) only the new path came back, and the project the file
	// left was never rebuilt. --ignore-submodules=none for the same reason, against
	// diff.ignoreSubmodules.
	out, err := gitOutput(ctx, dir, gitOpts{}, "diff", "--name-only", "--no-renames", "--ignore-submodules=none", mergeBase)
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	files := splitLines([]byte(out))
	// Untracked-but-not-ignored files (new source files) are conceptually part of
	// the working tree, but git diff omits them. List them explicitly so a brand-new
	// file seeds its project the same way a modified one does. --exclude-standard
	// honors .gitignore, so build artifacts stay out.
	untracked, err := gitOutput(ctx, dir, gitOpts{}, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	return append(files, splitLines([]byte(untracked))...), nil
}

// BranchChanges reports what OTHER branches, local and remote-tracking, are changing, most
// recently updated first, so a reader can be told the file in front of them is also being
// edited elsewhere.
//
// It does NOT fetch. A remote-tracking answer is exactly as fresh as the reader's last fetch;
// going and getting more would be a network act nobody asked for, on a path that exists to
// annotate a diff, so BranchChange.Local tells a caller which answers need "as of your last
// fetch".
//
// One fork lists the refs and one more diffs each of them, so the cost is limit+1 forks and the
// cap belongs here rather than in the caller: git applies it to the ref listing and no diff is
// ever run for a branch that was going to be discarded. The three-dot form computes the merge base
// internally, which is what keeps it to one fork per branch rather than two.
func (v gitVCS) BranchChanges(ctx context.Context, dir, base string, limit int) ([]types.BranchChange, error) {
	if limit <= 0 {
		return nil, nil
	}
	if err := checkRev(base); err != nil {
		return nil, err
	}
	// Deliberately over-asked. FOUR kinds of ref are dropped below: <remote>/HEAD, the reader's
	// own branch, one whose diff fails, and one whose diff is empty (which always includes base
	// itself). A budget of limit+1 covered exactly one of them, so a repository with a few
	// stale remotes silently returned fewer branches than asked with no sign the list was short.
	// Listing refs is one fork whatever the count; only the per-branch diffs are paid per entry,
	// and the loop stops at limit.
	// Both ref spaces, and the FULL refname rather than the short one, because the short form
	// throws away the single fact the caller needs to caption the answer: whether this branch is
	// here or is a copy of somebody else's as of the last fetch.
	refs, err := gitOutput(ctx, dir, gitOpts{}, "for-each-ref",
		"--sort=-committerdate", "--count", strconv.Itoa(limit*2+8),
		"--format=%(refname)", "refs/heads/", "refs/remotes/")
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}
	mine, err := gitOutput(ctx, dir, gitOpts{}, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git rev-parse: %w", err)
	}
	out := make([]types.BranchChange, 0, limit)
	// A local branch and its remote-tracking copy are ONE line of work under two names, and
	// reporting both would tell the reader two people are editing a file when one is. Local wins:
	// it is the current answer, where the tracking copy is only as new as the last fetch. Sorted
	// by committerdate, so the copy git listed first is not reliably the fresher one; the name
	// decides, not the order.
	seen := make(map[string]bool, limit)
	for _, pass := range []bool{true, false} {
		for _, ref := range splitLines([]byte(refs)) {
			if len(out) == limit {
				break
			}
			local := strings.HasPrefix(ref, headsPrefix)
			if local != pass {
				continue // locals first, so a tracking copy of one already taken is skipped below
			}
			short, ok := shortBranchName(ref)
			if !ok || seen[short] {
				continue
			}
			// <remote>/HEAD is a symbolic pointer at the default branch, so it duplicates
			// whatever it aims at and names no line of work of its own.
			if short == "HEAD" {
				continue
			}
			// A detached HEAD reports the literal "HEAD" here and matches nothing, which is
			// right: there is no branch of the reader's own to exclude.
			if short == strings.TrimSpace(mine) {
				continue
			}
			if err := checkRev(ref); err != nil {
				// A ref git itself listed, so this is not the caller-supplied case checkRev
				// exists for, but a name that cannot be passed safely is skipped rather
				// than trusted.
				continue
			}
			// Both sides of a rename, as ChangedFiles reports them.
			paths, err := gitOutput(ctx, dir, gitOpts{}, "diff", "--name-only", "--no-renames", "--ignore-submodules=none", base+"..."+ref)
			if err != nil {
				// One unreachable branch is not a reason to report none: a ref can vanish
				// between the listing and the diff, and the rest of the answer is still true.
				continue
			}
			lines := splitLines([]byte(paths))
			if len(lines) == 0 {
				continue
			}
			seen[short] = true
			out = append(out, types.BranchChange{Ref: short, Paths: lines, Local: local})
		}
	}
	return out, nil
}

// headsPrefix and remotesPrefix are the two ref spaces BranchChanges reads.
const (
	headsPrefix   = "refs/heads/"
	remotesPrefix = "refs/remotes/"
)

// shortBranchName reduces a full refname to the name a reader would use, and reports whether it
// was a branch ref at all.
//
// The remote segment comes off, whatever it is called. Trimming the literal "origin/" was wrong in
// the ordinary fork setup: an `upstream/feat/x` kept its prefix, so it never matched the reader's
// own branch name and came back as somebody else competing on the very files they were editing.
//
// Dropping it is also what lets a local branch and its remote-tracking copy compare equal, which
// is how BranchChanges tells one line of work under two names from two lines of work.
func shortBranchName(ref string) (string, bool) {
	switch {
	case strings.HasPrefix(ref, headsPrefix):
		name := strings.TrimPrefix(ref, headsPrefix)
		return name, name != ""
	case strings.HasPrefix(ref, remotesPrefix):
		remote, name, ok := strings.Cut(strings.TrimPrefix(ref, remotesPrefix), "/")
		return name, ok && remote != "" && name != ""
	default:
		return "", false
	}
}

// RangeDiff returns the unified diff of what head added since it diverged from base.
//
// Three dots, so the answer is the symmetric difference and never charges the reader for commits
// that landed on base while the branch was open. That is the same form BranchChanges uses, and for
// the same reason: both questions are about somebody's branch, not about how far the base has moved.
//
// A non-existent revision is reported rather than swallowed. BranchChanges skips a ref whose diff
// fails because it holds many and the rest are still true; here the range IS the request, and a
// caller handed "" would read it as a branch that changed nothing.
func (v gitVCS) RangeDiff(ctx context.Context, dir, base, head string, paths []string) (string, error) {
	if err := checkRequiredRev(base, head); err != nil {
		return "", err
	}
	// Histogram rather than the default myers because this patch is what remarks anchor into: myers
	// reports a moved function as a delete plus an unrelated insert, while histogram anchors on
	// lines unique to both sides and keeps the move legible as a move.
	// --no-ext-diff and --no-textconv because the box's diff drivers would put their output,
	// not git's patch, in front of the parser.
	args := []string{"diff", "--histogram", "--no-ext-diff", "--no-textconv", base + "..." + head}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := gitOutput(ctx, dir, gitOpts{}, args...)
	if err != nil {
		return "", fmt.Errorf("git diff %s...%s: %w", base, head, err)
	}
	return out, nil
}

// RangeFiles implements types.RangeReporter with plumbing: `diff-tree` from the merge
// base reads neither diff.renames nor diff.ignoreSubmodules, either of which would hide a
// path from a porcelain `git diff`.
func (v gitVCS) RangeFiles(ctx context.Context, dir, base, head string, paths []string) ([]string, error) {
	if err := checkRequiredRev(base, head); err != nil {
		return nil, err
	}
	mb, err := gitOutput(ctx, dir, gitOpts{}, "merge-base", base, head)
	if err != nil {
		return nil, fmt.Errorf("git merge-base %s %s: %w", base, head, err)
	}
	return gitDiffTree(ctx, dir, mb, head, paths)
}

// gitDiffTree lists the repository-relative paths whose content differs between two trees
// or commits, a rename being its old path and its new one, narrowed to paths when given.
func gitDiffTree(ctx context.Context, dir, a, b string, paths []string) ([]string, error) {
	at, err := gitTopWhen(ctx, dir, paths)
	if err != nil {
		return nil, err
	}
	args := append([]string{"diff-tree", "-r", "--no-renames", "--name-only", "-z", a, b, "--"}, paths...)
	out, err := gitOutput(ctx, at, gitOpts{Literal: true, KeepLeadingSpace: true}, args...)
	if err != nil {
		return nil, fmt.Errorf("git diff-tree %s %s: %w", a, b, err)
	}
	return splitNUL(out), nil
}

// gitTopWhen returns the directory a call taking repository-relative paths runs in: the
// top level when there are paths, since git reads a pathspec from its working directory,
// and dir otherwise, which spares the lookup.
func gitTopWhen(ctx context.Context, dir string, paths []string) (string, error) {
	if len(paths) == 0 {
		return dir, nil
	}
	top, err := gitOutput(ctx, dir, gitOpts{}, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	return top, nil
}

// splitNUL splits -z output into its non-empty fields.
func splitNUL(s string) []string {
	var out []string
	for f := range strings.SplitSeq(s, "\x00") {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// RangeCommits implements types.RangeReporter by listing ids with `rev-list` and
// resolving each through FindCommit, the one commit parser. paths run from the top level,
// literally, so a path RangeFiles returned narrows to exactly that path.
func (v gitVCS) RangeCommits(ctx context.Context, dir, base, head string, paths []string) ([]types.Commit, error) {
	if err := checkRequiredRev(base, head); err != nil {
		return nil, err
	}
	at, err := gitTopWhen(ctx, dir, paths)
	if err != nil {
		return nil, err
	}
	args := append([]string{"rev-list", base + ".." + head, "--"}, paths...)
	out, err := gitOutput(ctx, at, gitOpts{Literal: true}, args...)
	if err != nil {
		return nil, fmt.Errorf("git rev-list %s..%s: %w", base, head, err)
	}
	return resolveEach(ctx, dir, v, splitLines([]byte(out)))
}

// IsAncestor implements types.AncestryReporter. merge-base's exit 1 is its answer "no";
// every other failure is it declining to answer (an unknown revision, or a shallow clone
// missing the history that would decide).
func (v gitVCS) IsAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error) {
	if err := checkRev(ancestor, descendant); err != nil {
		return false, err
	}
	_, err := gitOutput(ctx, dir, gitOpts{}, "merge-base", "--is-ancestor", ancestor, descendant)
	switch {
	case err == nil:
		return true, nil
	case exitCode(err) == 1:
		return false, nil
	default:
		return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w", ancestor, descendant, err)
	}
}

// recoverMergeBase fetches history until base and HEAD share an ancestor in a shallow
// clone, returning that merge base, or "" when it cannot get one.
//
// A shallow CI checkout holds HEAD, and maybe base, without their common ancestor, so
// `git merge-base` fails the same way it does for a ref that does not exist. Diff cannot
// tell those apart, affected reports MGS1010, and the run builds every project instead of
// the affected ones. Fetching just enough history makes the cost track how far the branch
// diverged, rather than the whole life of the repository.
//
// Both sides need history and neither fetch alone supplies it: fetching only base leaves
// HEAD grafted at its original depth, and the remote's configured refspec never brings
// base in at all. Each round does both.
//
// Which flag depends on whether the ref is already here, and getting it wrong DESTROYS
// history:
//
//   - --depth is absolute in both directions: `git fetch --depth=32` against a checkout
//     cloned at depth 50 leaves it holding 32. Anything already present is grown with
//     --deepen, which only ever extends the boundary.
//   - A base ref arriving for the FIRST time still needs --depth, because --deepen has no
//     boundary to extend and would let the ref land with all its history.
//
// Only a shallow repository is touched. A shallow developer checkout IS fetched into,
// which only ever adds history, never removes it.
func (v gitVCS) recoverMergeBase(ctx context.Context, dir, base string) string {
	if shallow, err := gitOutput(ctx, dir, gitOpts{}, "rev-parse", "--is-shallow-repository"); err != nil || shallow != "true" {
		return ""
	}
	// base is a remote-tracking name ("origin/main") whose first segment must be a
	// CONFIGURED remote. Verifying that is load-bearing, not a courtesy: the segment is
	// passed to `git fetch` as the repository argument, which is a URL sink, so an
	// unchecked value sends git off to whatever it names. ("refs/remotes/origin/main"
	// would have it looking for a repository at the relative path ./refs.)
	remote, branch, _ := strings.Cut(base, "/")
	if branch == "" {
		slog.DebugContext(ctx, "cannot deepen: base ref is not a remote-tracking name", slog.String("base", base))
		return ""
	}
	if _, err := gitOutput(ctx, dir, gitOpts{}, "config", "--get", "remote."+remote+".url"); err != nil {
		slog.DebugContext(ctx, "cannot deepen: base ref names no configured remote",
			slog.String("base", base), slog.String("remote", remote))
		return ""
	}
	tracking := fmt.Sprintf("refs/remotes/%s/%s", remote, branch)
	refspec := fmt.Sprintf("+refs/heads/%s:%s", branch, tracking)
	_, err := gitOutput(ctx, dir, gitOpts{}, "rev-parse", "--verify", "--quiet", tracking)
	baseIsHere := err == nil

	// Growing rungs: a branch cut a few commits ago lands on the first, while a long-lived
	// branch still converges in a handful of round trips instead of one full download.
	for _, depth := range []int{32, 128, 512, 2048} {
		baseFlag := fmt.Sprintf("--deepen=%d", depth)
		if !baseIsHere {
			baseFlag = fmt.Sprintf("--depth=%d", depth)
		}
		// A failed fetch ends the recovery rather than advancing to the next rung: no
		// network, no such branch, and a concurrent run holding .git/shallow.lock are all
		// conditions a larger depth cannot fix, and retrying each of them four times only
		// stalls the run behind timeouts.
		if err := gitFetchQuiet(ctx, dir, baseFlag, remote, refspec); err != nil {
			slog.DebugContext(ctx, "cannot deepen: fetching the base ref failed",
				slog.String("base", base), slog.String("error", err.Error()))
			return ""
		}
		baseIsHere = true
		if err := gitFetchQuiet(ctx, dir, fmt.Sprintf("--deepen=%d", depth), remote); err != nil {
			slog.DebugContext(ctx, "cannot deepen: extending HEAD's own history failed",
				slog.String("base", base), slog.String("error", err.Error()))
			return ""
		}
		if mergeBase, err := gitOutput(ctx, dir, gitOpts{}, "merge-base", base, "HEAD"); err == nil {
			// Info, not Debug: this quietly spent several network round trips, and the depth
			// it settled on tells a reader whether their checkout depth is set too low.
			slog.InfoContext(ctx, "deepened shallow clone to reach the merge base",
				slog.String("base", base), slog.Int("depth", depth), slog.String("merge_base", mergeBase))
			return mergeBase
		}
		// Once the whole history is here, no larger depth can add a commit, so a still
		// missing merge base means the two really are unrelated. Stop rather than spend
		// three more round trips proving it.
		if shallow, err := gitOutput(ctx, dir, gitOpts{}, "rev-parse", "--is-shallow-repository"); err == nil && shallow != "true" {
			break
		}
	}
	slog.DebugContext(ctx, "cannot deepen: no merge base within the deepest fetch", slog.String("base", base))
	return ""
}

// gitFetchQuiet runs one of recoverMergeBase's fetches. --no-recurse-submodules because
// fetch.recurseSubmodules defaults to on-demand and the recovery wants commits in THIS
// repository, never a submodule's contents. Isolated, since a fetch magus makes unasked
// must not run the user's reference-transaction hook.
func gitFetchQuiet(ctx context.Context, dir, depthFlag, remote string, refspec ...string) error {
	args := append([]string{"fetch", "--quiet", depthFlag, "--no-tags", "--no-recurse-submodules", remote}, refspec...)
	_, err := gitOutput(ctx, dir, gitOpts{Isolated: true}, args...)
	return err
}

func (v gitVCS) DiffCommands(ctx context.Context, dir, base string) (types.DiffCommandHints, error) {
	sha, err := gitOutput(ctx, dir, gitOpts{}, "rev-parse", "HEAD")
	if err != nil {
		return types.DiffCommandHints{}, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return types.DiffCommandHints{
		CLI: fmt.Sprintf("git diff %s...%s", base, sha),
		GUI: fmt.Sprintf("git difftool %s...%s", base, sha),
	}, nil
}

func (v gitVCS) Bisect(ctx context.Context, dir string, opts types.BisectOptions) (types.Culprit, error) {
	if err := checkRev(opts.Good, opts.Bad); err != nil {
		return types.Culprit{}, err
	}
	if opts.Good == "" {
		sha, err := v.commitBeforeTime(ctx, dir, opts.GoodBefore)
		if err != nil {
			return types.Culprit{}, err
		}
		opts.Good = sha
	}
	ok, err := v.IsAncestor(ctx, dir, opts.Good, "HEAD")
	if err != nil {
		return types.Culprit{}, fmt.Errorf("good commit %q: %w", opts.Good, err)
	}
	if !ok {
		return types.Culprit{}, fmt.Errorf("good commit %q is not an ancestor of HEAD", opts.Good)
	}
	bad := opts.Bad
	if bad == "" {
		bad = "HEAD"
	}

	if err := bisectStep(gitExec(ctx, dir, gitOpts{}, "bisect", "start", bad, opts.Good)); err != nil {
		return types.Culprit{}, fmt.Errorf("git bisect start: %w", err)
	}
	defer func() { _ = bisectStep(gitExec(context.WithoutCancel(ctx), dir, gitOpts{}, "bisect", "reset")) }()

	if err := bisectStep(gitUserCommand(ctx, dir, "bisect", "run", "sh", "-c", opts.TestCmd)); err != nil {
		slog.WarnContext(ctx, "git bisect run exited with error", slog.String("err", err.Error()))
	}

	sha, err := v.culprit(ctx, dir)
	if err != nil {
		return types.Culprit{}, err
	}
	info, _ := v.commitInfo(ctx, dir, sha)
	return types.Culprit{ID: sha, Info: info}, nil
}

func (v gitVCS) commitBeforeTime(ctx context.Context, dir string, t time.Time) (string, error) {
	sha, err := gitOutput(ctx, dir, gitOpts{}, "log",
		"--before="+t.UTC().Format(time.RFC3339),
		"-n", "1", "--format=%H")
	if err != nil {
		return "", fmt.Errorf("git log: %w", err)
	}
	if sha == "" {
		return "", errors.New("no commit found before the last passing run")
	}
	return sha, nil
}

func (v gitVCS) commitInfo(ctx context.Context, dir, sha string) (string, error) {
	return gitOutput(ctx, dir, gitOpts{}, "log", "-1", "--format=%s  (%an, %ad)", "--date=short", sha)
}

// bisectStep runs one bisect command with its progress on stderr, where the reader running
// `magus bisect` watches it.
func bisectStep(cmd *exec.Cmd) error {
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (v gitVCS) culprit(ctx context.Context, dir string) (string, error) {
	out, err := gitOutput(ctx, dir, gitOpts{}, "bisect", "log")
	if err != nil {
		return "", fmt.Errorf("git bisect log: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		s := strings.TrimSpace(line)
		// Newer git quotes the term: "# first 'bad' commit: [<id>] <subject>".
		for _, marker := range []string{"# first bad commit: [", "# first 'bad' commit: ["} {
			if after, ok := strings.CutPrefix(s, marker); ok {
				if sha, _, _ := strings.Cut(after, "]"); sha != "" {
					return sha, nil
				}
			}
		}
	}
	return "", errors.New("could not parse culprit from git bisect log")
}

func (v gitVCS) Metadata(ctx context.Context, dir string) (types.VCSMeta, error) {
	// Concurrent: every run reads this before its first step, and five serial spawns
	// were about 90ms of a cached replay.
	queries := [][]string{
		{"rev-parse", "--short", "HEAD"},
		{"rev-parse", "HEAD"},
		{"rev-parse", "--abbrev-ref", "HEAD"},
		{"log", "-1", "--format=%ci"},
		// --untracked-files=normal against status.showUntrackedFiles=no, which would
		// report a tree holding only new files as clean.
		{"status", "--porcelain", "--untracked-files=normal"},
	}
	outs := make([]string, len(queries))
	errs := make([]error, len(queries))
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Go(func() { outs[i], errs[i] = gitOutput(ctx, dir, gitOpts{}, q...) })
	}
	wg.Wait()
	if errs[0] != nil {
		return types.VCSMeta{}, errs[0]
	}
	shortHash, hash, branch, commitDate, dirtyOut := outs[0], outs[1], outs[2], outs[3], outs[4]
	// Don't swallow the dirty-probe error: a failed status must not be reported as
	// a clean tree (that would stamp a dirty build as clean).
	if err := errs[4]; err != nil {
		return types.VCSMeta{}, fmt.Errorf("git status: %w", err)
	}
	return types.VCSMeta{
		Short:      shortHash,
		ID:         hash,
		Ref:        branch,
		CommitDate: commitDate,
		IsDirty:    dirtyOut != "",
	}, nil
}

// RevTime reads rev's commit date with `git log -1 --format=%ct`, which prints a Unix
// timestamp and so needs no layout guess the way Metadata's %ci would.
//
// A rev git cannot resolve exits non-zero, so the probe's error is discarded the way
// Metadata discards its optional reads: asking about `origin/main` in a clone that has
// never fetched it is how a fresh checkout legitimately answers, and reporting that as a
// failure would make the caller unable to tell "not here" from "the probe broke". The one
// error left is a %ct that did not parse, which means git answered something this code
// does not understand.
func (v gitVCS) RevTime(ctx context.Context, dir, rev string) (time.Time, bool, error) {
	if err := checkRequiredRev(rev); err != nil {
		return time.Time{}, false, err
	}
	out, _ := gitOutput(ctx, dir, gitOpts{}, "log", "-1", "--format=%ct", rev, "--")
	if out == "" {
		return time.Time{}, false, nil
	}
	secs, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("git log %s: parse %q: %w", rev, out, err)
	}
	return time.Unix(secs, 0), true, nil
}

// Dirty reports whether the working tree (optionally scoped to paths) has
// uncommitted changes, via `git status --porcelain`. Non-empty output = dirty.
func (v gitVCS) Dirty(ctx context.Context, dir string, paths []string) (bool, error) {
	files, err := v.DirtyFiles(ctx, dir, paths)
	return len(files) > 0, err
}

// DirtyFiles implements types.VCSDriver, returning repo-relative paths.
func (v gitVCS) DirtyFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	// --untracked-files=normal: status.showUntrackedFiles would otherwise hide new files
	// (no) or list every file inside a new directory (all).
	args := []string{"status", "--porcelain", "--untracked-files=normal"}
	if _, made := madeCheckout(dir); made {
		// Reading a submodule's own changes runs git in it, under a config the checkout's
		// code may have written. =dirty still lists a submodule whose commit moved. Only
		// the flag holds: a .gitmodules `ignore` outranks the diff.ignoreSubmodules config.
		args = append(args, "--ignore-submodules=dirty")
	}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := gitOutput(ctx, dir, gitOpts{KeepLeadingSpace: true}, args...)
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	return gitStatusPaths(splitStatusLines(out)), nil
}

// gitStatusPaths turns porcelain lines into paths: three columns of prefix, then the
// rename arrow, then git's C-quoting.
//
// A rename reads "R  old -> new", and the NEW path is the one that exists on disk, so that
// is the one kept: a caller stages, hashes, or globs what is there.
//
// The unquoting is NOT made redundant by core.quotePath=false. That setting stops git
// escaping bytes outside ASCII, and nothing more: a name containing a double quote or a
// backslash still comes back quoted and escaped ("we\"ird.txt") with the setting off
// (measured). Left alone it is a path that exists nowhere.
//
// Only git's own quoting form is unquoted, gated on the leading double quote, because
// strconv.Unquote also accepts Go rune and raw-string literals; without the gate a file
// literally named `x` would lose its backquotes.
func gitStatusPaths(lines []string) []string {
	out := trimStatusColumns(lines, 3)
	for i, p := range out {
		if _, after, found := strings.Cut(p, " -> "); found {
			p = after
		}
		if strings.HasPrefix(p, `"`) {
			if unquoted, err := strconv.Unquote(p); err == nil {
				p = unquoted
			}
		}
		out[i] = p
	}
	return out
}

// hasCommits reports whether the repository has at least one commit, i.e. whether HEAD
// resolves. A freshly `git init`ed repository has an UNBORN HEAD, and anything naming HEAD
// fails there rather than reporting an empty result.
//
// It returns a bool rather than an error deliberately: the callers want the question
// answered, not propagated, and phrasing it as "if err != nil { return nil }" at the call
// site is both the nilerr pattern a linter flags and the shape this package has repeatedly
// been bitten by: a probe whose failure becomes a false answer. Confining the error to one
// named predicate makes "no commits yet" a fact rather than a swallowed failure.
func (v gitVCS) hasCommits(ctx context.Context, dir string) bool {
	_, err := gitOutput(ctx, dir, gitOpts{}, "rev-parse", "--verify", "HEAD")
	return err == nil
}

// splitStatusLines splits VCS status/diff output into non-empty lines (one changed
// entry each), or nil when the tree is clean. Shared by the git/hg/sl/jj DirtyFiles.
func splitStatusLines(out string) []string {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// gitTrackedBatch caps how many pathspecs go into one `git ls-files`. A workspace can
// declare thousands of output files (docs/gen alone was ~700 when it was committed), and
// every one becomes an argv entry; batching keeps the command clear of ARG_MAX instead of
// failing on the one workspace big enough to hit it.
const gitTrackedBatch = 256

// DirtyDiff implements types.VCSDriver, diffing the working tree against HEAD.
//
// Against HEAD and not against the INDEX, which is what a bare `git diff` does. The index is
// git's alone (hg, sl and jj have none, so their DirtyDiff and DirtyFiles necessarily agree),
// and leaving it in made git the one backend where the two disagreed: `git status
// --porcelain` reports a STAGED change, while `git diff` does not. Measured: stage a drifted
// generated file and DirtyFiles names it while DirtyDiff comes back empty, so the drift gate
// prints "these outputs moved" followed by no diff at all. That is precisely the situation
// the gate exists for, since it fires in CI where nobody can look at the tree.
//
// It is also not a hypothetical ordering: `magus vcs add` stages, and a generate run that
// follows a staging step lands exactly here.
//
// -U1 keeps a multi-file diff readable in a CI log, where this is mostly read.
func (v gitVCS) DirtyDiff(ctx context.Context, dir string, paths []string) (string, error) {
	// A repository with no commits has no HEAD to diff against: `git diff HEAD` exits 128
	// there, where a bare `git diff` returns empty. Nothing is committed for the working
	// tree to differ FROM, so an empty diff is the answer.
	//
	// The check runs BEFORE the diff rather than as a rescue afterwards. Rescuing would
	// mean returning nil from an error branch, which cannot distinguish an unborn HEAD from
	// any other failure without matching git's message, and this package has been bitten
	// repeatedly by probes that report a false answer instead of an error. The cost is one
	// extra process on every call, which this path can afford: DirtyDiff runs a handful of
	// times per invocation, from the drift gate, not per file.
	if !v.hasCommits(ctx, dir) {
		return "", nil
	}
	// --no-ext-diff and --no-textconv: see RangeDiff.
	args := []string{"diff", "-U1", "--no-ext-diff", "--no-textconv", "HEAD"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := gitOutput(ctx, dir, gitOpts{KeepLeadingSpace: true}, args...)
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return out, nil
}

// TrackedFiles implements types.TrackedFileReporter. `git ls-files -- <paths>` prints the
// subset of those pathspecs that are in the index, which is exactly "tracked": an ignored
// path and an untracked-but-not-ignored path are both absent, and neither is distinguishable
// through Dirty.
func (v gitVCS) TrackedFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	var tracked []string
	for start := 0; start < len(paths); start += gitTrackedBatch {
		end := min(start+gitTrackedBatch, len(paths))
		args := append([]string{"ls-files", "--"}, paths[start:end]...)
		out, err := gitOutput(ctx, dir, gitOpts{}, args...)
		if err != nil {
			return nil, fmt.Errorf("git ls-files: %w", err)
		}
		tracked = append(tracked, splitLines([]byte(out))...)
	}
	return tracked, nil
}

// IgnoredFiles implements types.IgnoredFileReporter, returning the given paths the ignore
// rules cover, AS GIVEN.
//
// It shares IgnoredPaths' probe, and the sharing is what keeps the two from disagreeing.
// They ask nearly the same question, are named one letter apart on the same type, and return
// different shapes; separate probes gave OPPOSITE answers, because only IgnoredPaths passed
// --no-index, so for a file that is tracked AND matches an ignore rule IgnoredPaths said
// "ignored" and IgnoredFiles said "not ignored". Measured on a repo tracking keep.log under
// a *.log rule. Reaching for the wrong one of two nearly identical names is not a compile
// error, so the divergence surfaces only as a wrong answer.
//
// The rules-based answer is the one kept, because it is what both callers actually want:
// doc indexing asks "should I skip this", and conflict resolution asks whether a generated
// file that one side stopped tracking is now covered by an ignore rule. The index-aware
// answer ("git already tracks it, so the rules do not apply") answers neither. hg and sl
// share their probe between the two methods for the same reason.
func (v gitVCS) IgnoredFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	ignored, err := v.IgnoredPaths(ctx, dir, paths)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if ignored[p] {
			out = append(out, p)
		}
	}
	return out, nil
}

// Describe returns `git describe --tags --always --dirty`: the nearest tag (or a
// short hash when no tag is reachable), with a -dirty suffix for a modified tree.
func (v gitVCS) Describe(ctx context.Context, dir string) (string, error) {
	return gitOutput(ctx, dir, gitOpts{}, "describe", "--tags", "--always", "--dirty")
}

// Tags lists tags newest-first. creatordate is the sort key because it reads a
// lightweight tag's commit date and an annotated tag's own date, so the two kinds
// order together instead of the lightweight ones bunching at the repository's age.
// An ANNOTATED tag's %(objectname) is the TAG OBJECT's id, not the commit it points at,
// while a lightweight tag's is the commit. types.VCSTag.ID promises "the revision
// identifier the tag resolves to", so recording objectname made every annotated tag (the
// kind `git tag -a` and most release tooling create) report an id that matches no commit.
// A caller asking "is this release tagged at HEAD?" compared VCSTag.ID to Commit.ID and
// got no match for exactly the tags a release process creates. %(*objectname) is the
// dereferenced commit, and is EMPTY for a lightweight tag, so the %(if) picks whichever of
// the two is the commit.
//
// The name atom is %(refname:lstrip=2). %(refname:short) abbreviates only as far as the ref
// stays unambiguous, so under it a branch sharing a tag's name renders that tag as
// "tags/v0.4.0", which matches no "v*" pattern and carries "tags/" into VCSTag.Prefix. The
// v0.4.0 release ran against a repository whose own tag it could not see. lstrip=2 drops the
// two refs/tags/ components this query already restricts itself to.
func (v gitVCS) Tags(ctx context.Context, dir, pattern string) ([]types.VCSTag, error) {
	const format = "%(refname:lstrip=2)\t%(creatordate:iso-strict)\t" +
		"%(if)%(*objectname)%(then)%(*objectname)%(else)%(objectname)%(end)"
	out, err := gitOutput(ctx, dir, gitOpts{}, "for-each-ref", "--sort=-creatordate", "--format="+format, "refs/tags")
	if err != nil {
		return nil, err
	}
	return parseTags(out, pattern)
}

// RemoteURL returns a remote's fetch URL (types.RemoteReporter), "origin" when name is
// empty. `remote get-url` exits 2 for a remote that is not configured, which is
// ErrVCSUnsupported, so callers degrade to no link.
func (v gitVCS) RemoteURL(ctx context.Context, dir, name string) (string, error) {
	name = cmp.Or(name, "origin")
	if err := checkRemoteName(name); err != nil {
		return "", err
	}
	out, err := gitOutput(ctx, dir, gitOpts{}, "remote", "get-url", "--", name)
	if exitCode(err) == 2 || (err == nil && out == "") {
		return "", types.ErrVCSUnsupported
	}
	if err != nil {
		return "", fmt.Errorf("git remote get-url %s: %w", name, err)
	}
	return out, nil
}

// DefaultRef resolves the repo's default branch from origin/HEAD, independent of
// the checked-out branch (types.DefaultRefReporter), so committed-doc links stay
// stable across feature branches and worktrees. `git symbolic-ref refs/remotes/origin/HEAD`
// yields "origin/main"; we strip the "origin/" prefix. Yields ErrVCSUnsupported when
// origin/HEAD is unset (e.g. a repo cloned without it), so callers fall back.
func (v gitVCS) DefaultRef(ctx context.Context, dir string) (string, error) {
	out, err := gitOutput(ctx, dir, gitOpts{}, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil || out == "" {
		return "", types.ErrVCSUnsupported
	}
	return strings.TrimPrefix(out, "origin/"), nil
}

// CheckoutState implements types.CheckoutStateReporter with two reads of refs and none of
// the working tree: `symbolic-ref` names the branch HEAD points at and exits 1 when HEAD
// names a commit, and `for-each-ref` lists refs/remotes.
func (gitVCS) CheckoutState(ctx context.Context, dir string) (types.CheckoutState, error) {
	var state types.CheckoutState
	branch, err := gitOutput(ctx, dir, gitOpts{}, "symbolic-ref", "-q", "--short", "HEAD")
	var exit *exec.ExitError
	switch {
	case err == nil:
		state.Branch = branch
	case !errors.As(err, &exit) || exit.ExitCode() != 1:
		return types.CheckoutState{}, fmt.Errorf("git symbolic-ref: %w", err)
	}
	refs, err := gitOutput(ctx, dir, gitOpts{}, "for-each-ref", "--format=%(refname)", "refs/remotes")
	if err != nil {
		return types.CheckoutState{}, fmt.Errorf("git for-each-ref: %w", err)
	}
	for _, ref := range strings.Fields(refs) {
		state.RemoteBranches = append(state.RemoteBranches, strings.TrimPrefix(ref, "refs/remotes/"))
	}
	return state, nil
}

// CommitPushed implements types.PushStatusReporter: it asks whether id is an ancestor of
// the current branch's upstream ("@{upstream}"), the same tracking ref `git status` and a
// plain `git push` compare against. ok=false when no upstream is configured (a branch
// that has never been pushed, or one left explicitly untracked): the caller treats that
// as "assume pushed" rather than this driver claiming an answer it does not have.
func (v gitVCS) CommitPushed(ctx context.Context, dir, id string) (pushed, ok bool, err error) {
	upstream, uerr := gitOutput(ctx, dir, gitOpts{}, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if uerr != nil || upstream == "" {
		//nolint:nilerr // an unset upstream is a tree with no answer, not a failed lookup; ok=false already reports it
		return false, false, nil
	}
	// Only merge-base's exit 1 is an answer. Every other code is it declining to answer, and
	// a shallow clone is the ordinary case (git exits 128 because the history that would
	// decide is not in the object store). Reading those as "not pushed" is what offers
	// --amend on a commit the remote already carries.
	pushed, err = v.IsAncestor(ctx, dir, id, upstream)
	if err != nil {
		return false, false, err
	}
	return pushed, true, nil
}

// gitCommitFormat emits the NUL-delimited fields parseCommit expects: id, short,
// author name/email, the commit (record) date as strict ISO 8601 / RFC 3339 (%cI),
// parents, and the raw message (%B).
const gitCommitFormat = "%H%x00%h%x00%an%x00%ae%x00%cI%x00%P%x00%B"

func (v gitVCS) FindCommit(ctx context.Context, dir, rev string) (types.Commit, error) {
	if rev == "" {
		rev = "HEAD"
	}
	if err := checkRev(rev); err != nil {
		return types.Commit{}, err
	}
	// `--` keeps git from treating a path-like rev as a positional path arg.
	out, err := gitOutput(ctx, dir, gitOpts{}, "log", "-1", "--format="+gitCommitFormat, rev, "--")
	if err != nil {
		return types.Commit{}, fmt.Errorf("git log %s: %w", rev, err)
	}
	c := parseCommit(out)
	if c.ID == "" {
		return types.Commit{}, fmt.Errorf("git: no commit for %q", rev)
	}
	return c, nil
}

func (v gitVCS) History(ctx context.Context, dir string, limit int) ([]types.Commit, error) {
	if limit <= 0 {
		limit = 1
	}
	out, err := gitOutput(ctx, dir, gitOpts{}, "log", fmt.Sprintf("-%d", limit), "--format=%H")
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	return resolveEach(ctx, dir, v, splitLines([]byte(out)))
}

// gitChurnFormat opens each commit's --name-only block with a NUL sentinel followed
// by the NUL-separated hash, author, and committer date (%cI, strict ISO 8601).
const gitChurnFormat = "%x00%H%x00%an%x00%cI"

// ChangesByCommit implements types.ChurnReporter. --name-status lists each commit's
// files one per line, prefixed by what happened to it, and -M turns on rename
// detection so a moved file arrives as one R entry carrying both names instead of a
// delete and an add that nothing connects. That pairing is the whole point: without
// it a file's churn splits across every name it has ever had. Measured on this repo
// at 500 commits, -M --name-status costs the same as the --name-only form it
// replaced (0.40s either way), so the lineage is free.
//
// --no-merges keeps a merge's combined diff (often empty or sprawling) from skewing
// edit-frequency attribution. The `-- .` pathspec scopes the log to dir's subtree
// (git runs in dir), so both the commit limit and the listed files reflect only that
// subtree. One consequence worth knowing: a file moved INTO the subtree from outside
// it reads as an add, not a rename, so lineage is complete only for moves within the
// scope. since, when set, bounds the scan by commit date.
func (gitVCS) ChangesByCommit(ctx context.Context, dir string, commits int, since string) ([]types.CommitChange, error) {
	if commits <= 0 {
		commits = 1
	}
	args := []string{"log", fmt.Sprintf("-%d", commits), "--no-merges", "-M", "--name-status", "--format=" + gitChurnFormat}
	if since != "" {
		args = append(args, "--since="+since) // single token: a value can't be read as a flag
	}
	args = append(args, "--", ".")
	out, err := gitOutput(ctx, dir, gitOpts{}, args...)
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	return parseChangesByCommit(out), nil
}

// parseChangesByCommit splits ChangesByCommit's output: a line starting with NUL
// opens a new commit (the rest is hash, author, and date, NUL-separated); every
// other non-empty line is one --name-status entry attributed to the current commit.
func parseChangesByCommit(out string) []types.CommitChange {
	var changes []types.CommitChange
	cur := -1
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "\x00"); ok {
			c := types.CommitChange{}
			fields := strings.Split(rest, "\x00")
			if len(fields) > 0 {
				c.ID = fields[0]
			}
			if len(fields) > 1 {
				c.Author = fields[1]
			}
			if len(fields) > 2 {
				c.Date, _ = time.Parse(time.RFC3339, fields[2]) // zero on parse failure
			}
			changes = append(changes, c)
			cur = len(changes) - 1
			continue
		}
		if line == "" || cur < 0 {
			continue
		}
		if fc, ok := parseNameStatus(line); ok {
			changes[cur].Files = append(changes[cur].Files, fc)
		}
	}
	return changes
}

// parseNameStatus reads one --name-status line: a status letter, a TAB, then one
// path, or two, for the rename and copy forms, whose letter carries a similarity
// score (R096, C075). A line magus cannot read is skipped rather than guessed at,
// because a mis-parsed path attributes churn to a file that does not exist.
//
// A COPY is deliberately NOT lineage. Both files exist afterwards, so folding the
// copy's history onto its source would credit one file with edits made to another;
// it is recorded as a plain add, which is what it is from the new path's side.
func parseNameStatus(line string) (types.FileChange, bool) {
	fields := strings.Split(line, "\t")
	if len(fields) < 2 || fields[0] == "" {
		return types.FileChange{}, false
	}
	switch fields[0][0] {
	case 'R':
		if len(fields) < 3 {
			return types.FileChange{}, false
		}
		return types.FileChange{Path: fields[2], PrevPath: fields[1], Status: types.ChangeRenamed}, true
	case 'C':
		if len(fields) < 3 {
			return types.FileChange{}, false
		}
		return types.FileChange{Path: fields[2], Status: types.ChangeAdded}, true
	case 'A':
		return types.FileChange{Path: fields[1], Status: types.ChangeAdded}, true
	case 'D':
		return types.FileChange{Path: fields[1], Status: types.ChangeDeleted}, true
	case '?':
		// The letter a non-git driver emits for a status IT could not translate (see
		// jjChurnTemplate). git never writes it, and it has to be skipped explicitly:
		// the default below would otherwise record the driver's own uncertainty as an
		// edit to the path.
		return types.FileChange{}, false
	default:
		// M, and the rarer T (type change) / U (unmerged): the path changed in place.
		return types.FileChange{Path: fields[1], Status: types.ChangeModified}, true
	}
}

// gitDriverArgs is everything after the executable in merge.magus.driver.
const gitDriverArgs = " vcs merge-driver %O %A %B %L %P"

// gitRefreshHooks fire on a history-changing event that can stale the knowledge graph /
// symbol index: a branch switch, a merge/pull, and a rebase/amend.
var gitRefreshHooks = []string{"post-checkout", "post-merge", "post-rewrite"}

// gitDriftHooks fire around the two events that can leave generated output stale in
// committed history: the commit itself, and the push that publishes it. Neither hook may
// block (see types.DriftHookInstaller): both bodies are the same fail-open one-liner as
// gitRefreshHooks, just addressed to a different job.
var gitDriftHooks = []string{"post-commit", "pre-push"}

// InstallMergeDriver writes .gitattributes entries and registers the magus merge driver
// and the diff drivers git does not ship, all under one repository lock so a concurrent
// install cannot pair one's attributes with the other's registration. A root outside any
// git repository is an error.
func (v gitVCS) InstallMergeDriver(ctx context.Context, root string, globs types.MergeDriverGlobs) error {
	paths, ok, err := gitRepoPathsOf(ctx, root)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("vcs: install merge driver: %s is not in a git repository", root)
	}
	return withRepoLock(ctx, paths.commonDir, func() error {
		if _, err := writeManagedSection(filepath.Join(root, ".gitattributes"), generatedMarkers, gitAttrsBody(globs), configFile); err != nil {
			return err
		}
		return v.writeGitConfig(ctx, root)
	})
}

// EnsureMergeDriver re-wires the driver whenever the declared output globs have moved
// on (or nothing wired it in the first place), and reports whether it changed anything.
//
// Installing only at `magus init` leaves the protection frozen at the shape the
// workspace had that day: a project that declares an output later is never added to
// .gitattributes, and a clone that never ran init has no registration at all. Both fail
// the same silent way: a merge conflicts every generated file by hand. The globs are
// derived, so treat the section as derived too and keep it current on its own.
func (v gitVCS) EnsureMergeDriver(ctx context.Context, root string, globs types.MergeDriverGlobs) (bool, error) {
	if len(globs.Outputs) == 0 && len(globs.AutoResolve) == 0 {
		return false, nil
	}
	attrsCurrent, attrsWanted, err := v.gitAttrsState(root, globs)
	if err != nil {
		return false, err
	}
	// One read answers every registration question. Ensure runs on every workspace load and
	// its contract is to be cheap in the steady state, so it cannot spawn a subprocess each.
	// An unreadable config reads as nothing registered, and the install reports the error.
	cfg, _ := gitManagedConfig(ctx, root)
	registered := cfg[gitMergeDriverKey]
	haveDriver := registered != ""
	if attrsCurrent == attrsWanted && haveDriver && len(staleFuncnames(cfg)) == 0 &&
		driverExeExists(registered) && driverIsReachableHere(ctx, root, registered) &&
		driverIsPreferredHere(root, registered) && driverUsable(ctx, registered) {
		return false, nil
	}
	return true, v.InstallMergeDriver(ctx, root, globs)
}

// RunMergeDriver implements types.MergeDriverInstaller: git ran the driver during the
// merge, so a conflict still standing is one it did not settle.
func (gitVCS) RunMergeDriver(context.Context, string, []string) error { return nil }

// registeredDriver returns the command currently registered as the magus merge driver.
// ok is false when nothing usable is registered; the predicates below read an executable
// path out of the command and must not be asked about "".
//
// ok folds together the two states git spells identically: `config` exits non-zero with
// no output for an absent key, and zero with no output for a key set to the empty value.
func (v gitVCS) registeredDriver(ctx context.Context, root string) (cmd string, ok bool) {
	cmd, err := v.MergeDriverCommand(ctx, root)
	return cmd, err == nil && cmd != ""
}

// MergeDriverCommand implements types.MergeDriverInstaller with the effective
// merge.magus.driver, so a per-worktree registration wins over the shared one as it does
// for git itself. `config` exits 1 for an unset key.
func (v gitVCS) MergeDriverCommand(ctx context.Context, root string) (string, error) {
	cmd, err := gitOutput(ctx, root, gitOpts{}, "config", gitMergeDriverKey)
	if exitCode(err) == 1 {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("git config %s: %w", gitMergeDriverKey, err)
	}
	return cmd, nil
}

// gitMergeDriverKey is the config key holding the magus merge driver's command line.
const gitMergeDriverKey = "merge.magus.driver"

// gitManagedConfig returns the effective value of every config key magus registers: the
// merge driver and each gitFuncnames pattern, keyed as git spells them. An unset key is
// absent from the map.
//
// -z because a pattern may hold newlines. git lists every scope from system to worktree,
// so the last value printed for a key is the one `git config <key>` would return.
func gitManagedConfig(ctx context.Context, root string) (map[string]string, error) {
	keys := make([]string, 0, 1+len(gitFuncnames))
	keys = append(keys, gitMergeDriverKey)
	for _, f := range gitFuncnames {
		keys = append(keys, f.key())
	}
	pattern := "^(" + strings.ReplaceAll(strings.Join(keys, "|"), ".", `\.`) + ")$"
	out, err := gitOutput(ctx, root, gitOpts{}, "config", "-z", "--get-regexp", pattern)
	if exitCode(err) == 1 {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("git config --get-regexp %s: %w", pattern, err)
	}
	cfg := make(map[string]string, len(keys))
	for _, entry := range strings.Split(out, "\x00") {
		if key, value, _ := strings.Cut(entry, "\n"); key != "" {
			cfg[key] = value
		}
	}
	return cfg, nil
}

// driverArgsCurrent reports whether a registered command still names the subcommand this
// binary answers to. The driver moved from `magus merge-driver` to `magus vcs
// merge-driver`, and the registration lives in each clone's .git/config, which no commit
// can update. Without this an old clone invokes a spelling that no longer dispatches, git
// reads the failure as a conflict, and every generated file falls back to markers with
// nothing naming the cause. Rewriting the registration is what lets us carry no alias.
//
// The comparison is EXACT, against everything after the executable, because a substring or
// suffix test is only sound in one direction: a binary whose own verb is `merge-driver` finds
// " merge-driver " inside `vcs merge-driver %O ...`, calls the registration current, and
// never rewrites, while git invokes a spelling it cannot dispatch.
//
// The wanted string derives from gitDriverArgs, so changing the arguments cannot leave this
// matching a spelling the binary no longer answers to.
// It checks the ARGUMENTS ONLY: exactly the spelling this binary writes. driverUsable is
// what decides whether a registration that does NOT match may still be left alone.
func driverArgsCurrent(registered string) bool {
	return driverArgsMatch(registered, gitDriverArgs)
}

// driverArgsMatch takes the wanted argument string explicitly so a test can pose as a magus
// whose subcommand path differs from this one's. That direction (an OLDER binary reading a
// NEWER registration) is the one a suffix comparison gets wrong, and with gitDriverArgs
// hard-coded it could not be exercised at all: every assertion written against this binary's
// own spelling passes under both the correct rule and the broken one.
func driverArgsMatch(registered, wanted string) bool {
	_, args := splitDriver(registered)
	return args == strings.TrimSpace(wanted)
}

// driverUsable reports whether a registration can be left as it is.
//
// Two registrations differ from the spelling this binary writes, and they need opposite
// treatment. A WRAPPER (`env FOO=1 magus vcs merge-driver %O ...`, a shape splitVCSVerb's doc
// says is supported) works and must be preserved; rewriting it silently drops the wrapper
// someone added on purpose. A STALE VERB, written by a magus whose subcommand path has since
// moved, does not dispatch at all, and leaving it makes git read every generated file as
// conflicted. String comparison cannot separate them: `vcs merge-driver %O ...` ends with
// `merge-driver %O ...`, so a suffix test calls the stale one current and an exact test calls
// the wrapper stale.
//
// So ask the registration itself, and only when it is about to be overwritten. A matching
// spelling short-circuits, which is the overwhelmingly common case and keeps the steady state
// free of subprocesses; the cost lands only on the rare path that was going to rewrite
// anyway, where being right is worth one exec.
func driverUsable(ctx context.Context, registered string) bool {
	return driverServes(ctx, registered, gitDriverArgs)
}

func driverServes(ctx context.Context, registered, wanted string) bool {
	if driverArgsMatch(registered, wanted) {
		return true
	}
	return driverRegistrationAnswers(ctx, registered)
}

// driverRegistrationAnswers asks the registered command whether it still dispatches the verb
// it was registered with.
//
// Silence keeps the registration: rewriting drops a deliberate wrapper for good, while
// keeping a stale verb costs one workspace load, since the next Ensure asks again.
func driverRegistrationAnswers(ctx context.Context, registered string) bool {
	exe, args := splitDriver(registered)
	if exe == "" {
		return false
	}
	verb, _, _ := strings.Cut(args, " %")
	fields := strings.Fields(verb)
	if len(fields) == 0 {
		return false
	}
	dispatches, answered := driverProbe(ctx, exe, fields)
	return dispatches || !answered
}

// driverProbeBudget bounds a probe that never returns; it is not a latency target. The first
// execution of a newly written file pays a code-signature check serialized across the
// machine, so the probe's latency tracks how many other processes are launching new binaries:
// 3.2s worst measured locally, and past the 5s this replaced under concurrent gates.
const driverProbeBudget = 30 * time.Second

// driverProbe runs `exe <verb> -h` and reports whether it exited zero, and separately whether
// it answered at all. A command that cannot dispatch the verb exits non-zero; one killed at
// driverProbeBudget reported nothing, which is a fact about the machine and not about the
// driver.
//
// The git placeholders are dropped rather than substituted. `-h` returns before the child
// opens a workspace or touches an index, so this cannot mutate anything, and it works in a
// tree no released magus can load.
func driverProbe(ctx context.Context, exe string, verb []string) (dispatches, answered bool) {
	ctx, cancel := context.WithTimeout(ctx, driverProbeBudget)
	defer cancel()
	// nil Stdout/Stderr: usage text goes to /dev/null, not to whoever loaded the workspace.
	err := exec.CommandContext(ctx, exe, append(verb, "-h")...).Run()
	if ctx.Err() != nil {
		return false, false
	}
	return err == nil, true
}

// splitDriver splits a registered driver command into its executable and everything after
// it, unwrapping the quotes quoteDriverExe adds. Shared so driverArgsCurrent and
// driverExeExists cannot disagree about where the executable ends.
func splitDriver(registered string) (exe, args string) {
	rest := strings.TrimSpace(registered)
	if rest == "" {
		return "", ""
	}
	if strings.HasPrefix(rest, `"`) {
		if end := strings.Index(rest[1:], `"`); end >= 0 {
			return rest[1 : end+1], strings.TrimSpace(rest[end+2:])
		}
		return rest, ""
	}
	if i := strings.Index(rest, " "); i >= 0 {
		return rest[:i], strings.TrimSpace(rest[i+1:])
	}
	return rest, ""
}

// CheckMergeDriver reports whether the managed .gitattributes section and the merge driver
// registration are present, and the diff drivers with them: a missing diff driver line or
// xfuncname registration reads false, like a missing merge driver. A torn managed section in
// .gitattributes is an error.
func (v gitVCS) CheckMergeDriver(ctx context.Context, root string) (bool, error) {
	if _, ok := v.registeredDriver(ctx, root); !ok {
		return false, nil // not configured; not an error
	}
	present, err := managedSectionPresent(filepath.Join(root, ".gitattributes"), generatedMarkers)
	if err != nil || !present {
		return false, err
	}
	missing, err := MissingDiffDrivers(ctx, root)
	return len(missing) == 0, err
}

// MissingDiffDrivers names each piece of the diff driver wiring root lacks: a line of the
// managed .gitattributes section, as `.gitattributes: <line>`, or an xfuncname registration,
// as `git config <key>`. A registration holding a pattern other than magus's counts as
// missing, since Ensure overwrites it. Nil means complete, or root is not in a git
// repository. A torn managed section is an error.
func MissingDiffDrivers(ctx context.Context, root string) ([]string, error) {
	if _, ok, err := gitRepoPathsOf(ctx, root); err != nil || !ok {
		return nil, err
	}
	path := filepath.Join(root, ".gitattributes")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("vcs: read %s: %w", path, err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	spans, err := managedSpans(text, generatedMarkers)
	if err != nil {
		return nil, fmt.Errorf("vcs: %s: %w", path, err)
	}
	have := map[string]bool{}
	for _, s := range spans {
		for line := range strings.SplitSeq(text[s.start:s.end], "\n") {
			have[strings.TrimSpace(line)] = true
		}
	}
	var missing []string
	for _, d := range gitDiffDrivers {
		if !have[d.line()] {
			missing = append(missing, ".gitattributes: "+d.line())
		}
	}
	cfg, err := gitManagedConfig(ctx, root)
	if err != nil {
		return nil, err
	}
	for _, f := range staleFuncnames(cfg) {
		missing = append(missing, "git config "+f.key())
	}
	return missing, nil
}

// gitAttrsState returns .gitattributes as it is now and as the declared globs say it
// should be, so callers can compare the two without writing. It renders exactly what
// writeManagedSection would write, CRLF preservation included.
func (v gitVCS) gitAttrsState(root string, globs types.MergeDriverGlobs) (current, wanted string, err error) {
	path := filepath.Join(root, ".gitattributes")
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", "", fmt.Errorf("vcs: read %s: %w", path, err)
	}
	wanted, err = renderManagedFile(path, string(existing), generatedMarkers, gitAttrsBody(globs), configFile)
	if err != nil {
		return "", "", err
	}
	return string(existing), wanted, nil
}

// gitAttrsBody is the managed .gitattributes section's content for outputGlobs, after the
// diff driver lines.
//
// The two kinds share the section because they set different attributes: git resolves
// each attribute by the last line that sets it, so a generated `.go` file keeps
// merge=magus from its glob line and takes diff=golang from `*.go`, whichever line comes
// first.
//
// An auto-resolve glob sets merge=magus alone: the file is source, and linguist-generated
// would collapse it in review. One without a slash is anchored at the root, since git
// matches a slashless pattern at every depth and magus.yaml's globs are rooted.
func gitAttrsBody(globs types.MergeDriverGlobs) string {
	var body strings.Builder
	for _, d := range gitDiffDrivers {
		body.WriteString(d.line() + "\n")
	}
	for _, glob := range globs.Outputs {
		fmt.Fprintf(&body, "%s merge=magus linguist-generated\n", glob)
	}
	for _, glob := range globs.AutoResolve {
		if !strings.Contains(glob, "/") {
			glob = "/" + glob
		}
		fmt.Fprintf(&body, "%s merge=magus\n", glob)
	}
	return body.String()
}

// gitDiffDriver routes paths matching glob to a git diff driver. The driver's hunk-header
// ("funcname") pattern names the declaration each hunk lands in, which is how the
// footprint names what a change touched and what `git diff` prints after each @@.
type gitDiffDriver struct{ glob, driver string }

func (d gitDiffDriver) line() string { return d.glob + " diff=" + d.driver }

// gitDiffDrivers are the managed section's diff driver lines, one per glob of
// types.DiffDrivers, in its order.
var gitDiffDrivers = func() []gitDiffDriver {
	var out []gitDiffDriver
	for _, d := range types.DiffDrivers {
		for _, g := range d.Globs {
			out = append(out, gitDiffDriver{g, d.Name})
		}
	}
	return out
}()

// gitFuncname is the xfuncname pattern magus registers for a diff driver git does not ship.
// It lives in git config rather than .gitattributes, so, like the merge driver, every clone
// needs its own registration.
type gitFuncname struct{ driver, pattern string }

func (f gitFuncname) key() string { return "diff." + f.driver + ".xfuncname" }

// gitFuncnames are the patterns of the types.DiffDrivers git does not ship. The pure
// declaration placement (types.Hunks) reads the same patterns, so a region named without
// git is the region git names.
var gitFuncnames = func() []gitFuncname {
	var out []gitFuncname
	for _, d := range types.DiffDrivers {
		if !d.Builtin {
			out = append(out, gitFuncname{d.Name, d.Funcname})
		}
	}
	return out
}()

// staleFuncnames returns the gitFuncnames whose registration in cfg is missing or holds
// another pattern.
func staleFuncnames(cfg map[string]string) []gitFuncname {
	var stale []gitFuncname
	for _, f := range gitFuncnames {
		if cfg[f.key()] != f.pattern {
			stale = append(stale, f)
		}
	}
	return stale
}

// gitMergeDriverCommand is the command line git runs to resolve a conflict in a
// generated file. Registering the bare word "magus" assumes a released binary on
// PATH; when there is not one, git cannot execute the driver and silently falls
// back to conflict markers in every generated file, which is indistinguishable
// from having no driver at all. Prefer PATH so the registration survives an
// upgrade-in-place, and fall back to this binary's own absolute path so a
// source checkout with no installed magus still merges cleanly.
//
// PATH is preferred only if that binary answers the spelling being registered. Existing on
// PATH is not enough: a source checkout finds an INSTALLED RELEASE there, and pairing that
// path with this binary's spelling registers something nothing can dispatch. One release plus
// one source tree is enough to hit it: the ordinary development setup.
//
// A magus built at the workspace root comes FIRST, ahead of PATH. Answering the spelling is
// a weaker test than it looks: driverExeAnswers probes with -h, which returns before the
// child opens a workspace, so a release too old to READ this magusfile still answers and
// still wins. That is how one v0.3.0 build became the registered driver for 142 worktrees
// of the repo that defines magus, each failing at load on every conflict. A binary in the
// tree is the one that can read the tree.
//
// It reaches only a workspace that builds an executable named `magus` at its root, which in
// practice is magus's own. Everyone else keeps the PATH registration and its
// upgrade-in-place behavior.
func gitMergeDriverCommand(ctx context.Context, root string) string {
	return quoteDriverExe(driverExe(ctx, root)) + gitDriverArgs
}

// driverExe is the magus a merge driver registration or run names: the workspace's own
// build, then PATH's, each only if it dispatches the driver, then this process.
func driverExe(ctx context.Context, root string) string {
	if exe := localDriverExe(root); exe != "" && driverExeAnswers(ctx, exe) {
		return exe
	}
	if exe, err := exec.LookPath("magus"); err == nil && driverExeAnswers(ctx, exe) {
		return exe
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "magus"
}

// localDriverExe is the workspace's own magus binary, or "" when it has none built.
func localDriverExe(root string) string {
	exe := filepath.Join(root, "magus")
	info, err := os.Stat(exe)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return ""
	}
	return exe
}

// quoteDriverExe quotes a path git would otherwise split on whitespace. splitDriver
// unwraps the same quoting.
func quoteDriverExe(exe string) string {
	if strings.ContainsAny(exe, " \t") {
		return `"` + exe + `"`
	}
	return exe
}

// driverExeAnswers reports whether exe dispatches the subcommand gitDriverArgs names, by asking
// it for help: a binary that does not know the subcommand exits non-zero.
//
// It spawns a subprocess, so it belongs on the repair path only; EnsureMergeDriver returns
// early in the steady state and reaches InstallMergeDriver when something is already wrong.
// `-h` returns before the child opens a workspace, so it also works in a tree no released
// magus can load, which is when the answer matters most.
//
// Silence reads as no here, the opposite of driverRegistrationAnswers: this caller picks what
// to write, and declining PATH falls back to os.Executable(), which dispatches by construction.
func driverExeAnswers(ctx context.Context, exe string) bool {
	verb, _, _ := strings.Cut(strings.TrimSpace(gitDriverArgs), " %")
	dispatches, _ := driverProbe(ctx, exe, strings.Fields(verb))
	return dispatches
}

// driverExeExists reports whether the command currently registered still resolves to a
// runnable binary. A registration can rot without anyone touching git config: an absolute
// path recorded from a `go run` build points into a temp dir that is gone next run, and a
// path into a since-removed install is no better. git treats a driver it cannot execute
// as a conflict, so a rotted registration behaves exactly like no driver; the failure it
// causes never mentions the driver, so nothing points at the cause.
func driverExeExists(registered string) bool {
	exe, _ := splitDriver(registered)
	if exe == "" {
		return false
	}
	if strings.ContainsRune(exe, filepath.Separator) {
		_, err := os.Stat(exe)
		return err == nil
	}
	_, err := exec.LookPath(exe)
	return err == nil
}

// writeGitConfig scopes the registration per worktree where git allows it. The value is
// usually THIS worktree's binary (gitMergeDriverCommand falls back to os.Executable()
// when PATH holds no magus that answers), so a shared write points every other worktree at
// this build, and a merge there silently resolves with the wrong tool. Reading stays
// unscoped; `git config <key>` already prefers worktree config. The xfuncname patterns
// take the same scope so every registration magus owns lives in one place.
func (v gitVCS) writeGitConfig(ctx context.Context, root string) error {
	scope := []string{"config"}
	if v.worktreeConfigEnabled(ctx, root) {
		scope = append(scope, "--worktree")
	}
	set := func(key, value string) error {
		if _, err := gitOutput(ctx, root, gitOpts{}, slices.Concat(scope, []string{key, value})...); err != nil {
			return fmt.Errorf("git config %s: %w", key, err)
		}
		return nil
	}
	if err := set(gitMergeDriverKey, gitMergeDriverCommand(ctx, root)); err != nil {
		return err
	}
	for _, f := range gitFuncnames {
		if err := set(f.key(), f.pattern); err != nil {
			return err
		}
	}
	return nil
}

// worktreeConfigEnabled reports whether per-worktree config is available: without the
// extension, `git config --worktree` is an error.
func (v gitVCS) worktreeConfigEnabled(ctx context.Context, root string) bool {
	out, err := gitOutput(ctx, root, gitOpts{}, "config", "--get", "extensions.worktreeConfig")
	return err == nil && out == "true"
}

// driverIsReachableHere reports whether the registered executable is one this process would
// choose. Anything else is another worktree's build, and counting it as current is how the
// first worktree to register wins permanently.
//
// A path INSIDE root is reachable by definition, and that is the case this got wrong. It
// admitted only PATH's magus and this process's own binary, so a registration naming a
// binary in the worktree (which is what install-dogfood writes, and the only thing that
// can resolve conflicts with the change under test) was rejected as foreign and rewritten
// on the next workspace load. Measured 2026-09-05: the registration survived exactly one
// magus command. The distinction the original rule wanted is another worktree's root
// versus this one, not "in a worktree at all".
func driverIsReachableHere(ctx context.Context, root, registered string) bool {
	exe, _ := splitDriver(registered)
	if exe == "" {
		return false
	}
	if !strings.ContainsRune(exe, filepath.Separator) {
		return true // a bare name resolves through PATH wherever it runs
	}
	if pathUnder(root, exe) {
		return true
	}
	if p, err := exec.LookPath("magus"); err == nil && p == exe && driverExeAnswers(ctx, exe) {
		return true
	}
	self, err := os.Executable()
	return err == nil && self == exe
}

// driverIsPreferredHere reports whether the registration already names a binary from this
// workspace, when the workspace has one to offer.
//
// Reachability alone leaves a wrong registration in place forever. An installed release
// answers the -h probe, so driverIsReachableHere calls it fine, the steady-state check
// returns early, and nothing ever rewrites, which is why 142 worktrees kept a v0.3.0
// driver that could not read the tree. Preferring the local build has to be a reason to
// REPLACE what is registered, not only a preference applied when something else already
// forced a rewrite.
//
// A workspace with no magus of its own has nothing better to offer, so whatever is
// registered stands and PATH keeps its upgrade-in-place behavior.
func driverIsPreferredHere(root, registered string) bool {
	if localDriverExe(root) == "" {
		return true
	}
	exe, _ := splitDriver(registered)
	return pathUnder(root, exe)
}

// pathUnder reports whether p sits inside root. Both are cleaned first, so a registration
// written with a trailing slash or a "." segment compares the same as one without.
func pathUnder(root, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// InstallRefreshHook implements types.RefreshHookInstaller: after it returns, each of
// gitRefreshHooks runs command on a history-changing event, fail-open, beside whatever
// the hook already did. It returns the hooks it changed, none when all were current.
// A root outside any git repository installs nothing and is not an error; a hook written
// for an interpreter other than sh, or holding a torn managed section, is. Installs are
// serialized per repository.
func (v gitVCS) InstallRefreshHook(ctx context.Context, root, command string) ([]string, error) {
	return installGitHookSections(ctx, root, gitRefreshHooks, refreshMarkers, func(name string) string {
		return gitHookBody(name, command)
	})
}

// gitRepoPaths are the directories of a git repository that magus writes managed
// sections into or locks, both absolute.
type gitRepoPaths struct {
	// hooksDir is where git runs hooks: core.hooksPath when set, else the common
	// directory's hooks/.
	hooksDir string
	// commonDir is the metadata directory every worktree of the repository shares.
	commonDir string
}

// gitRepoPathsOf resolves root's gitRepoPaths in one git call. ok is false when root is
// not inside a git repository; any other failure, cancellation included, is an error.
func gitRepoPathsOf(ctx context.Context, root string) (paths gitRepoPaths, ok bool, err error) {
	// The C locale keeps git's "not a git repository" wording what the test below reads.
	cmd := gitExec(ctx, root, gitOpts{Env: []string{"LC_ALL=C"}}, "rev-parse", "--git-path", "hooks", "--git-common-dir")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return gitRepoPaths{}, false, ctx.Err()
		}
		if strings.Contains(stderr.String(), "not a git repository") {
			return gitRepoPaths{}, false, nil
		}
		return gitRepoPaths{}, false, fmt.Errorf("vcs: git rev-parse in %s: %w: %s", root, err, strings.TrimSpace(stderr.String()))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return gitRepoPaths{}, false, fmt.Errorf("vcs: git rev-parse in %s: want 2 lines, got %q", root, out)
	}
	abs := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(root, p)
	}
	return gitRepoPaths{hooksDir: abs(lines[0]), commonDir: abs(lines[1])}, true, nil
}

// installGitHookSections writes the m section, with body(name) as its content, into each
// named hook of root's repository under one repository lock, and returns the hooks it
// changed. A root outside any git repository installs nothing.
func installGitHookSections(ctx context.Context, root string, names []string, m managedMarkers, body func(name string) string) ([]string, error) {
	paths, ok, err := gitRepoPathsOf(ctx, root)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	if err := os.MkdirAll(paths.hooksDir, 0o755); err != nil {
		return nil, fmt.Errorf("vcs: mkdir %s: %w", paths.hooksDir, err)
	}
	var installed []string
	err = withRepoLock(ctx, paths.commonDir, func() error {
		for _, name := range names {
			changed, err := writeManagedSection(filepath.Join(paths.hooksDir, name), m, body(name), hookFile)
			if err != nil {
				return err
			}
			if changed {
				installed = append(installed, name)
			}
		}
		return nil
	})
	return installed, err
}

// gitHookBody is the shell a hook runs. post-checkout also fires on file checkouts (git
// checkout <path>), so it guards on the branch-checkout flag ($3==1); the others fire
// only on a real history move. The command is best-effort (`|| true`) so a hook never
// fails the git operation.
func gitHookBody(name, command string) string {
	guard := ""
	if name == "post-checkout" {
		guard = "[ \"$3\" = \"1\" ] || exit 0\n"
	}
	return guard + command + " >/dev/null 2>&1 || true\n"
}

// InstallDriftHook implements types.DriftHookInstaller: after it returns, a commit and
// the push that follows it (gitDriftHooks) both run command, fail-open, so the server
// checks for stale generated output in the background. Its section coexists with the
// refresh section and any hand-written body in the same hook. It returns the hooks it
// changed, none when all were current. A root outside any git repository installs
// nothing and is not an error; a hook written for an interpreter other than sh, or
// holding a torn managed section, is. Installs are serialized per repository.
func (v gitVCS) InstallDriftHook(ctx context.Context, root, command string) ([]string, error) {
	// Neither hook needs a guard: post-commit fires only on a real commit, and pre-push
	// fires only on a real push, unlike post-checkout's dual meaning.
	return installGitHookSections(ctx, root, gitDriftHooks, driftMarkers, func(string) string {
		return command + " >/dev/null 2>&1 || true\n"
	})
}

// InstallRegenHook implements types.RegenHookInstaller: after it returns, each of
// SettleHooks runs command with its own name appended, beside the refresh and drift
// sections and any hand-written body, and post-merge no longer carries the section an
// earlier magus wrote there. It returns the hooks it changed, none when all were
// current. Same error and locking contract as InstallRefreshHook.
//
// Not fail-open, unlike the other hook sections: the command is the merge driver's
// second half, and a failure it swallowed would be a commit carrying stale output that
// nothing reports. In the two hooks git runs before it commits, a regeneration that
// failed stops the commit. What does stay open is the binary itself: the hooks dir is
// shared by every worktree of the repository, each with a magus of its own, so a
// missing one (127) or one that predates the flag (2, usage) prints a line and lets the
// commit through rather than failing every other worktree's merges.
func (v gitVCS) InstallRegenHook(ctx context.Context, root, command string) ([]string, error) {
	installed, err := installGitHookSections(ctx, root, SettleHooks, regenMarkers, func(name string) string {
		return gitSettleHookBody(name, command)
	})
	if err != nil {
		return nil, err
	}
	removed, err := removeGitHookSections(ctx, root, []string{"post-merge"}, regenMarkers)
	return append(installed, removed...), err
}

// gitSettleHookBody is the shell one settle hook runs. The pre-commit and post-commit
// guards read the operation state off the git dir so an ordinary commit, which is most
// of them, never starts magus; post-rewrite fires for an amend too, which is not a
// merge. The guards are `if` blocks rather than early exits so a section below this one
// still runs.
func gitSettleHookBody(name, command string) string {
	bin, _, _ := strings.Cut(command, " ")
	run := command + " " + name
	tolerate := "magus_rc=$?\n" +
		"if [ $magus_rc -eq 2 ] || [ $magus_rc -eq 127 ]; then\n" +
		"  echo \"magus: the settle hook did not run: " + bin + " is missing or predates 'vcs resolve --hook', so this operation is not regenerated; run '" + bin + " vcs resolve' once it is\" >&2\n"
	stop := "elif [ $magus_rc -ne 0 ]; then\n  exit $magus_rc\n"
	const end = "fi\n"
	guarded := func(test, body string) string {
		var b strings.Builder
		b.WriteString("if " + test + "; then\n")
		for _, line := range strings.SplitAfter(strings.TrimSuffix(body, "\n"), "\n") {
			b.WriteString("  " + line)
		}
		b.WriteString("\nfi\n")
		return b.String()
	}
	const stateHeads = "magus_git_dir=$(git rev-parse --git-dir) && { [ -e \"$magus_git_dir/CHERRY_PICK_HEAD\" ] || [ -e \"$magus_git_dir/REVERT_HEAD\" ]"
	switch name {
	case HookPreMergeCommit:
		return run + "\n" + tolerate + stop + end
	case HookPreCommit:
		return guarded(stateHeads+" || [ -e \"$magus_git_dir/MERGE_HEAD\" ]; }", run+"\n"+tolerate+stop+end)
	case HookPostCommit:
		return guarded(stateHeads+"; }", run+"\n"+tolerate+end)
	case HookPostRewrite:
		return guarded("[ \"$1\" = rebase ]", run+" \"$@\"\n"+tolerate+end)
	default:
		return run + "\n" + tolerate + end
	}
}

// removeGitHookSections deletes the m section from each named hook of root's repository
// and returns the hooks it changed. A hook without one is left as it is.
func removeGitHookSections(ctx context.Context, root string, names []string, m managedMarkers) ([]string, error) {
	paths, ok, err := gitRepoPathsOf(ctx, root)
	if err != nil || !ok {
		return nil, err
	}
	var removed []string
	err = withRepoLock(ctx, paths.commonDir, func() error {
		for _, name := range names {
			changed, err := removeManagedSection(filepath.Join(paths.hooksDir, name), m)
			if err != nil {
				return err
			}
			if changed {
				removed = append(removed, name)
			}
		}
		return nil
	})
	return removed, err
}

// gitArgChunkSize bounds pathspecs per git invocation. Resolving in bulk exists to avoid
// per-path process cost, but an unbounded argv hits E2BIG on the tightest platforms.
const gitArgChunkSize = 256

// gitPathChunks splits paths into argv-sized batches, preserving order.
func gitPathChunks(paths []string) [][]string {
	var chunks [][]string
	for start := 0; start < len(paths); start += gitArgChunkSize {
		end := min(start+gitArgChunkSize, len(paths))
		chunks = append(chunks, paths[start:end])
	}
	return chunks
}

// runGitBatched runs `git <args...> -- <paths>` in root in argv-sized batches. The paths
// are literal: every caller passes a file it was told about, and a name like "*.txt" must
// not match its neighbors.
func runGitBatched(ctx context.Context, root string, args []string, paths []string) error {
	for _, chunk := range gitPathChunks(paths) {
		argv := slices.Concat(args, []string{"--"}, chunk)
		if _, err := gitOutput(ctx, root, gitOpts{Literal: true}, argv...); err != nil {
			return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}
	return nil
}

// gitPathPrefix returns root's path relative to the repository top level with a trailing
// slash, or "" when root is the top level. Porcelain paths are top-level-relative, so a
// workspace rooted deeper needs this to translate them.
func gitPathPrefix(ctx context.Context, root string) string {
	out, err := gitOutput(ctx, root, gitOpts{}, "rev-parse", "--show-prefix")
	if err != nil {
		return ""
	}
	return out
}

// Conflicts implements types.ConflictResolver. It reads porcelain status rather than
// `diff --diff-filter=U` because the classification matters: a path git marks D on either
// side has no content to merge, and a caller needs that apart from a content conflict.
//
// --no-renames and -uno narrow what git emits. parseConflicts stays correct without them
// (see its rename note), so these are a second line of defense.
func (v gitVCS) Conflicts(ctx context.Context, root string) ([]types.Conflict, error) {
	out, err := gitOutput(ctx, root, gitOpts{KeepLeadingSpace: true}, "status", "--porcelain=v1", "-z", "--no-renames", "-uno")
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	return parseConflicts(out, gitPathPrefix(ctx, root)), nil
}

// parseConflicts turns `git status --porcelain=v1 -z` output into the unmerged paths.
//
// The NUL stream is walked by index, not ranged over: a rename or copy occupies TWO
// fields and the second is that entry's payload. Read as its own entry, an original path
// like "Utils/x.txt" parses as XY="Ut", passes the U test, and surfaces as a phantom
// conflict at "ls/x.txt".
//
// prefix is the workspace's path relative to the repository top level. Status paths are
// top-level-relative, so a workspace rooted deeper needs them rebased and should skip the
// ones outside itself.
func parseConflicts(out, prefix string) []types.Conflict {
	fields := strings.Split(out, "\x00")
	var conflicts []types.Conflict
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			continue
		}
		xy, path := entry[:2], entry[3:]
		// A rename/copy carries its original path in the following field.
		if strings.ContainsAny(xy, "RC") {
			i++
			continue
		}
		if !strings.ContainsRune(xy, 'U') && xy != "AA" && xy != "DD" {
			continue
		}
		if prefix != "" {
			rel, ok := strings.CutPrefix(path, prefix)
			if !ok {
				continue
			}
			path = rel
		}
		kind := types.ConflictKindContent
		switch {
		case xy == "DD":
			kind = types.ConflictKindBothDeleted
		case strings.ContainsRune(xy, 'D'):
			kind = types.ConflictKindDeleted
		}
		conflicts = append(conflicts, types.Conflict{Path: filepath.ToSlash(path), Kind: kind})
	}
	return conflicts
}

// KeepIncoming implements types.ConflictResolver, taking git's `--theirs`: during a
// rebase, the commit being replayed.
//
// The batch attempt only saves processes: `git checkout --theirs` is atomic across its
// pathspecs, so the per-path fallback cannot find a half-applied side. A path resolving
// from neither stage errors rather than being skipped; callers send both-deleted paths to
// RemoveConflicts.
// StartMerge begins a merge of ref without committing it. See types.MergeStarter.
//
// --no-ff as well as --no-commit: a fast-forwardable branch would otherwise MOVE rather
// than merge, leaving no operation in progress, no conflicts, and a caller that thinks it
// settled something. --no-ff makes the outcome one shape regardless of topology.
//
// A ref git could read as a flag is refused rather than passed through. `git merge` has no
// `--` separator for its ref argument, so rejecting it is the only guard available.
func (v gitVCS) StartMerge(ctx context.Context, root, ref string, as types.Person) error {
	if err := checkRequiredRev(ref); err != nil {
		return err
	}
	var opts gitOpts
	if as != (types.Person{}) {
		env, err := commitIdentity(types.CommitMeta{Author: as, Committer: as})
		if err != nil {
			return err
		}
		opts.Env = env
	}
	commit, err := gitOutput(ctx, root, gitOpts{}, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return fmt.Errorf("git rev-parse %s: %w", ref, err)
	}
	// An operation already underway would pass the check below with ITS MERGE_HEAD, and a
	// failed merge would read as one that began.
	if head, _ := gitMergeHead(ctx, root); head != "" {
		return fmt.Errorf("git merge %s: a merge of %s is already in progress; conclude or abort it first", ref, head)
	}
	// The ref, not the id, so the message git prepares names the branch the user merged.
	_, err = gitOutput(ctx, root, opts, "merge", "--no-commit", "--no-ff", ref)
	if err == nil {
		return nil
	}
	// A merge that CONFLICTS exits non-zero, and that is the case this exists to set up;
	// the conflicts are the payload, reported by Conflicts. Only a merge that never began
	// is an error, and git leaves MERGE_HEAD naming the merged commit exactly when one is
	// underway.
	if head, _ := gitMergeHead(ctx, root); head == commit {
		return nil
	}
	return fmt.Errorf("git merge %s: %w", ref, err)
}

// gitMergeHead is the commit an in-progress merge is merging, "" when none is. Asking git
// rather than reading .git/MERGE_HEAD finds it in a linked checkout too, where .git is a
// file.
func gitMergeHead(ctx context.Context, root string) (string, error) {
	return gitOutput(ctx, root, gitOpts{}, "rev-parse", "-q", "--verify", "MERGE_HEAD")
}

// AbortMerge abandons the in-progress merge. See types.MergeStarter.
func (v gitVCS) AbortMerge(ctx context.Context, root string) error {
	if _, err := gitOutput(ctx, root, gitOpts{}, "merge", "--abort"); err != nil {
		return fmt.Errorf("git merge --abort: %w", err)
	}
	return nil
}

func (v gitVCS) KeepIncoming(ctx context.Context, root string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if err := runGitBatched(ctx, root, []string{"checkout", "--theirs"}, paths); err == nil {
		return nil
	}
	for _, p := range paths {
		if err := runGitBatched(ctx, root, []string{"checkout", "--theirs"}, []string{p}); err == nil {
			continue
		}
		if err := runGitBatched(ctx, root, []string{"checkout", "--ours"}, []string{p}); err != nil {
			return fmt.Errorf("git checkout %q: the merge left content on neither side; resolve it by hand: %w", p, err)
		}
	}
	return nil
}

// MarkResolved implements types.ConflictResolver.
func (v gitVCS) MarkResolved(ctx context.Context, root string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	return runGitBatched(ctx, root, []string{"add"}, paths)
}

// RemoveConflicts implements types.ConflictResolver. --ignore-unmatch stops a path the
// merge already removed from failing the batch.
func (v gitVCS) RemoveConflicts(ctx context.Context, root string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	return runGitBatched(ctx, root, []string{"rm", "-q", "-f", "--ignore-unmatch"}, paths)
}

// IgnoredPaths implements types.ConflictResolver. `git check-ignore` exits 1 when nothing
// matches, which answers "none are ignored" rather than failing.
//
// --no-index makes the answer useful. By default check-ignore consults the index and
// calls a TRACKED path not-ignored, since ignore rules do not apply to what git already
// follows. Every conflicted path is tracked, so the default answers "not ignored" for all
// of them, including the generated file whose conflict is that one side stopped tracking
// it. The question is about the RULES: would this path be ignored if nothing tracked it.
func (v gitVCS) IgnoredPaths(ctx context.Context, root string, paths []string) (map[string]bool, error) {
	ignored := make(map[string]bool, len(paths))
	if len(paths) == 0 {
		return ignored, nil
	}
	for _, chunk := range gitPathChunks(paths) {
		stdin := []byte(strings.Join(chunk, "\x00") + "\x00")
		out, err := gitOutput(ctx, root, gitOpts{Stdin: stdin, KeepLeadingSpace: true}, "check-ignore", "--no-index", "-z", "--stdin")
		if err != nil && exitCode(err) != 1 {
			return nil, fmt.Errorf("git check-ignore: %w", err)
		}
		for _, p := range strings.Split(out, "\x00") {
			if p != "" {
				ignored[filepath.ToSlash(p)] = true
			}
		}
	}
	return ignored, nil
}

// gitRedirectVars are the environment variables that move git off the repository the
// caller named. They are the reason every git subprocess here needs a scrubbed environment
// rather than just -C or cmd.Dir.
//
// GIT_DIR is the dangerous one: it OVERRIDES both -C and cmd.Dir, and git exports it into
// every hook, `rebase --exec`, and `bisect run`. magus refreshes the merge-driver
// registration on workspace load, so before this scrub, running any magus command from a
// pre-commit hook wrote `merge.magus.driver` into whatever repository was being committed
// to. The others redirect the work tree, index, object store, or ref namespace the same
// way, GIT_REPLACE_REF_BASE and the shallow and graft files change which objects a name
// resolves to, GIT_ATTR_SOURCE which tree attributes come from, the pathspec variables how
// a path argument is read, and GIT_CONFIG_* can inject config into a run that asked for
// none.
var gitRedirectVars = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_NAMESPACE", "GIT_PREFIX", "GIT_CEILING_DIRECTORIES",
	"GIT_SHALLOW_FILE", "GIT_GRAFT_FILE",
	"GIT_REPLACE_REF_BASE", "GIT_NO_REPLACE_OBJECTS",
	"GIT_ATTR_SOURCE",
	"GIT_LITERAL_PATHSPECS", "GIT_GLOB_PATHSPECS", "GIT_NOGLOB_PATHSPECS", "GIT_ICASE_PATHSPECS",
	"GIT_CONFIG", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS",
}

// gitEnviron is the process environment with gitRedirectVars removed, so -C and cmd.Dir
// decide which repository a subprocess acts on.
//
// Deliberately NOT "drop everything matching GIT_*". That prefix is not one category, it is
// two, and only one of them is a problem. The variables above answer WHICH repository; the
// rest answer HOW to work on it: GIT_SSH_COMMAND, GIT_ASKPASS and GIT_PROXY_COMMAND are
// how a fetch authenticates (recoverMergeBase really does fetch), GIT_TERMINAL_PROMPT=0 is
// what stops CI hanging on a credential prompt, GIT_EXEC_PATH is how a nonstandard install
// finds its own subcommands, and GIT_TRACE is someone actively debugging. Stripping those
// turns a working fetch into an auth failure, or silently discards the flag the user set to
// find out why. The blanket rule is simpler to write and strictly worse to run.
//
// The maintenance rule, so this does not become guesswork: add a variable here only if it
// changes which repository, work tree, index, object store, or ref namespace git acts on,
// which objects a name resolves to, where attributes are read from, or how a path argument
// is read. Anything governing transport, credentials, identity, or diagnostics stays
// inherited. GIT_CONFIG_GLOBAL and GIT_CONFIG_NOSYSTEM stay too: they pick a config FILE,
// and the settings a parse depends on are pinned per call by gitExec.
func gitEnviron() []string {
	env := os.Environ()
	out := env[:0:0]
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(gitRedirectVars, name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// gitOpts is what one git call adds to the hardening gitExec always applies.
type gitOpts struct {
	// Env is appended after the scrub: a commit's identity and dates, a scratch index.
	Env []string
	// Stdin feeds a --stdin command.
	Stdin []byte
	// Literal makes every path after -- a literal path, never pathspec magic: a file
	// named "*.txt" or ":x" means that file.
	Literal bool
	// Isolated marks work on state magus owns (a scratch checkout, a tree merge, a fetch
	// into a scratch ref): no hook runs, and gitIsolationPins apply. Opt-in because Bisect
	// and StartMerge run in the user's own checkout, where their hooks, rerere and signing
	// are part of what the user asked for.
	Isolated bool
	// KeepLeadingSpace keeps gitOutput's leading whitespace, where a status column starts
	// a line.
	KeepLeadingSpace bool
}

// gitParsePins are the config settings every git call overrides, whatever the box
// configures, because each changes bytes magus parses:
//
//	color.ui, color.diff, color.status  ANSI in a parse; see vcsExec
//	core.quotePath      a C-quoted non-ASCII path, which matches no source glob, so
//	                    `magus affected` silently never rebuilds its project
//	log.showSignature   extra lines in any log parse
//	diff.noprefix       a diff header without the a/ and b/ its readers split on
//	diff.mnemonicPrefix i/ and w/ in place of a/ and b/
var gitParsePins = []string{
	"-c", "color.ui=false",
	"-c", "color.diff=false",
	"-c", "color.status=false",
	"-c", "core.quotePath=false",
	"-c", "log.showSignature=false",
	"-c", "diff.noprefix=false",
	"-c", "diff.mnemonicPrefix=false",
}

// gitIsolationPins turn off the box's behaviour on isolated work: a recorded rerere
// resolution settling a merge magus reads, and a commit or push waiting on a signing key.
var gitIsolationPins = []string{
	"-c", "rerere.enabled=false",
	"-c", "commit.gpgSign=false",
	"-c", "push.gpgSign=false",
	"-c", "core.hooksPath=" + os.DevNull,
}

// gitCheckoutPins turn off, on every call in a checkout CreateCheckout made, the config
// that makes status, add or commit run a program (git-config(1)): core.fsmonitor names a
// hook git runs to list changed files, and submodule.recurse makes a merge or checkout
// run git inside each submodule, whose own config the checkout's code wrote. The -c
// values reach every git process the call starts through GIT_CONFIG_PARAMETERS. Filter,
// diff and merge drivers need no pin: git reads their commands only from config, and the
// pinned GIT_DIR and GIT_COMMON_DIR keep that config the repository's own.
var gitCheckoutPins = []string{
	"-c", "core.fsmonitor=false",
	"-c", "submodule.recurse=false",
}

// gitWaitDelay bounds how long Wait blocks on a child of git (ssh, a credential helper)
// still holding its pipes after git exits or ctx ends. Network deadlines are the caller's.
const gitWaitDelay = 10 * time.Second

// gitExec builds a git subprocess in dir that cannot be redirected off that repository,
// cannot prompt for credentials, cannot hang on a child holding its pipes, and reads no
// replacement objects. Every git invocation in this package goes through it. An empty dir
// is the process working directory.
//
// GIT_TERMINAL_PROMPT=0 because git and ssh read a credential prompt from /dev/tty, not
// from stdin, so an auth-required remote would hang a build forever on a prompt nobody
// sees. GIT_NO_REPLACE_OBJECTS=1 because a refs/replace/ ref would otherwise substitute
// one commit or tree for another under every read and merge.
//
// In a checkout CreateCheckout made, the call is isolated whatever o says, carries
// gitCheckoutPins, and reads the repository through the paths recorded at creation. When
// a file naming them has changed, running the command returns *types.CheckoutTamperedError
// and starts no git.
func gitExec(ctx context.Context, dir string, o gitOpts, args ...string) *exec.Cmd {
	co, made := madeCheckout(dir)
	argv := slices.Clone(gitParsePins)
	if o.Isolated || made {
		argv = append(argv, gitIsolationPins...)
	}
	if made {
		argv = append(argv, gitCheckoutPins...)
	}
	cmd := exec.CommandContext(ctx, "git", append(argv, args...)...)
	cmd.Dir = dir
	cmd.Env = append(gitEnviron(), "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1")
	if o.Literal {
		cmd.Env = append(cmd.Env, "GIT_LITERAL_PATHSPECS=1")
	}
	cmd.Env = append(cmd.Env, o.Env...)
	if made {
		cmd.Env = append(cmd.Env, "GIT_DIR="+co.gitDir, "GIT_COMMON_DIR="+co.commonDir, "GIT_WORK_TREE="+co.dir)
		if err := co.check(); err != nil {
			cmd.Err = err
		}
	}
	if o.Stdin != nil {
		cmd.Stdin = bytes.NewReader(o.Stdin)
	}
	cmd.WaitDelay = gitWaitDelay
	return cmd
}

// gitUserCommand builds a git subprocess that runs the user's own code (`bisect run`):
// scrubbed of the redirect variables like every other, but carrying none of magus's pins,
// which git would export to that code through GIT_CONFIG_PARAMETERS and the environment.
func gitUserCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnviron()
	cmd.WaitDelay = gitWaitDelay
	return cmd
}

// gitOutput runs gitExec and returns stdout without trailing newlines, and without leading
// whitespace unless o.KeepLeadingSpace. A failure carries what git said on stderr.
func gitOutput(ctx context.Context, dir string, o gitOpts, args ...string) (string, error) {
	out, err := gitExec(ctx, dir, o, args...).Output()
	if err != nil {
		return "", gitStderr(err)
	}
	if o.KeepLeadingSpace {
		return strings.TrimRight(string(out), "\n"), nil
	}
	return strings.TrimSpace(string(out)), nil
}

// vcsExec builds an hg, Sapling or jj subprocess with the switch that stops it emitting
// ANSI, so magus never parses output it has to strip first. git has gitExec. The caller
// still sets cmd.Dir or passes the backend's -R/-C flag.
//
// Not defensive: a user with `color.ui = always` in their gitconfig (a common setting, since
// it is how you keep color when piping to a pager) made `magus diff` list every UNTRACKED
// file and silently drop every tracked one, at exit 0. The escape sequence lands in front of
// the `diff --git` header, so the header stops beginning a line and no reader sees it. magus
// synthesizes the untracked half itself, uncolored, which is why output still appeared and
// the failure looked like a clean answer.
//
// hg, Sapling and jj all take a global `--color=never`. git is the exception: it has no such
// top-level flag (only a per-subcommand `--color`, which not every subcommand accepts), so
// gitParsePins carry the config override, which covers diff, log and status alike.
//
// Environment variables are NOT an option here, measured against all four with color forced
// on in repository config: NO_COLOR, HGPLAIN and TERM=dumb each failed to suppress it. An
// explicit config value outranks every one of them, and an explicit flag is what outranks the
// config. That is the whole reason this prepends a flag rather than scrubbing an env.
//
// The switch goes FIRST because it is a global option that must precede the subcommand.
func vcsExec(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, append([]string{"--color=never"}, args...)...)
}

// vcsOutput runs an hg, Sapling or jj subcommand in dir and returns its trimmed stdout.
// An empty dir uses the process working directory (the exec.Cmd.Dir convention).
func vcsOutput(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := vcsExec(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// vcsOutputRaw is vcsOutput without the leading-whitespace trim, for output whose
// COLUMNS carry meaning: a status column can be a space, and TrimSpace ate it on the first
// line only, so exactly one path per status came back missing its first character.
func vcsOutputRaw(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := vcsExec(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// revFileOutput runs a backend's "show this file at this revision" command and returns
// stdout EXACTLY: no trimming of any kind, unlike vcsOutput and vcsOutputRaw.
//
// This is file CONTENT, not a status line. A trailing newline is part of the file, and a
// helper that ate it would hand back something that is not what the revision holds, which
// is the one promise ReadFileAt makes.
func revFileOutput(cmd *exec.Cmd, what string) (string, error) {
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w: %s", what, err, strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}

func splitLines(out []byte) []string {
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// ReadFileAt implements types.RevisionFileReader via `git show <rev>:<path>`.
//
// The path is passed with forward slashes and rooted at the repository, which is how git
// spells a revision:path pair on every platform; it is an object lookup, not a filesystem
// one, so filepath.FromSlash here would break it on Windows rather than fix it.
func (gitVCS) ReadFileAt(ctx context.Context, root, rev, path string) (string, error) {
	if rev == "" {
		rev = "HEAD"
	}
	if err := checkRev(rev); err != nil {
		return "", err
	}
	return revFileOutput(gitExec(ctx, root, gitOpts{}, "show", rev+":"+path),
		fmt.Sprintf("git show %s:%s", rev, path))
}

// ExportRevision implements types.RevisionExporter. `git -C dir archive <rev> -- .`
// limits the archive to dir's own subtree and emits paths relative to it, so dstDir
// mirrors the workspace as of rev whether dir is the git top-level or a nested subdir.
// It streams through the stdlib tar reader (no `tar` binary needed) and rejects any
// entry whose path would escape dstDir.
func (gitVCS) ExportRevision(ctx context.Context, dir, rev, dstDir string) error {
	if rev == "" {
		rev = "HEAD"
	}
	if err := checkRev(rev); err != nil {
		return err
	}
	cmd := gitExec(ctx, dir, gitOpts{}, "archive", "--format=tar", rev, "--", ".")
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("git archive pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start git archive: %w", err)
	}

	extractErr := extractTar(stdout, dstDir)
	// Drain any unread archive bytes unconditionally (even after an extract error) so git
	// never blocks writing to a full pipe; only then is it safe to Wait (Wait closes the
	// pipe). This ordering is load-bearing.
	_, _ = io.Copy(io.Discard, stdout)
	if waitErr := cmd.Wait(); waitErr != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return fmt.Errorf("git archive %q: %s", rev, msg)
	}
	if extractErr != nil {
		return fmt.Errorf("extract revision %q: %w", rev, extractErr)
	}
	return nil
}

// extractTar writes a tar stream into dstDir, creating parent directories as needed and
// refusing any entry whose path would escape dstDir (git archive never emits such an
// entry; the guard is defense in depth against a crafted path). Symlinks, hardlinks, and
// other special entries are skipped: the extraction feeds a read-only consumer, so an
// escaping or dangling link has no value there.
func extractTar(r io.Reader, dstDir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(hdr.Name) {
			return fmt.Errorf("archive entry %q escapes the destination", hdr.Name)
		}
		target := filepath.Join(dstDir, filepath.FromSlash(hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeTarEntry(tr, target, hdr); err != nil {
				return err
			}
		}
	}
}

// writeTarEntry writes one regular-file entry to target.
func writeTarEntry(tr *tar.Reader, target string, hdr *tar.Header) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, tr); err != nil {
		f.Close()
		return fmt.Errorf("write %q: %w", hdr.Name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %q: %w", hdr.Name, err)
	}
	return nil
}

// Preserve builds a commit object holding the working copy's uncommitted state and
// anchors it under refs/magus/preserved/, so nothing can garbage-collect it.
//
// Not `git stash create`: measured 2026-09-08, it captures TRACKED changes only, and
// untracked files are the work that is in no commit and so the work a whole-tree revert
// destroys irrecoverably. It wraps this same plumbing anyway.
//
// A TEMPORARY index keeps the invariant. GIT_INDEX_FILE points add at a scratch file, so
// the developer's real index, which may hold a half-staged commit, is never read or
// written, and neither is the working tree.
func (v gitVCS) Preserve(ctx context.Context, dir string) (string, error) {
	dirty, err := v.Dirty(ctx, dir, nil)
	if err != nil {
		return "", fmt.Errorf("git preserve: %w", err)
	}
	if !dirty {
		return "", nil
	}
	// A private DIRECTORY rather than a temp file: git writes the index itself, so handing
	// it a path means creating a name and stepping back off it, and on a shared tmpdir
	// anyone can take that name in between. Nobody else can write inside a 0700 directory
	// magus just minted. RemoveAll takes the whole thing, git's file included.
	idxDir, err := os.MkdirTemp("", "magus-preserve-index-")
	if err != nil {
		return "", fmt.Errorf("git preserve: temp index: %w", err)
	}
	defer os.RemoveAll(idxDir)
	idxPath := filepath.Join(idxDir, "index")

	env := append([]string{"GIT_INDEX_FILE=" + idxPath}, preserveIdentity...)
	run := func(args ...string) (string, error) {
		// Isolated: a capture never signs or runs a hook; it is magus's object, not a commit
		// the user made.
		return gitOutput(ctx, dir, gitOpts{Env: env, Isolated: true}, args...)
	}
	// Seed from HEAD where there is one, so the snapshot reads as a change against the
	// checked-out commit rather than an initial import. An unborn HEAD has nothing to read
	// and starts from an empty index instead.
	born := v.hasCommits(ctx, dir)
	if born {
		if _, err := run("read-tree", "HEAD"); err != nil {
			return "", fmt.Errorf("git preserve: read-tree: %w", err)
		}
	}
	// -A rather than an explicit path list: DirtyFiles keeps only the NEW side of a rename,
	// so an index seeded from HEAD would keep the OLD path too and the tree would hold
	// both, a tree that was never the working copy. -A still honours .gitignore (measured),
	// so nothing ignored rides along.
	if _, err := run("add", "-A"); err != nil {
		return "", fmt.Errorf("git preserve: add: %w", err)
	}
	tree, err := run("write-tree")
	if err != nil {
		return "", fmt.Errorf("git preserve: write-tree: %w", err)
	}
	commitArgs := []string{"commit-tree", tree, "-m", preserveMessage}
	if born {
		commitArgs = append(commitArgs, "-p", "HEAD")
	}
	sha, err := run(commitArgs...)
	if err != nil {
		return "", fmt.Errorf("git preserve: commit-tree: %w", err)
	}
	// Anchored because gc reaps a commit no ref points at, which would make the handle
	// resolve today and not next week. refs/magus/ is off every branch and not pushed
	// without an explicit refspec, but it is NOT invisible: `git log --all`,
	// `rev-list --all` and `clone --mirror` all see it, and each ref pins the ancestry it
	// was taken on. PrunePreserved below keeps that bounded.
	if _, err := run("update-ref", preservedRefPrefix+sha, sha); err != nil {
		return "", fmt.Errorf("git preserve: update-ref: %w", err)
	}
	// Pruned AFTER the new ref exists, so a failure here costs the old refs their cleanup
	// and never this capture its anchor. The error is dropped because reporting a
	// housekeeping failure as a preserve failure sends a caller looking for stored work.
	_, _ = v.PrunePreserved(ctx, dir, time.Now().Add(-preserveRetention))
	return sha, nil
}

// preservedRefPrefix is the namespace Preserve anchors under. Under refs/magus/ rather
// than refs/heads/ or refs/tags/ so it shows up in neither `git branch` nor `git tag`,
// and is not pushed without an explicit refspec.
const preservedRefPrefix = "refs/magus/preserved/"

// preserveIdentity is who every capture is minted as, in place of whoever configured the
// box.
//
// commit-tree refuses a name or email it had to guess, so a checkout with no configured
// identity could not capture at all: a CI runner, a container, a fresh clone, which is
// where losing uncommitted work costs the most. Whose work a capture holds is the tree,
// not the header, and the ref never leaves the repository. The .invalid TLD is reserved
// (RFC 2606), so the address cannot reach anyone.
var preserveIdentity = []string{
	"GIT_AUTHOR_NAME=magus", "GIT_AUTHOR_EMAIL=magus@magus.invalid",
	"GIT_COMMITTER_NAME=magus", "GIT_COMMITTER_EMAIL=magus@magus.invalid",
}

// gitStderr appends what git actually said to an exit-status error. cmd.Output captures
// stderr into ExitError and nothing reads it, so a failure surfaces as a bare `exit status
// 128` that names neither the command's complaint nor a way to act on it.
func gitStderr(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
	}
	return err
}

// PrunePreserved deletes the refs Preserve anchored whose commit predates before.
//
// Keyed on the ref's COMMITTER date, which is when the capture was taken. The author date
// is not: commit-tree copies it from the environment, where a caller can set it.
//
// The commit's SUBJECT has to be Preserve's too. Anyone can write into
// refs/magus/preserved, and this runs unasked inside every Preserve, so the prefix alone
// is not grounds to delete a commit magus did not make.
func (v gitVCS) PrunePreserved(ctx context.Context, dir string, before time.Time) ([]string, error) {
	run := func(args ...string) (string, error) {
		return gitOutput(ctx, dir, gitOpts{Isolated: true}, args...)
	}
	// The subject is LAST in the format because it is the only field that can hold a
	// space, which is what makes the split unambiguous.
	listed, err := run("for-each-ref", "--sort=committerdate",
		"--format=%(refname) %(objectname) %(committerdate:unix) %(contents:subject)", preservedRefPrefix)
	if err != nil {
		return nil, fmt.Errorf("git prune-preserved: for-each-ref: %w", err)
	}
	var dropped []string
	for _, line := range strings.Split(listed, "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), " ", 4)
		if len(fields) < 4 {
			continue
		}
		name, sha, unix, subject := fields[0], fields[1], fields[2], fields[3]
		secs, convErr := strconv.ParseInt(unix, 10, 64)
		if convErr != nil {
			continue
		}
		// Sorted ascending, so the first ref at or after the cutoff ends the scan.
		if !time.Unix(secs, 0).Before(before) {
			break
		}
		if subject != preserveMessage {
			continue
		}
		// Deleted against the SHA that passed the checks above, so a ref another process
		// moved in between is refused rather than dropped on a stale listing.
		if _, err := run("update-ref", "-d", name, sha); err != nil {
			return dropped, fmt.Errorf("git prune-preserved: update-ref -d %s: %w", name, err)
		}
		dropped = append(dropped, strings.TrimPrefix(name, preservedRefPrefix))
	}
	return dropped, nil
}

// The capabilities that combine revisions without a working copy, or in a checkout magus
// owns: tree identity, tree merges, commits, secondary checkouts, fetch, push and bundles.
// Every call that writes is Isolated, since no hook or signing setting on the box belongs
// to that work, and every argument is checked before git sees it. A git command that fails
// reports as "git <subcommand>: ..."; an argument refused before git runs, as "vcs: ...".

// scratchRetention is how old a scratch ref must be before any call may sweep it: far
// longer than one fetch or bundle, so only a ref a crashed process left behind qualifies.
const scratchRetention = 24 * time.Hour

// scratchRef names a ref under refs/magus/<kind>/ that no other call, in this process or
// another, can share: the mint time, the pid, and random bytes. The time is what lets
// sweepScratchRefs judge a leftover without guessing.
func scratchRef(kind string) (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("vcs: scratch ref: %w", err)
	}
	return fmt.Sprintf("refs/magus/%s/%d-%d-%s", kind, time.Now().Unix(), os.Getpid(), hex.EncodeToString(b[:])), nil
}

// dropScratchRef deletes a scratchRef whatever became of the caller's context, so a
// cancelled call leaves no ref behind.
func dropScratchRef(ctx context.Context, dir, ref string) error {
	if _, err := gitOutput(context.WithoutCancel(ctx), dir, gitOpts{Isolated: true}, "update-ref", "-d", ref); err != nil {
		return fmt.Errorf("git update-ref -d %s: %w", ref, err)
	}
	return nil
}

// sweepScratchRefs deletes the scratch refs of kind older than scratchRetention, which only
// a process that died mid-call leaves. Each is deleted against the id it was listed at, so
// a ref another process moved in between is left alone. It is housekeeping: a failure
// costs the leftovers their cleanup and never the caller its work.
func sweepScratchRefs(ctx context.Context, dir, kind string) {
	prefix := "refs/magus/" + kind + "/"
	listed, err := gitOutput(ctx, dir, gitOpts{}, "for-each-ref", "--format=%(refname) %(objectname)", prefix)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-scratchRetention)
	for _, line := range splitLines([]byte(listed)) {
		ref, id, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		stamp, _, _ := strings.Cut(strings.TrimPrefix(ref, prefix), "-")
		secs, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil || !time.Unix(secs, 0).Before(cutoff) {
			continue
		}
		_, _ = gitOutput(ctx, dir, gitOpts{Isolated: true}, "update-ref", "-d", ref, id)
	}
}

// TreeID implements types.TreeReporter.
func (v gitVCS) TreeID(ctx context.Context, root, rev string) (string, error) {
	if err := checkRequiredRev(rev); err != nil {
		return "", err
	}
	out, err := gitOutput(ctx, root, gitOpts{}, "rev-parse", "--verify", "--end-of-options", rev+"^{tree}")
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s^{tree}: %w", rev, err)
	}
	return out, nil
}

// DiffTrees implements types.TreeReporter.
func (v gitVCS) DiffTrees(ctx context.Context, root, a, b string) ([]string, error) {
	if err := checkRequiredRev(a, b); err != nil {
		return nil, err
	}
	return gitDiffTree(ctx, root, a, b, nil)
}

// MergeTrees implements types.TreeMerger with `merge-tree --write-tree`, which exits 1 when
// the merge conflicts: an answer, read as one.
func (v gitVCS) MergeTrees(ctx context.Context, root string, m types.TreeMerge) (types.TreeMergeResult, error) {
	if err := checkRequiredRev(m.Ours, m.Theirs); err != nil {
		return types.TreeMergeResult{}, err
	}
	if err := checkRev(m.Base); err != nil {
		return types.TreeMergeResult{}, err
	}
	args := []string{"merge-tree", "--write-tree", "--no-messages", "-z"}
	if m.Base != "" {
		args = append(args, "--merge-base="+m.Base)
	}
	args = append(args, m.Ours, m.Theirs)
	var stderr bytes.Buffer
	cmd := gitExec(ctx, root, gitOpts{Isolated: true}, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	switch code := exitCode(err); {
	case err == nil, code == 1:
	case code == 129:
		return types.TreeMergeResult{}, errors.New("git merge-tree --write-tree needs git 2.40 or newer")
	default:
		return types.TreeMergeResult{}, fmt.Errorf("git merge-tree %s %s: %w: %s", m.Ours, m.Theirs, err, strings.TrimSpace(stderr.String()))
	}
	return parseMergeTree(string(out))
}

// MergeBase implements types.TreeMerger with `merge-base --all`, which exits 1 when a and
// b share no history: an answer, read as one.
func (v gitVCS) MergeBase(ctx context.Context, root, a, b string) (string, bool, error) {
	if err := checkRequiredRev(a, b); err != nil {
		return "", false, err
	}
	out, err := gitOutput(ctx, root, gitOpts{}, "merge-base", "--all", "--end-of-options", a, b)
	switch {
	case exitCode(err) == 1:
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("git merge-base %s %s: %w", a, b, err)
	}
	bases := strings.Fields(out)
	if len(bases) != 1 {
		return "", false, nil
	}
	return bases[0], true, nil
}

// parseMergeTree reads `merge-tree --write-tree -z` output: the tree id, then one
// "<mode> <object> <stage>\t<path>" record per conflicted index entry. Which stages a path
// has classifies it: both sides (2 and 3) is a content conflict, the base alone is a
// deletion on both sides, and anything else is one side deleting what the other changed.
func parseMergeTree(out string) (types.TreeMergeResult, error) {
	fields := splitNUL(out)
	if len(fields) == 0 {
		return types.TreeMergeResult{}, errors.New("git merge-tree: printed no tree")
	}
	res := types.TreeMergeResult{Tree: fields[0]}
	stages := map[string][]byte{}
	var order []string
	for _, rec := range fields[1:] {
		meta, path, ok := strings.Cut(rec, "\t")
		parts := strings.Fields(meta)
		if !ok || len(parts) != 3 || len(parts[2]) != 1 {
			return types.TreeMergeResult{}, fmt.Errorf("git merge-tree: unreadable conflict record %q", rec)
		}
		if _, seen := stages[path]; !seen {
			order = append(order, path)
		}
		stages[path] = append(stages[path], parts[2][0])
	}
	for _, path := range order {
		s := stages[path]
		kind := types.ConflictKindDeleted
		switch {
		case bytes.IndexByte(s, '2') >= 0 && bytes.IndexByte(s, '3') >= 0:
			kind = types.ConflictKindContent
		case bytes.Equal(s, []byte("1")):
			kind = types.ConflictKindBothDeleted
		}
		res.Conflicts = append(res.Conflicts, types.Conflict{Path: path, Kind: kind})
	}
	return res, nil
}

// CommitTree implements types.CommitWriter. The message goes on stdin, so no message can
// be read as an option and none is cleaned up.
func (v gitVCS) CommitTree(ctx context.Context, root string, c types.TreeCommit) (string, error) {
	if err := checkCommitID(c.Tree); err != nil {
		return "", err
	}
	args := []string{"commit-tree", c.Tree}
	for _, p := range c.Parents {
		if err := checkCommitID(p); err != nil {
			return "", err
		}
		args = append(args, "-p", p)
	}
	env, err := commitIdentity(c.CommitMeta)
	if err != nil {
		return "", err
	}
	args = append(args, "-F", "-")
	id, err := gitOutput(ctx, root, gitOpts{Isolated: true, Env: env, Stdin: []byte(c.Message)}, args...)
	if err != nil {
		return "", fmt.Errorf("git commit-tree: %w", err)
	}
	return id, nil
}

// commitIdentity is the environment that names a commit's author, committer and dates, so
// a commit never falls back to whoever configured the box or to a date the environment
// carries. Both people need a name and an email.
func commitIdentity(d types.CommitMeta) ([]string, error) {
	for _, p := range []struct {
		role string
		who  types.Person
	}{{"author", d.Author}, {"committer", d.Committer}} {
		if p.who.Name == "" || p.who.Email == "" {
			return nil, fmt.Errorf("vcs: a commit needs a %s with a name and an email", p.role)
		}
	}
	date := d.Date
	if date.IsZero() {
		date = time.Now()
	}
	stamp := fmt.Sprintf("@%d %s", date.Unix(), date.Format("-0700"))
	return []string{
		"GIT_AUTHOR_NAME=" + d.Author.Name, "GIT_AUTHOR_EMAIL=" + d.Author.Email,
		"GIT_COMMITTER_NAME=" + d.Committer.Name, "GIT_COMMITTER_EMAIL=" + d.Committer.Email,
		"GIT_AUTHOR_DATE=" + stamp, "GIT_COMMITTER_DATE=" + stamp,
	}, nil
}

// Commit implements types.CommitWriter. Paths are recorded literally, then `commit` runs
// with the message verbatim from stdin and no hook, so the box's commit.cleanup, template
// and hooks cannot change what is recorded.
func (v gitVCS) Commit(ctx context.Context, root string, c types.CheckoutCommit) (string, error) {
	env, err := commitIdentity(c.CommitMeta)
	if err != nil {
		return "", err
	}
	if err := runGitBatched(ctx, root, []string{"add", "-A"}, c.Paths); err != nil {
		return "", err
	}
	opts := gitOpts{Isolated: true, Env: env, Stdin: []byte(c.Message)}
	if _, err := gitOutput(ctx, root, opts, "commit", "--quiet", "--no-verify", "--cleanup=verbatim", "-F", "-"); err != nil {
		return "", fmt.Errorf("git commit: %w", err)
	}
	id, err := gitOutput(ctx, root, gitOpts{}, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return id, nil
}

// GeneratedPaths implements types.GeneratedPathReporter from the linguist-generated
// attribute in rev's tree. The box's global and system attribute files are switched off.
// $GIT_DIR/info/attributes cannot be, and it outranks the tree, so its presence is an
// error rather than a silently different answer.
func (v gitVCS) GeneratedPaths(ctx context.Context, root, rev string, paths []string) (map[string]bool, error) {
	marked := make(map[string]bool, len(paths))
	if len(paths) == 0 {
		return marked, nil
	}
	if err := checkRequiredRev(rev); err != nil {
		return nil, err
	}
	local, err := gitOutput(ctx, root, gitOpts{}, "rev-parse", "--git-path", "info/attributes")
	if err != nil {
		return nil, fmt.Errorf("git rev-parse --git-path: %w", err)
	}
	if !filepath.IsAbs(local) {
		local = filepath.Join(root, local)
	}
	if _, err := os.Stat(local); err == nil {
		return nil, fmt.Errorf("vcs: %s overrides the revision's attributes; remove it", local)
	}
	opts := gitOpts{Env: []string{"GIT_ATTR_NOSYSTEM=1"}, KeepLeadingSpace: true}
	for _, chunk := range gitPathChunks(paths) {
		opts.Stdin = []byte(strings.Join(chunk, "\x00") + "\x00")
		out, err := gitOutput(ctx, root, opts, "-c", "core.attributesFile="+os.DevNull,
			"check-attr", "--source="+rev, "-z", "--stdin", "linguist-generated")
		if err != nil {
			return nil, fmt.Errorf("git check-attr --source=%s: %w", rev, err)
		}
		// Records are <path> NUL <attribute> NUL <value> NUL.
		f := strings.Split(out, "\x00")
		for i := 0; i+2 < len(f); i += 3 {
			if f[i+2] == "set" || f[i+2] == "true" {
				marked[f[i]] = true
			}
		}
	}
	return marked, nil
}

// worktreeAdmin serializes adding and removing linked worktrees in this process; git's
// own locking there is not built for contention.
var worktreeAdmin sync.Mutex

// fetches serializes fetches into one repository in this process, keyed by its
// directory. Two concurrent `git fetch` runs read each other's refs mid-update: one
// negotiates with a commit another has named but not finished writing, and fails with
// "bad object" and "did not send all necessary objects".
var fetches struct {
	sync.Mutex
	byDir map[string]*sync.Mutex
}

func lockFetches(dir string) (unlock func()) {
	fetches.Lock()
	if fetches.byDir == nil {
		fetches.byDir = map[string]*sync.Mutex{}
	}
	mu, ok := fetches.byDir[dir]
	if !ok {
		mu = &sync.Mutex{}
		fetches.byDir[dir] = mu
	}
	fetches.Unlock()
	mu.Lock()
	return mu.Unlock
}

// checkoutLockReason is the lock reason CreateCheckout records on each worktree it adds.
// It marks the checkouts CheckoutProvisioner manages, and the lock keeps `git worktree prune`
// from dropping one whose directory went missing before RemoveCheckout ran.
const checkoutLockReason = "magus checkout"

// realPath resolves p's symlinks, resolving the nearest existing ancestor when p itself
// does not exist yet, so a path is compared by where it really lands.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(realPath(parent), filepath.Base(p))
}

// gitCheckout is what CreateCheckout recorded of a linked worktree it added, before
// anything ran in it: the directories git reads it through, and each file that names them
// as it stood.
type gitCheckout struct {
	dir, gitDir, commonDir string
	files                  []recordedFile
}

// recordedFile is a file's content, or its absence (exists false).
type recordedFile struct {
	path    string
	content []byte
	exists  bool
}

// gitCheckoutsMade maps each checkout CreateCheckout made in this process, by its path as
// given and resolved, to its record, until RemoveCheckout removes it.
var gitCheckoutsMade struct {
	sync.Mutex
	byDir map[string]*gitCheckout
}

func madeCheckout(dir string) (*gitCheckout, bool) {
	if dir == "" {
		return nil, false
	}
	gitCheckoutsMade.Lock()
	defer gitCheckoutsMade.Unlock()
	co, ok := gitCheckoutsMade.byDir[filepath.Clean(dir)]
	return co, ok
}

// check returns a *types.CheckoutTamperedError naming the first recorded file that
// changed. A file that is no longer a regular one has changed, and is never read: a FIFO
// would block the read.
func (c *gitCheckout) check() error {
	for _, f := range c.files {
		info, err := os.Lstat(f.path)
		switch {
		case errors.Is(err, fs.ErrNotExist) && !f.exists:
			continue
		case err == nil && f.exists && info.Mode().IsRegular():
			if got, err := os.ReadFile(f.path); err == nil && bytes.Equal(got, f.content) {
				continue
			}
		}
		return &types.CheckoutTamperedError{Checkout: c.dir, File: f.path}
	}
	return nil
}

// recordCheckout records the linked worktree git just added at dir. Its git directory
// must be an entry of the common directory's worktrees, which no code run in the checkout
// can add to. config.worktree is recorded because git reads it when the repository
// enables extensions.worktreeConfig.
func recordCheckout(ctx context.Context, root, dir string) (*gitCheckout, error) {
	common, err := gitOutput(ctx, root, gitOpts{}, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, fmt.Errorf("git rev-parse --git-common-dir: %w", err)
	}
	dotgit := filepath.Join(dir, ".git")
	content, err := os.ReadFile(dotgit)
	if err != nil {
		return nil, err
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir: ")
	if !ok {
		return nil, fmt.Errorf("vcs: %s does not name a git directory", dotgit)
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	if filepath.Dir(realPath(gitDir)) != realPath(filepath.Join(common, "worktrees")) {
		return nil, fmt.Errorf("vcs: %s names %s, which is not a worktree of %s", dotgit, gitDir, common)
	}
	co := &gitCheckout{dir: filepath.Clean(dir), gitDir: filepath.Clean(gitDir), commonDir: common}
	for _, p := range []string{dotgit, filepath.Join(gitDir, "commondir"), filepath.Join(gitDir, "gitdir"), filepath.Join(gitDir, "config.worktree")} {
		b, err := os.ReadFile(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			co.files = append(co.files, recordedFile{path: p})
		case err != nil:
			return nil, err
		default:
			co.files = append(co.files, recordedFile{path: p, content: b, exists: true})
		}
	}
	return co, nil
}

// CreateCheckout implements types.CheckoutProvisioner with a detached linked worktree,
// locked with checkoutLockReason, and records it for gitExec.
func (v gitVCS) CreateCheckout(ctx context.Context, root, dir, rev string) error {
	if err := checkRequiredRev(rev); err != nil {
		return err
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("vcs: checkout %q is not an absolute path", dir)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("vcs: checkout %s already exists", dir)
	}
	if pathUnder(realPath(root), realPath(dir)) {
		return fmt.Errorf("vcs: checkout %s lies inside %s, where discovery would index it as a copy of the workspace", dir, root)
	}
	// git's checkout after a merge (2.54 and 2.55 measured) looks blobs up in a
	// loose-object directory listing cached before the merge wrote its own, finds those
	// missing, and fetches them.
	remote, err := gitPromisorRemote(ctx, root)
	if err != nil {
		return err
	}
	if remote != "" {
		return fmt.Errorf("vcs: %s is a partial clone, which asks remote %q for any object it lacks; a merge in a checkout of it "+
			"can ask for a blob the merge just wrote, which the remote never had: check out from a full clone", root, remote)
	}
	worktreeAdmin.Lock()
	defer worktreeAdmin.Unlock()
	if _, err := gitOutput(ctx, root, gitOpts{Isolated: true}, "worktree", "add", "--quiet", "--detach",
		"--lock", "--reason", checkoutLockReason, "--", dir, rev); err != nil {
		return fmt.Errorf("git worktree add %s: %w", dir, err)
	}
	co, err := recordCheckout(ctx, root, dir)
	if err != nil {
		_, rmErr := gitOutput(ctx, root, gitOpts{Isolated: true}, "worktree", "remove", "--force", "--force", "--", dir)
		return errors.Join(fmt.Errorf("record checkout %s: %w", dir, err), rmErr)
	}
	gitCheckoutsMade.Lock()
	defer gitCheckoutsMade.Unlock()
	if gitCheckoutsMade.byDir == nil {
		gitCheckoutsMade.byDir = map[string]*gitCheckout{}
	}
	gitCheckoutsMade.byDir[co.dir] = co
	gitCheckoutsMade.byDir[realPath(co.dir)] = co
	return nil
}

// RemoveCheckout implements types.CheckoutProvisioner. A checkout made in this process
// is removed by deleting its directory and its git directory, which reads none of the
// files code run in it could have rewritten. Any other goes through `git worktree
// remove`, whose doubled --force removes a locked worktree, one with changes in it, and
// the registration of one whose directory is gone.
func (v gitVCS) RemoveCheckout(ctx context.Context, root, dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("vcs: checkout %q is not an absolute path", dir)
	}
	worktreeAdmin.Lock()
	defer worktreeAdmin.Unlock()
	if co, ok := madeCheckout(dir); ok {
		gitCheckoutsMade.Lock()
		for k, c := range gitCheckoutsMade.byDir {
			if c == co {
				delete(gitCheckoutsMade.byDir, k)
			}
		}
		gitCheckoutsMade.Unlock()
		return errors.Join(os.RemoveAll(co.dir), os.RemoveAll(co.gitDir))
	}
	owned, err := gitCheckouts(ctx, root)
	if err != nil {
		return err
	}
	registered, ok := owned[realPath(dir)]
	if !ok {
		return fmt.Errorf("vcs: %s is not a checkout CreateCheckout made", dir)
	}
	if _, err := gitOutput(ctx, root, gitOpts{Isolated: true}, "worktree", "remove", "--force", "--force", "--", registered); err != nil {
		return fmt.Errorf("git worktree remove %s: %w", dir, err)
	}
	return nil
}

// Checkouts implements types.CheckoutProvisioner.
func (v gitVCS) Checkouts(ctx context.Context, root string) ([]string, error) {
	owned, err := gitCheckouts(ctx, root)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(owned))
	for real := range owned {
		out = append(out, real)
	}
	slices.Sort(out)
	return out, nil
}

// gitCheckouts maps each worktree CreateCheckout made, by its resolved path, to the path
// git registered it under. `worktree list --porcelain -z` is records of NUL-terminated
// attribute lines, each record ending in an empty one.
func gitCheckouts(ctx context.Context, root string) (map[string]string, error) {
	out, err := gitOutput(ctx, root, gitOpts{KeepLeadingSpace: true}, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	owned := map[string]string{}
	path := ""
	for _, line := range strings.Split(out, "\x00") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = strings.TrimPrefix(line, "worktree ")
		case line == "locked "+checkoutLockReason && path != "":
			owned[realPath(path)] = path
		case line == "":
			path = ""
		}
	}
	return owned, nil
}

// checkConfiguredRemote refuses a remote name the repository has not configured. git
// reads an unconfigured name as a path or URL, so without this "evil" would fetch from
// ./evil.
func checkConfiguredRemote(ctx context.Context, dir, remote string) error {
	if err := checkRemoteName(remote); err != nil {
		return err
	}
	if _, err := gitOutput(ctx, dir, gitOpts{}, "config", "--get", "remote."+remote+".url"); err != nil {
		if exitCode(err) == 1 {
			return fmt.Errorf("vcs: %q is not a remote this repository has configured", remote)
		}
		return fmt.Errorf("git config remote.%s.url: %w", remote, err)
	}
	return nil
}

// gitPromisorRemote names the remote the repository at root fetches missing objects from
// on demand, "" when it has none. A partial clone records one in extensions.partialClone,
// remote.<name>.promisor, or both.
func gitPromisorRemote(ctx context.Context, root string) (string, error) {
	name, err := gitOutput(ctx, root, gitOpts{}, "config", "--get", "extensions.partialClone")
	switch {
	case err == nil && name != "":
		return name, nil
	case err != nil && exitCode(err) != 1:
		return "", fmt.Errorf("git config extensions.partialClone: %w", err)
	}
	out, err := gitOutput(ctx, root, gitOpts{}, "config", "--type=bool", "--get-regexp", `^remote\..*\.promisor$`)
	switch {
	case exitCode(err) == 1:
		return "", nil
	case err != nil:
		return "", fmt.Errorf("git config remote.*.promisor: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if key, on, _ := strings.Cut(line, " "); on == "true" {
			return strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".promisor"), nil
		}
	}
	return "", nil
}

// FetchRef implements types.RevisionFetcher.
func (v gitVCS) FetchRef(ctx context.Context, root, remote, ref string) (string, error) {
	if err := checkRefName(ref); err != nil {
		return "", err
	}
	if err := checkConfiguredRemote(ctx, root, remote); err != nil {
		return "", err
	}
	return fetchCommit(ctx, root, remote, ref)
}

// FetchCommit implements types.RevisionFetcher.
func (v gitVCS) FetchCommit(ctx context.Context, root, remote, id string) error {
	if err := checkCommitID(id); err != nil {
		return err
	}
	if err := checkConfiguredRemote(ctx, root, remote); err != nil {
		return err
	}
	if _, err := gitOutput(ctx, root, gitOpts{}, "cat-file", "-e", "--end-of-options", id+"^{commit}"); err == nil {
		return nil
	}
	got, err := fetchCommit(ctx, root, remote, id)
	if err != nil {
		return err
	}
	if got != id {
		return fmt.Errorf("git fetch %s %s: produced %s", remote, id, got)
	}
	return nil
}

// fetchCommit fetches src from remote into a scratch ref, returns the commit it names, and
// deletes the ref. --no-tags stops tag following, and an empty --refmap stops the
// remote-tracking update git otherwise makes when src matches remote.<name>.fetch, so no
// ref the user reads moves. The fetched commit rests on gc's prune grace, two weeks by
// default, which outlasts any caller that goes on to anchor it.
func fetchCommit(ctx context.Context, dir, remote, src string) (id string, err error) {
	unlock := lockFetches(dir)
	defer unlock()
	sweepScratchRefs(ctx, dir, "fetch")
	tmp, err := scratchRef("fetch")
	if err != nil {
		return "", err
	}
	if _, err := gitOutput(ctx, dir, gitOpts{Isolated: true}, "fetch", "--quiet", "--no-tags",
		"--no-write-fetch-head", "--no-recurse-submodules", "--no-auto-maintenance", "--refmap=",
		"--", remote, "+"+src+":"+tmp); err != nil {
		return "", fmt.Errorf("git fetch %s %s: %w", remote, src, err)
	}
	defer func() { err = errors.Join(err, dropScratchRef(ctx, dir, tmp)) }()
	id, err = gitOutput(ctx, dir, gitOpts{}, "rev-parse", "--verify", "--end-of-options", tmp+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("git fetch %s %s: %s names no commit: %w", remote, src, src, err)
	}
	return id, nil
}

// Push implements types.Pusher. Each refused ref is read from --porcelain's "!" line,
// never from stderr's prose.
func (v gitVCS) Push(ctx context.Context, root string, p types.PushLease) error {
	if err := checkRefName(p.Ref); err != nil {
		return err
	}
	if err := checkCommitID(p.To); err != nil {
		return err
	}
	if err := checkCommitID(p.Expected); err != nil {
		return err
	}
	if err := checkConfiguredRemote(ctx, root, p.Remote); err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd := gitExec(ctx, root, gitOpts{Isolated: true}, "push", "--quiet", "--porcelain", "--no-verify",
		"--no-follow-tags", "--recurse-submodules=no", "--force-with-lease="+p.Ref+":"+p.Expected,
		"--", p.Remote, p.To+":"+p.Ref)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		return nil
	}
	if refused := pushRefusal(string(out), p.Ref); refused != nil {
		return refused
	}
	return fmt.Errorf("git push %s %s: %w: %s", p.Remote, p.Ref, err, strings.TrimSpace(stderr.String()))
}

// staleLease reports whether a refusal means the ref was not where the lease expected:
// git's own check before sending ("stale info"), or the remote's under a race it lost
// ("incorrect old value provided", or a lock naming the value it expected). A lock the
// remote could not take for any other reason, or a ref it failed to update, says
// nothing about the lease.
func staleLease(reason string) bool {
	return strings.Contains(reason, "stale info") || strings.Contains(reason, "incorrect old value provided") ||
		strings.Contains(reason, "cannot lock ref") && strings.Contains(reason, "but expected")
}

// pushRefusal reads ref's "!" line in `push --porcelain` output,
// "<flag>\t<from>:<to>\t<summary> (<reason>)", as ErrStaleLease or a *PushRejectedError
// carrying the reason, or the summary when there is none. nil means no such line.
func pushRefusal(out, ref string) error {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 || fields[0] != "!" || !strings.HasSuffix(fields[1], ":"+ref) {
			continue
		}
		reason := strings.TrimSpace(fields[2])
		if open := strings.LastIndex(reason, " ("); open >= 0 && strings.HasSuffix(reason, ")") {
			reason = reason[open+2 : len(reason)-1]
		}
		if staleLease(reason) {
			return types.ErrStaleLease
		}
		return &types.PushRejectedError{Ref: ref, Reason: reason}
	}
	return nil
}

// Bundle implements types.Bundler. A bundle carries refs, not bare commits, so r.Head
// rides under a scratch ref that is gone when Bundle returns.
func (v gitVCS) Bundle(ctx context.Context, root, file string, r types.BundleRange) (err error) {
	if err := checkCommitID(r.Head); err != nil {
		return err
	}
	if r.Base != "" {
		if err := checkCommitID(r.Base); err != nil {
			return err
		}
	}
	if !filepath.IsAbs(file) {
		return fmt.Errorf("vcs: bundle %q is not an absolute path", file)
	}
	sweepScratchRefs(ctx, root, "bundle")
	tmp, err := scratchRef("bundle")
	if err != nil {
		return err
	}
	if _, err := gitOutput(ctx, root, gitOpts{Isolated: true}, "update-ref", tmp, r.Head); err != nil {
		return fmt.Errorf("git update-ref %s: %w", tmp, err)
	}
	defer func() { err = errors.Join(err, dropScratchRef(ctx, root, tmp)) }()
	args := []string{"bundle", "create", "--quiet", file, tmp}
	if r.Base != "" {
		args = append(args, "^"+r.Base)
	}
	if _, err := gitOutput(ctx, root, gitOpts{Isolated: true}, args...); err != nil {
		return fmt.Errorf("git bundle create %s: %w", file, err)
	}
	return nil
}

// Unbundle implements types.Bundler. `bundle unbundle` writes the objects and prints the
// refs the bundle carries without creating any.
func (v gitVCS) Unbundle(ctx context.Context, root, file string) error {
	if !filepath.IsAbs(file) {
		return fmt.Errorf("vcs: bundle %q is not an absolute path", file)
	}
	if _, err := gitOutput(ctx, root, gitOpts{}, "bundle", "verify", "--quiet", file); err != nil {
		return fmt.Errorf("git bundle verify %s: %w", file, err)
	}
	if _, err := gitOutput(ctx, root, gitOpts{Isolated: true}, "bundle", "unbundle", file); err != nil {
		return fmt.Errorf("git bundle unbundle %s: %w", file, err)
	}
	return nil
}
