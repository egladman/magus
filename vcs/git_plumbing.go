package vcs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egladman/magus/types"
)

// The capabilities that combine revisions without a working copy, or in a checkout magus
// owns: tree identity, tree merges, commits, secondary checkouts, fetch, push and bundles.
// Every call here passes NoHooks, since no hook on the box belongs to that work, and every
// revision argument is checked before git sees it.

// scratchSeq numbers scratchRef's refs within this process.
var scratchSeq atomic.Int64

// scratchRef names a ref that is unique to this process and call, under refs/magus/<kind>/,
// so concurrent calls, and a branch named like another's prefix, can never share one.
func scratchRef(kind string) string {
	return fmt.Sprintf("refs/magus/%s/%d-%d", kind, os.Getpid(), scratchSeq.Add(1))
}

// dropScratchRef deletes a scratchRef whatever became of the caller's context, so a
// cancelled call leaves no ref behind.
func dropScratchRef(ctx context.Context, root, ref string) {
	_, _ = gitOutput(context.WithoutCancel(ctx), root, gitOpts{NoHooks: true}, "update-ref", "-d", ref)
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
	return gitDiffTree(ctx, root, a, b)
}

// MergeTrees implements types.TreeMerger with `merge-tree --write-tree`, which exits 1 when
// the merge conflicts: an answer, read as one.
func (v gitVCS) MergeTrees(ctx context.Context, root string, m types.TreeMerge) (types.MergeResult, error) {
	if err := checkRequiredRev(m.Ours, m.Theirs); err != nil {
		return types.MergeResult{}, err
	}
	if err := checkRev(m.Base); err != nil {
		return types.MergeResult{}, err
	}
	args := []string{"merge-tree", "--write-tree", "--no-messages", "-z"}
	if m.Base != "" {
		args = append(args, "--merge-base="+m.Base)
	}
	args = append(args, m.Ours, m.Theirs)
	var stderr bytes.Buffer
	cmd := gitExec(ctx, root, gitOpts{NoHooks: true}, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	switch code := gitExitCode(err); {
	case err == nil, code == 1:
	case code == 129:
		return types.MergeResult{}, errors.New("vcs: git: merge-tree --write-tree needs git 2.40 or newer")
	default:
		return types.MergeResult{}, fmt.Errorf("git merge-tree %s %s: %w: %s", m.Ours, m.Theirs, err, strings.TrimSpace(stderr.String()))
	}
	return parseMergeTree(string(out))
}

// parseMergeTree reads `merge-tree --write-tree -z` output: the tree id, then one
// "<mode> <object> <stage>\t<path>" record per conflicted index entry. Which stages a path
// has classifies it: both sides (2 and 3) is a content conflict, the base alone is a
// deletion on both sides, and anything else is one side deleting what the other changed.
func parseMergeTree(out string) (types.MergeResult, error) {
	fields := splitNUL(out)
	if len(fields) == 0 {
		return types.MergeResult{}, errors.New("vcs: git merge-tree printed no tree")
	}
	res := types.MergeResult{Tree: fields[0]}
	stages := map[string][]byte{}
	var order []string
	for _, rec := range fields[1:] {
		meta, path, ok := strings.Cut(rec, "\t")
		parts := strings.Fields(meta)
		if !ok || len(parts) != 3 || len(parts[2]) != 1 {
			return types.MergeResult{}, fmt.Errorf("vcs: git merge-tree: unreadable conflict record %q", rec)
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

// CommitTree implements types.TreeMerger. The message goes on stdin, so no message can be
// read as an option and none is cleaned up.
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
	env, err := commitIdentity(c.Author, c.Committer, c.Date)
	if err != nil {
		return "", err
	}
	args = append(args, "-F", "-")
	id, err := gitOutput(ctx, root, gitOpts{NoHooks: true, Env: env, Stdin: []byte(c.Message)}, args...)
	if err != nil {
		return "", fmt.Errorf("git commit-tree: %w", err)
	}
	return id, nil
}

// commitIdentity is the environment that names a commit's author and committer, so a
// commit never falls back to whoever configured the box. Both need a name and an email.
func commitIdentity(author, committer types.Person, date time.Time) ([]string, error) {
	for _, p := range []struct {
		role string
		who  types.Person
	}{{"author", author}, {"committer", committer}} {
		if p.who.Name == "" || p.who.Email == "" {
			return nil, fmt.Errorf("vcs: a commit needs a %s with a name and an email", p.role)
		}
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + author.Name, "GIT_AUTHOR_EMAIL=" + author.Email,
		"GIT_COMMITTER_NAME=" + committer.Name, "GIT_COMMITTER_EMAIL=" + committer.Email,
	}
	if !date.IsZero() {
		stamp := fmt.Sprintf("@%d %s", date.Unix(), date.Format("-0700"))
		env = append(env, "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	}
	return env, nil
}

// Commit implements types.Committer. Paths are recorded literally, then `commit` runs with
// the message verbatim from stdin and no hook, so the box's commit.cleanup, template and
// hooks cannot change what is recorded.
func (v gitVCS) Commit(ctx context.Context, root string, o types.CommitOptions) (string, error) {
	env, err := commitIdentity(o.Author, o.Committer, o.Date)
	if err != nil {
		return "", err
	}
	if err := runGitBatched(ctx, root, []string{"add", "-A"}, o.Paths); err != nil {
		return "", err
	}
	opts := gitOpts{NoHooks: true, Env: env, Stdin: []byte(o.Message)}
	if _, err := gitOutput(ctx, root, opts, "commit", "--quiet", "--no-verify", "--cleanup=verbatim", "-F", "-"); err != nil {
		return "", fmt.Errorf("git commit: %w", err)
	}
	return gitOutput(ctx, root, gitOpts{}, "rev-parse", "--verify", "HEAD")
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
		return nil, fmt.Errorf("vcs: git: %s overrides the revision's attributes; remove it", local)
	}
	opts := gitOpts{Env: []string{"GIT_ATTR_NOSYSTEM=1"}, Raw: true}
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

// CreateCheckout implements types.CheckoutCreator with a detached linked worktree.
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
	top, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if pathUnder(top, dir) {
		return fmt.Errorf("vcs: checkout %s lies inside %s, where discovery would index it as a copy of the workspace", dir, top)
	}
	worktreeAdmin.Lock()
	defer worktreeAdmin.Unlock()
	if _, err := gitOutput(ctx, root, gitOpts{NoHooks: true}, "worktree", "add", "--quiet", "--detach", "--", dir, rev); err != nil {
		return fmt.Errorf("git worktree add %s: %w", dir, err)
	}
	return nil
}

// RemoveCheckout implements types.CheckoutCreator. --force removes a checkout with changes
// in it, and drops the registration of one whose directory is already gone.
func (v gitVCS) RemoveCheckout(ctx context.Context, root, dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("vcs: checkout %q is not an absolute path", dir)
	}
	worktreeAdmin.Lock()
	defer worktreeAdmin.Unlock()
	if _, err := gitOutput(ctx, root, gitOpts{NoHooks: true}, "worktree", "remove", "--force", "--", dir); err != nil {
		return fmt.Errorf("git worktree remove %s: %w", dir, err)
	}
	return nil
}

// Checkouts implements types.CheckoutCreator. The first record `worktree list` prints is
// the main checkout, which is not a secondary one.
func (v gitVCS) Checkouts(ctx context.Context, root string) ([]string, error) {
	out, err := gitOutput(ctx, root, gitOpts{Raw: true}, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	var dirs []string
	for _, f := range splitNUL(out) {
		if dir, ok := strings.CutPrefix(f, "worktree "); ok {
			dirs = append(dirs, dir)
		}
	}
	if len(dirs) == 0 {
		return nil, nil
	}
	return dirs[1:], nil
}

// FetchBranch implements types.RevisionFetcher.
func (v gitVCS) FetchBranch(ctx context.Context, root, remote, branch string) (string, error) {
	if err := checkRemoteName(remote); err != nil {
		return "", err
	}
	if err := checkBranchName(branch); err != nil {
		return "", err
	}
	return fetchCommit(ctx, root, remote, "refs/heads/"+branch)
}

// FetchRef implements types.RevisionFetcher.
func (v gitVCS) FetchRef(ctx context.Context, root, remote, ref string) (string, error) {
	if err := checkRemoteName(remote); err != nil {
		return "", err
	}
	if err := checkRefName(ref); err != nil {
		return "", err
	}
	return fetchCommit(ctx, root, remote, ref)
}

// FetchCommit implements types.RevisionFetcher.
func (v gitVCS) FetchCommit(ctx context.Context, root, remote, id string) error {
	if err := checkRemoteName(remote); err != nil {
		return err
	}
	if err := checkCommitID(id); err != nil {
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
		return fmt.Errorf("vcs: git: fetching %s from %s produced %s", id, remote, got)
	}
	return nil
}

// fetchCommit fetches src from remote into a scratch ref, returns the commit it names, and
// deletes the ref. --no-tags stops tag following, and an empty --refmap stops the
// remote-tracking update git otherwise makes when src matches remote.<name>.fetch, so no
// ref the user reads moves. The fetched commit rests on gc's prune grace, two weeks by
// default, which outlasts any caller that goes on to anchor it.
func fetchCommit(ctx context.Context, root, remote, src string) (string, error) {
	tmp := scratchRef("fetch")
	defer dropScratchRef(ctx, root, tmp)
	if _, err := gitOutput(ctx, root, gitOpts{NoHooks: true}, "fetch", "--quiet", "--no-tags",
		"--no-write-fetch-head", "--no-recurse-submodules", "--no-auto-maintenance", "--refmap=",
		"--", remote, "+"+src+":"+tmp); err != nil {
		return "", fmt.Errorf("git fetch %s %s: %w", remote, src, err)
	}
	id, err := gitOutput(ctx, root, gitOpts{}, "rev-parse", "--verify", "--end-of-options", tmp+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("git fetch %s %s: %s names no commit: %w", remote, src, src, err)
	}
	return id, nil
}

// Push implements types.Pusher. Each refused ref is read from --porcelain's "!" line,
// "<flag>\t<from>:<to>\t<summary> (<reason>)", never from stderr's prose: a lease that no
// longer holds is "stale info", and every other refusal is the remote's own.
func (v gitVCS) Push(ctx context.Context, root, remote, ref, id, expected string) error {
	if err := checkRemoteName(remote); err != nil {
		return err
	}
	if err := checkRefName(ref); err != nil {
		return err
	}
	if err := checkCommitID(id); err != nil {
		return err
	}
	if err := checkCommitID(expected); err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd := gitExec(ctx, root, gitOpts{NoHooks: true}, "-c", "push.gpgSign=false",
		"push", "--quiet", "--porcelain", "--no-verify", "--no-follow-tags",
		"--force-with-lease="+ref+":"+expected, "--", remote, id+":"+ref)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		return nil
	}
	if reason, ok := pushRefusal(string(out), ref); ok {
		if reason == "stale info" {
			return types.ErrPushLease
		}
		return &types.PushRejectedError{Ref: ref, Reason: reason}
	}
	return fmt.Errorf("git push %s %s: %w: %s", remote, ref, err, strings.TrimSpace(stderr.String()))
}

// pushRefusal finds ref's "!" line in `push --porcelain` output and returns the reason in
// its trailing parentheses, or the summary when there is none.
func pushRefusal(out, ref string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 || fields[0] != "!" || !strings.HasSuffix(fields[1], ":"+ref) {
			continue
		}
		summary := strings.TrimSpace(fields[2])
		if open := strings.LastIndex(summary, " ("); open >= 0 && strings.HasSuffix(summary, ")") {
			return summary[open+2 : len(summary)-1], true
		}
		return summary, true
	}
	return "", false
}

// Bundle implements types.Bundler. A bundle carries refs, not bare commits, so rev rides
// under a scratch ref that is gone when Bundle returns.
func (v gitVCS) Bundle(ctx context.Context, root, file, exclude, rev string) error {
	if err := checkCommitID(exclude); err != nil {
		return err
	}
	if err := checkCommitID(rev); err != nil {
		return err
	}
	if !filepath.IsAbs(file) {
		return fmt.Errorf("vcs: bundle %q is not an absolute path", file)
	}
	tmp := scratchRef("bundle")
	if _, err := gitOutput(ctx, root, gitOpts{NoHooks: true}, "update-ref", tmp, rev); err != nil {
		return fmt.Errorf("git update-ref %s: %w", tmp, err)
	}
	defer dropScratchRef(ctx, root, tmp)
	if _, err := gitOutput(ctx, root, gitOpts{}, "bundle", "create", "--quiet", file, tmp, "^"+exclude); err != nil {
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
	if _, err := gitOutput(ctx, root, gitOpts{}, "bundle", "unbundle", file); err != nil {
		return fmt.Errorf("git bundle unbundle %s: %w", file, err)
	}
	return nil
}
