package vcs

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/types"
)

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

// CreateCheckout implements types.CheckoutProvisioner with a detached linked worktree,
// locked with checkoutLockReason.
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
	worktreeAdmin.Lock()
	defer worktreeAdmin.Unlock()
	if _, err := gitOutput(ctx, root, gitOpts{Isolated: true}, "worktree", "add", "--quiet", "--detach",
		"--lock", "--reason", checkoutLockReason, "--", dir, rev); err != nil {
		return fmt.Errorf("git worktree add %s: %w", dir, err)
	}
	return nil
}

// RemoveCheckout implements types.CheckoutProvisioner. The doubled --force removes a
// locked worktree, one with changes in it, and the registration of one whose directory is
// gone.
func (v gitVCS) RemoveCheckout(ctx context.Context, root, dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("vcs: checkout %q is not an absolute path", dir)
	}
	worktreeAdmin.Lock()
	defer worktreeAdmin.Unlock()
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

// staleLeaseReasons are the refusals that mean the ref was not where the lease expected:
// git's own check before sending ("stale info"), and the remote's under a race it lost
// ("cannot lock ref", "failed to update ref", "incorrect old value provided").
var staleLeaseReasons = []string{"stale info", "cannot lock ref", "failed to update ref", "incorrect old value provided"}

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
		for _, stale := range staleLeaseReasons {
			if strings.Contains(reason, stale) {
				return types.ErrStaleLease
			}
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
