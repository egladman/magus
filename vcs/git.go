package vcs

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/types"
)

type gitVCS struct{}

func (v gitVCS) Name() string     { return "git" }
func (v gitVCS) Claims() []string { return []string{".git"} }
func (v gitVCS) Base() string     { return "origin/main" }

// ParentRef is the first parent of the checked-out commit. `^` rather than `~1`:
// they are the same for a linear commit and differ on a merge, where `^` is the
// branch being merged INTO - which is the side a CI run wants to measure from.
func (v gitVCS) ParentRef() string { return "HEAD^" }

// IsSecondaryCheckout reports whether dir is a linked git worktree: its .git is a
// FILE whose gitdir points under another repo's .git/worktrees/. The main checkout
// has a .git DIRECTORY, and a submodule's gitdir points under .git/modules/, so
// neither is treated as a linked worktree.
func (v gitVCS) IsSecondaryCheckout(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return false // absent, or a directory (the main checkout) - either way not linked
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return false
	}
	return strings.Contains(filepath.ToSlash(strings.TrimSpace(rest)), "/.git/worktrees/")
}

func (v gitVCS) Root(ctx context.Context, dir string) (string, error) {
	cmd := gitExec(ctx, "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
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
	if err := checkRef(base); err != nil {
		return nil, err
	}
	mergeBase, err := vcsOutput(ctx, dir, "git", "merge-base", base, "HEAD")
	if err != nil {
		recovered := v.recoverMergeBase(ctx, dir, base)
		if recovered == "" {
			// Report the original failure, not the recovery's: a shallow clone that could
			// not be deepened is still, to the caller, a repository with no merge base.
			return nil, fmt.Errorf("git merge-base: %w", err)
		}
		mergeBase = recovered
	}
	// core.quotePath=false on BOTH probes, for the reason DirtyFiles sets it: git otherwise
	// renders a path outside ASCII as a C-quoted, backslash-escaped literal
	// ("uni/caf\303\251.md"). Omitting it on either probe fails silently in the worst way -
	// project.normalizeFiles only trims and slash-converts, so the quoted string matches no
	// source glob, and the project owning that file is simply never rebuilt. No diagnostic,
	// no error; `magus affected` just under-builds forever.
	out, err := vcsOutput(ctx, dir, "git", "-c", "core.quotePath=false", "diff", "--name-only", mergeBase)
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	files := splitLines([]byte(out))
	// Untracked-but-not-ignored files (new source files) are conceptually part of
	// the working tree, but git diff omits them. List them explicitly so a brand-new
	// file seeds its project the same way a modified one does. --exclude-standard
	// honors .gitignore, so build artifacts stay out.
	untracked, err := vcsOutput(ctx, dir, "git", "-c", "core.quotePath=false", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	return append(files, splitLines([]byte(untracked))...), nil
}

// BranchChanges reports what OTHER remote-tracking branches are changing, most recently updated
// first, so a reader can be told the file in front of them is also being edited elsewhere.
//
// Remote-tracking refs only, and it does NOT fetch. The answer is exactly as fresh as the reader's
// last fetch - going and getting more would be a network act nobody asked for, on a path that
// exists to annotate a diff. A caller has to say "as of your last fetch" rather than implying the
// answer is live.
//
// One fork lists the refs and one more diffs each of them, so the cost is limit+1 forks and the
// cap belongs here rather than in the caller: git applies it to the ref listing and no diff is
// ever run for a branch that was going to be discarded. The three-dot form computes the merge base
// internally, which is what keeps it to one fork per branch rather than two.
func (v gitVCS) BranchChanges(ctx context.Context, dir, base string, limit int) ([]types.BranchChange, error) {
	if limit <= 0 {
		return nil, nil
	}
	if err := checkRef(base); err != nil {
		return nil, err
	}
	// One over the cap, because the reader's OWN branch is usually among the most recent and is
	// dropped below - asking for exactly limit would quietly return one fewer than asked.
	refs, err := vcsOutput(ctx, dir, "git", "for-each-ref",
		"--sort=-committerdate", "--count", strconv.Itoa(limit+1),
		"--format=%(refname:short)", "refs/remotes/")
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}
	mine, err := vcsOutput(ctx, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git rev-parse: %w", err)
	}
	out := make([]types.BranchChange, 0, limit)
	for _, ref := range splitLines([]byte(refs)) {
		if len(out) == limit {
			break
		}
		// The remote is whatever it is called. Trimming the literal "origin/" was wrong in the
		// ordinary fork setup: an `upstream/feat/x` kept its prefix, so it never matched the
		// reader's own branch name and came back as somebody else competing on the very files
		// they were editing.
		remote, short, hasRemote := strings.Cut(ref, "/")
		if !hasRemote || remote == "" || short == "" {
			continue
		}
		// <remote>/HEAD is a symbolic pointer at the default branch, so it duplicates whatever it
		// aims at and names no line of work of its own.
		if short == "HEAD" {
			continue
		}
		// A detached HEAD reports the literal "HEAD" here and matches nothing, which is right:
		// there is no branch of the reader's own to exclude.
		if short == strings.TrimSpace(mine) {
			continue
		}
		if err := checkRef(ref); err != nil {
			// A ref git itself listed, so this is not the caller-supplied case checkRef exists
			// for - but a name that cannot be passed safely is skipped rather than trusted.
			continue
		}
		// core.quotePath=false for the reason ChangedFiles sets it: a non-ASCII path otherwise
		// arrives C-quoted, matches no source glob, and the overlap silently never reports.
		paths, err := vcsOutput(ctx, dir, "git", "-c", "core.quotePath=false",
			"diff", "--name-only", base+"..."+ref)
		if err != nil {
			// One unreachable branch is not a reason to report none: a ref can vanish between
			// the listing and the diff, and the rest of the answer is still true.
			continue
		}
		lines := splitLines([]byte(paths))
		if len(lines) == 0 {
			continue
		}
		out = append(out, types.BranchChange{Ref: short, Paths: lines})
	}
	return out, nil
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
	if shallow, err := vcsOutput(ctx, dir, "git", "rev-parse", "--is-shallow-repository"); err != nil || shallow != "true" {
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
	if _, err := vcsOutput(ctx, dir, "git", "config", "--get", "remote."+remote+".url"); err != nil {
		slog.DebugContext(ctx, "cannot deepen: base ref names no configured remote",
			slog.String("base", base), slog.String("remote", remote))
		return ""
	}
	tracking := fmt.Sprintf("refs/remotes/%s/%s", remote, branch)
	refspec := fmt.Sprintf("+refs/heads/%s:%s", branch, tracking)
	_, err := vcsOutput(ctx, dir, "git", "rev-parse", "--verify", "--quiet", tracking)
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
		if mergeBase, err := vcsOutput(ctx, dir, "git", "merge-base", base, "HEAD"); err == nil {
			// Info, not Debug: this quietly spent several network round trips, and the depth
			// it settled on tells a reader whether their checkout depth is set too low.
			slog.InfoContext(ctx, "deepened shallow clone to reach the merge base",
				slog.String("base", base), slog.Int("depth", depth), slog.String("merge_base", mergeBase))
			return mergeBase
		}
		// Once the whole history is here, no larger depth can add a commit, so a still
		// missing merge base means the two really are unrelated. Stop rather than spend
		// three more round trips proving it.
		if shallow, err := vcsOutput(ctx, dir, "git", "rev-parse", "--is-shallow-repository"); err == nil && shallow != "true" {
			break
		}
	}
	slog.DebugContext(ctx, "cannot deepen: no merge base within the deepest fetch", slog.String("base", base))
	return ""
}

// gitFetchQuiet runs one of recoverMergeBase's fetches.
//
// GIT_TERMINAL_PROMPT=0 is the load-bearing part: git and ssh read a credential prompt
// from /dev/tty directly, not from the stdin vcsOutput hands them, so an auth-required
// remote would hang a build tool forever on a prompt nobody can see - strictly worse than
// the over-build this recovery exists to prevent. --no-recurse-submodules because
// fetch.recurseSubmodules defaults to on-demand and the recovery wants commits in THIS
// repository, never a submodule's contents.
func gitFetchQuiet(ctx context.Context, dir, depthFlag, remote string, refspec ...string) error {
	args := append([]string{"fetch", "--quiet", depthFlag, "--no-tags", "--no-recurse-submodules", remote}, refspec...)
	cmd := gitExec(ctx, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	_, err := cmd.Output()
	return err
}

func (v gitVCS) DiffCommands(ctx context.Context, dir, base string) (types.DiffCommandHints, error) {
	out, err := gitExec(ctx, "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return types.DiffCommandHints{}, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	return types.DiffCommandHints{
		CLI: fmt.Sprintf("git diff %s...%s", base, sha),
		GUI: fmt.Sprintf("git difftool %s...%s", base, sha),
	}, nil
}

func (v gitVCS) Bisect(ctx context.Context, dir string, opts types.BisectOptions) (types.Culprit, error) {
	if err := checkRef(opts.Good); err != nil {
		return types.Culprit{}, err
	}
	if err := checkRef(opts.Bad); err != nil {
		return types.Culprit{}, err
	}
	if opts.Good == "" {
		sha, err := v.commitBeforeTime(ctx, dir, opts.GoodBefore)
		if err != nil {
			return types.Culprit{}, err
		}
		opts.Good = sha
	}
	if err := v.isAncestor(ctx, dir, opts.Good); err != nil {
		return types.Culprit{}, fmt.Errorf("good commit %q is not an ancestor of HEAD: %w", opts.Good, err)
	}
	bad := opts.Bad
	if bad == "" {
		bad = "HEAD"
	}

	if err := v.start(ctx, dir, bad, opts.Good); err != nil {
		return types.Culprit{}, fmt.Errorf("git bisect start: %w", err)
	}
	defer func() { _ = v.reset(context.WithoutCancel(ctx), dir) }()

	if err := v.run(ctx, dir, opts.TestCmd); err != nil {
		slog.WarnContext(ctx, "git bisect run exited with error", slog.String("err", err.Error()))
	}

	sha, err := v.culprit(ctx, dir)
	if err != nil {
		return types.Culprit{}, err
	}
	info, _ := v.commitInfo(ctx, dir, sha)
	return types.Culprit{ID: sha, Info: info}, nil
}

// isAncestor, commitBeforeTime and commitInfo run via `git -C dir` so they target
// the repository under bisect, not the process cwd: the dir-scoping the VCSDriver
// contract requires for correctness under concurrent runs.
func (v gitVCS) isAncestor(ctx context.Context, dir, sha string) error {
	return gitExec(ctx, "-C", dir, "merge-base", "--is-ancestor", sha, "HEAD").Run()
}

func (v gitVCS) commitBeforeTime(ctx context.Context, dir string, t time.Time) (string, error) {
	out, err := gitExec(ctx, "-C", dir, "log",
		"--before="+t.UTC().Format(time.RFC3339),
		"-n", "1", "--format=%H").Output()
	if err != nil {
		return "", fmt.Errorf("git log: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", errors.New("no commit found before the last passing run")
	}
	return sha, nil
}

func (v gitVCS) commitInfo(ctx context.Context, dir, sha string) (string, error) {
	out, err := gitExec(ctx, "-C", dir, "log", "-1",
		"--format=%s  (%an, %ad)", "--date=short", sha).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (v gitVCS) start(ctx context.Context, dir, bad, good string) error {
	cmd := gitExec(ctx, "bisect", "start", bad, good)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (v gitVCS) run(ctx context.Context, dir, shellCmd string) error {
	cmd := gitExec(ctx, "bisect", "run", "sh", "-c", shellCmd)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (v gitVCS) reset(ctx context.Context, dir string) error {
	cmd := gitExec(ctx, "bisect", "reset")
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (v gitVCS) culprit(ctx context.Context, dir string) (string, error) {
	out, err := gitExec(ctx, "-C", dir, "bisect", "log").Output()
	if err != nil {
		return "", fmt.Errorf("git bisect log: %w", err)
	}
	for _, line := range bytes.Split(out, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if strings.HasPrefix(s, "# first bad commit: [") {
			after := strings.TrimPrefix(s, "# first bad commit: [")
			sha := strings.SplitN(after, "]", 2)[0]
			if sha != "" {
				return sha, nil
			}
		}
	}
	return "", errors.New("could not parse culprit from git bisect log")
}

func (v gitVCS) Metadata(ctx context.Context, dir string) (types.VCSMeta, error) {
	shortHash, err := vcsOutput(ctx, dir, "git", "rev-parse", "--short", "HEAD")
	if err != nil {
		return types.VCSMeta{}, err
	}
	hash, _ := vcsOutput(ctx, dir, "git", "rev-parse", "HEAD")
	branch, _ := vcsOutput(ctx, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")
	commitDate, _ := vcsOutput(ctx, dir, "git", "log", "-1", "--format=%ci")
	// Don't swallow the dirty-probe error: a failed status must not be reported as
	// a clean tree (that would stamp a dirty build as clean).
	dirtyOut, err := vcsOutput(ctx, dir, "git", "status", "--porcelain")
	if err != nil {
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
	out, _ := vcsOutput(ctx, dir, "git", "log", "-1", "--format=%ct", rev, "--")
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
//
// core.quotePath=false: git otherwise renders a non-ASCII path as a C-quoted string
// ("\303\251.md"), which the caller would have to unescape. Off, the bytes come through
// literally and stripping the status columns is the whole parse.
func (v gitVCS) DirtyFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	args := []string{"-c", "core.quotePath=false", "status", "--porcelain"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := vcsOutputRaw(ctx, dir, "git", args...)
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	return gitStatusPaths(splitStatusLines(out)), nil
}

// gitStatusPaths turns porcelain lines into paths: three columns of prefix, then the
// rename arrow, then git's C-quoting.
//
// A rename reads "R  old -> new", and the NEW path is the one that exists on disk, so that
// is the one kept - a caller stages, hashes, or globs what is there.
//
// The unquoting is NOT made redundant by core.quotePath=false. That setting stops git
// escaping bytes outside ASCII, and nothing more: a name containing a double quote or a
// backslash still comes back quoted and escaped ("we\"ird.txt") with the setting off -
// measured. Left alone it is a path that exists nowhere.
//
// Only git's own quoting form is unquoted, gated on the leading double quote, because
// strconv.Unquote also accepts Go rune and raw-string literals - without the gate a file
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
// been bitten by - a probe whose failure becomes a false answer. Confining the error to one
// named predicate makes "no commits yet" a fact rather than a swallowed failure.
func (v gitVCS) hasCommits(ctx context.Context, dir string) bool {
	_, err := vcsOutput(ctx, dir, "git", "rev-parse", "--verify", "HEAD")
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
// git's alone - hg, sl and jj have none, so their DirtyDiff and DirtyFiles necessarily agree
// - and leaving it in made git the one backend where the two disagreed: `git status
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
	// core.quotePath=false for the same reason DirtyFiles sets it: a non-ASCII path
	// otherwise comes back C-quoted.
	// A repository with no commits has no HEAD to diff against: `git diff HEAD` exits 128
	// there, where a bare `git diff` returns empty. Nothing is committed for the working
	// tree to differ FROM, so an empty diff is the answer.
	//
	// The check runs BEFORE the diff rather than as a rescue afterwards. Rescuing would
	// mean returning nil from an error branch, which cannot distinguish an unborn HEAD from
	// any other failure without matching git's message - and this package has been bitten
	// repeatedly by probes that report a false answer instead of an error. The cost is one
	// extra process on every call, which this path can afford: DirtyDiff runs a handful of
	// times per invocation, from the drift gate, not per file.
	if !v.hasCommits(ctx, dir) {
		return "", nil
	}
	args := []string{"-c", "core.quotePath=false", "diff", "-U1", "HEAD"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := vcsOutputRaw(ctx, dir, "git", args...)
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return out, nil
}

// TrackedFiles implements types.TrackedFileReporter. `git ls-files -- <paths>` prints the
// subset of those pathspecs that are in the index, which is exactly "tracked": an ignored
// path and an untracked-but-not-ignored path are both absent, and neither is distinguishable
// through Dirty.
//
// core.quotePath=false for the same reason ChangesByCommit sets it: git otherwise renders a
// non-ASCII path double-quoted with octal escapes, and the caller compares the result against
// real paths, so a quoted name silently matches nothing.
func (v gitVCS) TrackedFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	var tracked []string
	for start := 0; start < len(paths); start += gitTrackedBatch {
		end := min(start+gitTrackedBatch, len(paths))
		args := []string{"-c", "core.quotePath=false", "ls-files", "--"}
		args = append(args, paths[start:end]...)
		out, err := vcsOutput(ctx, dir, "git", args...)
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
	return vcsOutput(ctx, dir, "git", "describe", "--tags", "--always", "--dirty")
}

// Tags lists tags newest-first. creatordate is the sort key because it reads a
// lightweight tag's commit date and an annotated tag's own date, so the two kinds
// order together instead of the lightweight ones bunching at the repository's age.
// An ANNOTATED tag's %(objectname) is the TAG OBJECT's id, not the commit it points at,
// while a lightweight tag's is the commit. types.VCSTag.ID promises "the revision
// identifier the tag resolves to", so recording objectname made every annotated tag - the
// kind `git tag -a` and most release tooling create - report an id that matches no commit.
// A caller asking "is this release tagged at HEAD?" compared VCSTag.ID to Commit.ID and
// got no match for exactly the tags a release process creates. %(*objectname) is the
// dereferenced commit, and is EMPTY for a lightweight tag, so the %(if) picks whichever of
// the two is the commit.
func (v gitVCS) Tags(ctx context.Context, dir, pattern string) ([]types.VCSTag, error) {
	const format = "%(refname:short)\t%(creatordate:iso-strict)\t" +
		"%(if)%(*objectname)%(then)%(*objectname)%(else)%(objectname)%(end)"
	out, err := vcsOutput(ctx, dir, "git", "for-each-ref", "--sort=-creatordate", "--format="+format, "refs/tags")
	if err != nil {
		return nil, err
	}
	return parseTags(out, pattern)
}

// RemoteURL returns the "origin" remote's fetch URL (types.RemoteReporter). A repo
// with no origin configured yields ErrVCSUnsupported, so callers degrade to no link.
func (v gitVCS) RemoteURL(ctx context.Context, dir string) (string, error) {
	out, err := vcsOutput(ctx, dir, "git", "remote", "get-url", "origin")
	if err != nil || out == "" {
		return "", types.ErrVCSUnsupported
	}
	return out, nil
}

// DefaultRef resolves the repo's default branch from origin/HEAD, independent of
// the checked-out branch (types.DefaultRefReporter), so committed-doc links stay
// stable across feature branches and worktrees. `git symbolic-ref refs/remotes/origin/HEAD`
// yields "origin/main"; we strip the "origin/" prefix. Yields ErrVCSUnsupported when
// origin/HEAD is unset (e.g. a repo cloned without it), so callers fall back.
func (v gitVCS) DefaultRef(ctx context.Context, dir string) (string, error) {
	out, err := vcsOutput(ctx, dir, "git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil || out == "" {
		return "", types.ErrVCSUnsupported
	}
	return strings.TrimPrefix(out, "origin/"), nil
}

// gitCommitFormat emits the NUL-delimited fields parseCommit expects: id, short,
// author name/email, the commit (record) date as strict ISO 8601 / RFC 3339 (%cI),
// parents, and the raw message (%B).
const gitCommitFormat = "%H%x00%h%x00%an%x00%ae%x00%cI%x00%P%x00%B"

func (v gitVCS) FindCommit(ctx context.Context, dir, rev string) (types.Commit, error) {
	if rev == "" {
		rev = "HEAD"
	}
	if err := checkRef(rev); err != nil {
		return types.Commit{}, err
	}
	// `--` keeps git from treating a path-like rev as a positional path arg.
	out, err := vcsOutput(ctx, dir, "git", "log", "-1", "--format="+gitCommitFormat, rev, "--")
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
	out, err := vcsOutput(ctx, dir, "git", "log", fmt.Sprintf("-%d", limit), "--format=%H")
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
	// core.quotePath=false keeps non-ASCII paths raw (git otherwise emits them
	// double-quoted with octal escapes, which then match no file/project path).
	args := []string{"-c", "core.quotePath=false", "log", fmt.Sprintf("-%d", commits), "--no-merges", "-M", "--name-status", "--format=" + gitChurnFormat}
	if since != "" {
		args = append(args, "--since="+since) // single token: a value can't be read as a flag
	}
	args = append(args, "--", ".")
	out, err := vcsOutput(ctx, dir, "git", args...)
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
// path - or two, for the rename and copy forms, whose letter carries a similarity
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

// Managed-section markers: a locator plus the full line written. Matchers use the
// locator alone, so a section written by an older magus - whose banner used an em-dash -
// is still found and rewritten rather than duplicated below.
const (
	gitAttrsBegin     = "# BEGIN magus-generated"
	gitAttrsBeginLine = gitAttrsBegin + " - do not edit this section manually"
	gitAttrsEnd       = "# END magus-generated"
	gitDriverArgs     = " vcs merge-driver %O %A %B %L %P"

	gitHookBegin     = "# BEGIN magus-refresh"
	gitHookBeginLine = gitHookBegin + " - do not edit this section manually"
	gitHookEnd       = "# END magus-refresh"
)

// gitRefreshHooks fire on a history-changing event that can stale the knowledge graph /
// symbol index: a branch switch, a merge/pull, and a rebase/amend.
var gitRefreshHooks = []string{"post-checkout", "post-merge", "post-rewrite"}

// InstallMergeDriver writes .gitattributes entries and registers the magus merge driver.
func (v gitVCS) InstallMergeDriver(ctx context.Context, root string, outputGlobs []string) error {
	if err := v.writeGitAttrs(root, outputGlobs); err != nil {
		return err
	}
	return v.writeGitConfig(ctx, root)
}

// EnsureMergeDriver re-wires the driver whenever the declared output globs have moved
// on (or nothing wired it in the first place), and reports whether it changed anything.
//
// Installing only at `magus init` leaves the protection frozen at the shape the
// workspace had that day: a project that declares an output later is never added to
// .gitattributes, and a clone that never ran init has no registration at all. Both fail
// the same silent way - a merge conflicts every generated file by hand. The globs are
// derived, so treat the section as derived too and keep it current on its own.
func (v gitVCS) EnsureMergeDriver(ctx context.Context, root string, outputGlobs []string) (bool, error) {
	if len(outputGlobs) == 0 {
		return false, nil
	}
	attrsCurrent, attrsWanted := v.gitAttrsState(root, outputGlobs)
	// One read answers all three questions. Ensure runs on every workspace load and its
	// contract is to be cheap in the steady state, so it cannot spawn a subprocess each.
	registered := v.registeredDriver(ctx, root)
	attrsPresent := v.attrsSectionPresent(root)
	if attrsCurrent == attrsWanted && registered != "" && attrsPresent &&
		driverExeExists(registered) && driverIsReachableHere(ctx, registered) &&
		driverUsable(ctx, registered) {
		return false, nil
	}
	return true, v.InstallMergeDriver(ctx, root, outputGlobs)
}

// registeredDriver returns the command currently registered as the magus merge driver,
// or "" when nothing is registered.
func (v gitVCS) registeredDriver(ctx context.Context, root string) string {
	out, _ := gitExec(ctx, "-C", root, "config", "merge.magus.driver").Output()
	return strings.TrimSpace(string(out))
}

// attrsSectionPresent reports whether .gitattributes carries the managed section.
func (v gitVCS) attrsSectionPresent(root string) bool {
	data, _ := os.ReadFile(filepath.Join(root, ".gitattributes"))
	return strings.Contains(string(data), gitAttrsBegin)
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
// never rewrites - while git invokes a spelling it cannot dispatch.
//
// The wanted string derives from gitDriverArgs, so changing the arguments cannot leave this
// matching a spelling the binary no longer answers to.
// It checks the ARGUMENTS ONLY - exactly the spelling this binary writes. driverUsable is
// what decides whether a registration that does NOT match may still be left alone.
func driverArgsCurrent(registered string) bool {
	return driverArgsMatch(registered, gitDriverArgs)
}

// driverArgsMatch takes the wanted argument string explicitly so a test can pose as a magus
// whose subcommand path differs from this one's. That direction - an OLDER binary reading a
// NEWER registration - is the one a suffix comparison gets wrong, and with gitDriverArgs
// hard-coded it could not be exercised at all: every assertion written against this binary's
// own spelling passes under both the correct rule and the broken one.
func driverArgsMatch(registered, wanted string) bool {
	_, args := splitDriver(registered)
	return args == strings.TrimSpace(wanted)
}

// driverUsable reports whether a registration can be left as it is.
//
// Two registrations differ from the spelling this binary writes, and they need opposite
// treatment. A WRAPPER - `env FOO=1 magus vcs merge-driver %O ...`, a shape splitVCSVerb's doc
// says is supported - works and must be preserved; rewriting it silently drops the wrapper
// someone added on purpose. A STALE VERB, written by a magus whose subcommand path has since
// moved, does not dispatch at all, and leaving it makes git read every generated file as
// conflicted. String comparison cannot separate them: `vcs merge-driver %O ...` ends with
// `merge-driver %O ...`, so a suffix test calls the stale one current and an exact test calls
// the wrapper stale.
//
// So ask the registration itself, and only when it is about to be overwritten. A matching
// spelling short-circuits, which is the overwhelmingly common case and keeps the steady state
// free of subprocesses - the cost lands only on the rare path that was going to rewrite
// anyway, where being right is worth one exec.
func driverUsable(ctx context.Context, registered string) bool {
	return driverUsableFor(ctx, registered, gitDriverArgs)
}

func driverUsableFor(ctx context.Context, registered, wanted string) bool {
	if driverArgsMatch(registered, wanted) {
		return true
	}
	return driverRegistrationAnswers(ctx, registered)
}

// driverRegistrationAnswers runs the registered command's own verb with -h and reads the exit
// status: a command that cannot dispatch what it was registered with exits non-zero.
//
// The git placeholders are dropped rather than substituted. `-h` returns before the child
// opens a workspace or touches an index, so this cannot mutate anything, and it works in a
// tree no released magus can load.
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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// nil Stdout/Stderr: usage text goes to /dev/null, not to whoever loaded the workspace.
	return exec.CommandContext(ctx, exe, append(fields, "-h")...).Run() == nil
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

// CheckMergeDriver reports whether both .gitattributes and git config driver registration are present.
func (v gitVCS) CheckMergeDriver(ctx context.Context, root string) (bool, error) {
	if v.registeredDriver(ctx, root) == "" {
		return false, nil // not configured; not an error
	}
	return v.attrsSectionPresent(root), nil
}

// gitAttrsState returns .gitattributes as it is now and as the declared globs say it
// should be, so callers can compare the two without writing.
func (v gitVCS) gitAttrsState(root string, outputGlobs []string) (current, wanted string) {
	var section strings.Builder
	section.WriteString(gitAttrsBeginLine + "\n")
	for _, glob := range outputGlobs {
		fmt.Fprintf(&section, "%s merge=magus linguist-generated\n", glob)
	}
	section.WriteString(gitAttrsEnd + "\n")
	existing, _ := os.ReadFile(filepath.Join(root, ".gitattributes"))
	return string(existing), replaceManagedSection(string(existing), section.String(), gitAttrsBegin, gitAttrsEnd)
}

func (v gitVCS) writeGitAttrs(root string, outputGlobs []string) error {
	current, updated := v.gitAttrsState(root, outputGlobs)
	if current == updated {
		return nil // already correct: do not churn the file's mtime
	}
	return os.WriteFile(filepath.Join(root, ".gitattributes"), []byte(updated), 0o644)
}

// gitMergeDriverCommand is the command line git runs to resolve a conflict in a
// generated file. Registering the bare word "magus" assumes a released binary on
// PATH; when there is not one, git cannot execute the driver and silently falls
// back to conflict markers in every generated file - which is indistinguishable
// from having no driver at all. Prefer PATH so the registration survives an
// upgrade-in-place, and fall back to this binary's own absolute path so a
// source checkout with no installed magus still merges cleanly.
//
// PATH is preferred only if that binary answers the spelling being registered. Existing on
// PATH is not enough: a source checkout finds an INSTALLED RELEASE there, and pairing that
// path with this binary's spelling registers something nothing can dispatch. One release plus
// one source tree is enough to hit it - the ordinary development setup.
func gitMergeDriverCommand(ctx context.Context) string {
	if exe, err := exec.LookPath("magus"); err == nil && driverExeAnswers(ctx, exe) {
		return quoteDriverExe(exe) + gitDriverArgs
	}
	if exe, err := os.Executable(); err == nil {
		return quoteDriverExe(exe) + gitDriverArgs
	}
	return "magus" + gitDriverArgs
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
// It spawns a subprocess, so it belongs on the repair path only - EnsureMergeDriver returns
// early in the steady state and reaches InstallMergeDriver when something is already wrong.
// `-h` returns before the child opens a workspace, so it also works in a tree no released
// magus can load, which is when the answer matters most.
func driverExeAnswers(ctx context.Context, exe string) bool {
	verb, _, _ := strings.Cut(strings.TrimSpace(gitDriverArgs), " %")
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// nil Stdout/Stderr: the child's usage text goes to /dev/null, not to whoever happened
	// to load the workspace.
	return exec.CommandContext(ctx, exe, append(strings.Fields(verb), "-h")...).Run() == nil
}

// driverExeExists reports whether the command currently registered still resolves to a
// runnable binary. A registration can rot without anyone touching git config: an absolute
// path recorded from a `go run` build points into a temp dir that is gone next run, and a
// path into a since-removed install is no better. git treats a driver it cannot execute
// as a conflict, so a rotted registration behaves exactly like no driver - the failure it
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
// usually THIS worktree's binary - gitMergeDriverCommand falls back to os.Executable()
// when PATH holds no magus that answers - so a shared write points every other worktree at
// this build, and a merge there silently resolves with the wrong tool. Reading stays
// unscoped; `git config <key>` already prefers worktree config.
func (v gitVCS) writeGitConfig(ctx context.Context, root string) error {
	args := []string{"-C", root, "config"}
	if v.worktreeConfigEnabled(ctx, root) {
		args = append(args, "--worktree")
	}
	args = append(args, "merge.magus.driver", gitMergeDriverCommand(ctx))
	if out, err := gitExec(ctx, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("git config merge.magus.driver: %w\n%s", err, out)
	}
	return nil
}

// worktreeConfigEnabled reports whether per-worktree config is available: without the
// extension, `git config --worktree` is an error.
func (v gitVCS) worktreeConfigEnabled(ctx context.Context, root string) bool {
	out, err := gitExec(ctx, "-C", root, "config", "--get", "extensions.worktreeConfig").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// driverIsReachableHere reports whether the registered executable is one this process would
// choose. Anything else is another worktree's build, and counting it as current is how the
// first worktree to register wins permanently.
func driverIsReachableHere(ctx context.Context, registered string) bool {
	exe, _ := splitDriver(registered)
	if exe == "" {
		return false
	}
	if !strings.ContainsRune(exe, filepath.Separator) {
		return true // a bare name resolves through PATH wherever it runs
	}
	if p, err := exec.LookPath("magus"); err == nil && p == exe && driverExeAnswers(ctx, exe) {
		return true
	}
	self, err := os.Executable()
	return err == nil && self == exe
}

// InstallRefreshHook implements types.RefreshHookInstaller: it writes (or refreshes) the
// managed magus section into each of gitRefreshHooks so a history-changing event runs
// command. It reuses replaceManagedSection - the same managed-section mechanism as the
// merge-driver install - so it is idempotent and never clobbers a user's own hook body.
// A non-git tree yields no error and no installs.
func (v gitVCS) InstallRefreshHook(ctx context.Context, root, command string) ([]string, error) {
	hooksDir, err := vcsOutput(ctx, root, "git", "rev-parse", "--git-path", "hooks")
	if err != nil {
		return nil, nil //nolint:nilerr // not a git repo (or git unavailable): nothing to install
	}
	if !filepath.IsAbs(hooksDir) {
		hooksDir = filepath.Join(root, hooksDir)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return nil, fmt.Errorf("vcs: mkdir %s: %w", hooksDir, err)
	}
	var installed []string
	for _, name := range gitRefreshHooks {
		changed, err := writeManagedHook(filepath.Join(hooksDir, name), gitHookBody(name, command))
		if err != nil {
			return installed, err
		}
		if changed {
			installed = append(installed, name)
		}
	}
	return installed, nil
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

// writeManagedHook inserts or updates the managed magus section in the hook at path,
// giving a new file a POSIX-sh shebang and preserving any existing user body. It reports
// whether the file changed and keeps the hook executable.
func writeManagedHook(path, body string) (bool, error) {
	section := gitHookBeginLine + "\n" + body + gitHookEnd + "\n"
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("vcs: read %s: %w", path, err)
	}
	var next string
	if os.IsNotExist(err) {
		// replaceManagedSection normalizes to one blank line before a section, so a new
		// file written without it differs from itself on the next install, forever.
		next = "#!/bin/sh\n\n" + section
	} else {
		next = replaceManagedSection(string(existing), section, gitHookBegin, gitHookEnd)
	}
	if next == string(existing) {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(next), 0o755); err != nil {
		return false, fmt.Errorf("vcs: write %s: %w", path, err)
	}
	return true, nil
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

// runGitBatched runs `git -C root <args...> -- <paths>` in argv-sized batches.
func runGitBatched(ctx context.Context, root string, args []string, paths []string) error {
	for _, chunk := range gitPathChunks(paths) {
		argv := append([]string{"-C", root}, args...)
		argv = append(argv, "--")
		argv = append(argv, chunk...)
		if out, err := gitExec(ctx, argv...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
		}
	}
	return nil
}

// gitPathPrefix returns root's path relative to the repository top level with a trailing
// slash, or "" when root is the top level. Porcelain paths are top-level-relative, so a
// workspace rooted deeper needs this to translate them.
func gitPathPrefix(ctx context.Context, root string) string {
	out, err := vcsOutput(ctx, root, "git", "rev-parse", "--show-prefix")
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
	out, err := gitExec(ctx, "-C", root,
		"status", "--porcelain=v1", "-z", "--no-renames", "-uno").Output()
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	return parseConflicts(string(out), gitPathPrefix(ctx, root)), nil
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
// A ref beginning with "-" is refused rather than passed through, where git would read it
// as a flag. `git merge` has no `--` separator for its ref argument, so rejecting it is
// the only guard available.
func (v gitVCS) StartMerge(ctx context.Context, root, ref string) error {
	if err := checkRef(ref); err != nil {
		return err
	}
	out, err := gitExec(ctx, "-C", root, "merge", "--no-commit", "--no-ff", ref).CombinedOutput()
	if err == nil {
		return nil
	}
	// A merge that CONFLICTS exits non-zero, and that is the case this exists to set up -
	// the conflicts are the payload, reported by Conflicts. Only a merge that never began
	// is an error. git leaves MERGE_HEAD behind exactly when one is underway.
	if _, statErr := os.Stat(filepath.Join(root, ".git", "MERGE_HEAD")); statErr == nil {
		return nil
	}
	return fmt.Errorf("git merge %s: %w\n%s", ref, err, strings.TrimSpace(string(out)))
}

// AbortMerge abandons the in-progress merge. See types.MergeStarter.
func (v gitVCS) AbortMerge(ctx context.Context, root string) error {
	if out, err := gitExec(ctx, "-C", root, "merge", "--abort").CombinedOutput(); err != nil {
		return fmt.Errorf("git merge --abort: %w\n%s", err, strings.TrimSpace(string(out)))
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
// of them - including the generated file whose conflict is that one side stopped tracking
// it. The question is about the RULES: would this path be ignored if nothing tracked it.
func (v gitVCS) IgnoredPaths(ctx context.Context, root string, paths []string) (map[string]bool, error) {
	ignored := make(map[string]bool, len(paths))
	if len(paths) == 0 {
		return ignored, nil
	}
	for _, chunk := range gitPathChunks(paths) {
		cmd := gitExec(ctx, "-C", root, "check-ignore", "--no-index", "-z", "--stdin")
		cmd.Stdin = strings.NewReader(strings.Join(chunk, "\x00") + "\x00")
		out, err := cmd.Output()
		var exitErr *exec.ExitError
		if err != nil && (!errors.As(err, &exitErr) || exitErr.ExitCode() != 1) {
			return nil, fmt.Errorf("git check-ignore: %w", err)
		}
		for _, p := range strings.Split(string(out), "\x00") {
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
// way, and GIT_CONFIG_* can inject config into a run that asked for none.
var gitRedirectVars = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_NAMESPACE", "GIT_PREFIX", "GIT_CEILING_DIRECTORIES",
	"GIT_CONFIG", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS",
}

// gitEnviron is the process environment with gitRedirectVars removed, so -C and cmd.Dir
// decide which repository a subprocess acts on.
//
// Deliberately NOT "drop everything matching GIT_*". That prefix is not one category, it is
// two, and only one of them is a problem. The variables above answer WHICH repository; the
// rest answer HOW to work on it - GIT_SSH_COMMAND, GIT_ASKPASS and GIT_PROXY_COMMAND are
// how a fetch authenticates (recoverMergeBase really does fetch), GIT_TERMINAL_PROMPT=0 is
// what stops CI hanging on a credential prompt, GIT_EXEC_PATH is how a nonstandard install
// finds its own subcommands, and GIT_TRACE is someone actively debugging. Stripping those
// turns a working fetch into an auth failure, or silently discards the flag the user set to
// find out why. The blanket rule is simpler to write and strictly worse to run.
//
// The maintenance rule, so this does not become guesswork: add a variable here only if it
// changes which repository, work tree, index, object store, or ref namespace git acts on.
// Anything governing transport, credentials, identity, or diagnostics stays inherited.
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

// gitExec builds a git subprocess whose environment cannot redirect it off the repository
// the caller named. Use it instead of exec.CommandContext for every git invocation; the
// caller still sets cmd.Dir or passes -C as before.
func gitExec(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", uncolored("git", args)...)
	cmd.Env = gitEnviron()
	return cmd
}

// uncolored prepends the switch that stops a backend emitting ANSI, so magus never parses
// output it has to strip first.
//
// Not defensive: a user with `color.ui = always` in their gitconfig - a common setting, since
// it is how you keep color when piping to a pager - made `magus diff` list every UNTRACKED
// file and silently drop every tracked one, at exit 0. The escape sequence lands in front of
// the `diff --git` header, so the header stops beginning a line and no reader sees it. magus
// synthesizes the untracked half itself, uncolored, which is why output still appeared and
// the failure looked like a clean answer.
//
// hg, Sapling and jj all take a global `--color=never`. git is the exception: it has no such
// top-level flag (only a per-subcommand `--color`, which not every subcommand accepts), so it
// gets the config override, which covers diff, log and status alike.
//
// Environment variables are NOT an option here, measured against all four with color forced
// on in repository config: NO_COLOR, HGPLAIN and TERM=dumb each failed to suppress it. An
// explicit config value outranks every one of them, and an explicit flag is what outranks the
// config. That is the whole reason this prepends a flag rather than scrubbing an env.
//
// The switch goes FIRST because each is a global option that must precede the subcommand.
func uncolored(name string, args []string) []string {
	switch name {
	case "git":
		return append([]string{"-c", "color.ui=false"}, args...)
	case "hg", "sl", "jj":
		return append([]string{"--color=never"}, args...)
	default:
		return args
	}
}

// vcsExec builds a VCS subprocess with color already suppressed. Use it instead of
// exec.CommandContext for every hg, Sapling, and jj invocation; git has gitExec, which also
// scrubs the environment. The caller still sets cmd.Dir or passes the backend's -R/-C flag.
func vcsExec(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, uncolored(name, args)...)
}

// vcsOutput runs a VCS subcommand in dir and returns its trimmed stdout.
// An empty dir uses the process working directory (the exec.Cmd.Dir convention).
func vcsOutput(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, uncolored(name, args)...)
	cmd.Dir = dir
	if name == "git" {
		cmd.Env = gitEnviron()
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// vcsOutputRaw is vcsOutput without the leading-whitespace trim, for output whose
// COLUMNS carry meaning. git porcelain puts two status characters before the path, and
// the first is a space for an unstaged edit (" M path"); TrimSpace ate it on the first
// line only, so exactly one path per status came back missing its first character.
func vcsOutputRaw(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, uncolored(name, args)...)
	cmd.Dir = dir
	if name == "git" {
		cmd.Env = gitEnviron()
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// revFileOutput runs a backend's "show this file at this revision" command and returns
// stdout EXACTLY - no trimming of any kind, unlike vcsOutput and vcsOutputRaw.
//
// This is file CONTENT, not a status line. A trailing newline is part of the file, and a
// helper that ate it would hand back something that is not what the revision holds - which
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
// spells a revision:path pair on every platform - it is an object lookup, not a filesystem
// one, so filepath.FromSlash here would break it on Windows rather than fix it.
func (gitVCS) ReadFileAt(ctx context.Context, root, rev, path string) (string, error) {
	if rev == "" {
		rev = "HEAD"
	}
	if err := checkRef(rev); err != nil {
		return "", err
	}
	return revFileOutput(gitExec(ctx, "-C", root, "show", rev+":"+path),
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
	if err := checkRef(rev); err != nil {
		return err
	}
	cmd := gitExec(ctx, "-C", dir, "archive", "--format=tar", rev, "--", ".")
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
