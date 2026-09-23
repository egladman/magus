package vcs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// queueIdentity is who the tests commit as through the capabilities, which read no
// identity from the box.
var queueIdentity = types.Person{Name: "queue", Email: "queue@magus.invalid"}

// queueMeta is a commit by queueIdentity with message.
func queueMeta(message string) types.CommitMeta {
	return types.CommitMeta{Message: message, Author: queueIdentity, Committer: queueIdentity}
}

// isolateGitConfig keeps the developer's own git config out of the production calls a
// test makes, since gitExec (rightly) inherits it.
func isolateGitConfig(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
}

// remoteFixture is a bare remote and a clone of it whose origin is that remote: the shape
// a queue's checkout has. main holds base.txt and is tagged v1.
type remoteFixture struct {
	remote, clone string
}

func newRemoteFixture(t *testing.T) remoteFixture {
	t.Helper()
	isolateGitConfig(t)
	remote := t.TempDir()
	gitRun(t, remote, "init", "-q", "--bare", "-b", "main")
	seed := t.TempDir()
	gitInitRepo(t, seed, map[string]string{"base.txt": "base\n"})
	gitRun(t, seed, "branch", "-M", "main")
	gitRun(t, seed, "tag", "v1")
	gitRun(t, seed, "remote", "add", "origin", remote)
	gitRun(t, seed, "push", "-q", "origin", "main", "v1")

	clone := t.TempDir()
	gitRun(t, clone, "clone", "-q", "--no-tags", "file://"+remote, ".")
	gitRun(t, clone, "config", "maintenance.auto", "false")
	gitRun(t, clone, "config", "gc.auto", "0")
	return remoteFixture{remote: remote, clone: clone}
}

// advance commits one file to branch on the remote, through a scratch clone, and returns
// the new tip.
func (f remoteFixture) advance(t *testing.T, branch, name string) string {
	t.Helper()
	work := t.TempDir()
	gitRun(t, work, "clone", "-q", "file://"+f.remote, ".")
	start := "origin/main"
	if strings.Contains(gitTestOutput(t, work, "branch", "-r"), "origin/"+branch) {
		start = "origin/" + branch
	}
	gitRun(t, work, "checkout", "-q", "-B", branch, start)
	require.NoError(t, os.WriteFile(filepath.Join(work, name), []byte(name+"\n"), 0o644))
	gitRun(t, work, "add", "-A")
	gitRun(t, work, "commit", "-q", "-m", name)
	gitRun(t, work, "tag", "t-"+strings.ReplaceAll(name, ".", "-"))
	gitRun(t, work, "push", "-q", "origin", branch, "--tags")
	return gitTestOutput(t, work, "rev-parse", "HEAD")
}

func refsOf(t *testing.T, dir string) string {
	t.Helper()
	return gitTestOutput(t, dir, "for-each-ref", "--format=%(refname) %(objectname)")
}

// B4: a fetch that moved origin/main, followed a tag, or left a ref behind would change
// the user's view of the remote under a command that only asked for a commit.
func TestFetchRefLeavesTagsAndTrackingRefsAlone(t *testing.T) {
	f := newRemoteFixture(t)
	before := refsOf(t, f.clone)
	tip := f.advance(t, "main", "next.txt")

	got, err := gitVCS{}.FetchRef(t.Context(), f.clone, "origin", "refs/heads/main")
	require.NoError(t, err)
	assert.Equal(t, tip, got)
	assert.Equal(t, before, refsOf(t, f.clone), "fetching a branch moved, added or left a ref")
	gitRun(t, f.clone, "cat-file", "-e", tip+"^{commit}")
}

// B4: temporary refs keyed by branch alone collide under concurrency and across remotes
// that share a branch name. Each call here must get its own remote's tip.
func TestFetchRefConcurrentCallsDoNotShareARef(t *testing.T) {
	f := newRemoteFixture(t)
	other := newRemoteFixture(t)
	gitRun(t, f.clone, "remote", "add", "other", other.remote)
	tips := map[string]string{
		"origin": f.advance(t, "main", "mine.txt"),
		"other":  other.advance(t, "main", "theirs.txt"),
	}
	type result struct{ remote, id string }
	var wg sync.WaitGroup
	results := make(chan result, 16)
	errs := make(chan error, 16)
	for i := range 16 {
		remote := []string{"origin", "other"}[i%2]
		wg.Go(func() {
			id, err := gitVCS{}.FetchRef(t.Context(), f.clone, remote, "refs/heads/main")
			if err != nil {
				errs <- err
				return
			}
			results <- result{remote, id}
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	for r := range results {
		assert.Equal(t, tips[r.remote], r.id, "a fetch from %s answered with another fetch's tip", r.remote)
	}
	assert.NotContains(t, refsOf(t, f.clone), "refs/magus/", "a fetch left its scratch ref behind")
}

func TestFetchRefAndFetchCommit(t *testing.T) {
	f := newRemoteFixture(t)
	tip := f.advance(t, "feature", "feature.txt")

	got, err := gitVCS{}.FetchRef(t.Context(), f.clone, "origin", "refs/heads/feature")
	require.NoError(t, err)
	assert.Equal(t, tip, got)

	// Already present: nothing to fetch, and no error.
	require.NoError(t, gitVCS{}.FetchCommit(t.Context(), f.clone, "origin", tip))

	unseen := f.advance(t, "feature", "later.txt")
	require.NoError(t, gitVCS{}.FetchCommit(t.Context(), f.clone, "origin", unseen))
	gitRun(t, f.clone, "cat-file", "-e", unseen+"^{commit}")
}

// B10: a value shaped like a refspec, an option or a URL never reaches fetch or push.
func TestRemoteArgumentsRefuseRefspecsOptionsAndURLs(t *testing.T) {
	f := newRemoteFixture(t)
	before := refsOf(t, f.clone)
	id := gitTestOutput(t, f.clone, "rev-parse", "HEAD")
	g := gitVCS{}
	ctx := t.Context()

	_, err := g.FetchRef(ctx, f.clone, "origin", "+refs/heads/*:refs/heads/*")
	require.Error(t, err)
	_, err = g.FetchRef(ctx, f.clone, "origin", "refs/heads/main:refs/heads/main")
	require.Error(t, err)
	_, err = g.FetchRef(ctx, f.clone, "file://"+f.remote, "refs/heads/main")
	require.Error(t, err, "a URL is not a configured remote's name")
	_, err = g.FetchRef(ctx, f.clone, "--upload-pack=touch x", "refs/heads/main")
	require.Error(t, err)
	require.Error(t, g.FetchCommit(ctx, f.clone, "origin", "HEAD"), "a revision expression is not a commit id")
	require.Error(t, g.Push(ctx, f.clone, types.PushLease{Remote: "origin", Ref: "refs/heads/*", To: id, Expected: id}))
	_, err = g.TreeID(ctx, f.clone, "+refs/heads/main:refs/heads/x")
	require.Error(t, err)
	_, err = g.MergeTrees(ctx, f.clone, types.TreeMerge{Ours: "HEAD", Theirs: "-x"})
	require.Error(t, err)
	assert.Equal(t, before, refsOf(t, f.clone))
}

// A name git would read as a path must not become one: with a repository at ./evil and no
// remote called evil, fetch and push refuse rather than talk to ./evil.
func TestFetchAndPushRefuseARemoteNobodyConfigured(t *testing.T) {
	f := newRemoteFixture(t)
	evil := filepath.Join(f.clone, "evil")
	gitRun(t, f.clone, "clone", "-q", "--bare", "file://"+f.remote, evil)
	id := gitTestOutput(t, f.clone, "rev-parse", "HEAD")
	g := gitVCS{}

	_, err := g.FetchRef(t.Context(), f.clone, "evil", "refs/heads/main")
	require.ErrorContains(t, err, "not a remote this repository has configured")
	require.ErrorContains(t, g.FetchCommit(t.Context(), f.clone, "evil", strings.Repeat("a", 40)), "not a remote")
	err = g.Push(t.Context(), f.clone, types.PushLease{Remote: "evil", Ref: "refs/heads/x", To: id, Expected: id})
	require.ErrorContains(t, err, "not a remote")
	assert.Empty(t, gitTestOutput(t, evil, "for-each-ref", "refs/heads/x"))
}

// B7: a lease that no longer holds is ErrStaleLease, and a refusal of the remote's own is a
// *PushRejectedError carrying its words, read from --porcelain rather than stderr.
func TestPushLeaseAndRejection(t *testing.T) {
	f := newRemoteFixture(t)
	g := gitVCS{}
	ctx := t.Context()
	base := gitTestOutput(t, f.clone, "rev-parse", "HEAD")
	gitRun(t, f.clone, "commit", "-q", "--allow-empty", "-m", "update")
	update := gitTestOutput(t, f.clone, "rev-parse", "HEAD")
	lease := func(ref, to, expected string) types.PushLease {
		return types.PushLease{Remote: "origin", Ref: ref, To: to, Expected: expected}
	}

	require.NoError(t, g.Push(ctx, f.clone, lease("refs/heads/main", update, base)))
	assert.Equal(t, update, gitTestOutput(t, f.remote, "rev-parse", "refs/heads/main"))

	require.NoError(t, g.Push(ctx, f.clone, lease("refs/heads/main", update, base)),
		"a ref already at id is the push having happened, not a lost lease")

	gitRun(t, f.clone, "commit", "-q", "--allow-empty", "-m", "late")
	late := gitTestOutput(t, f.clone, "rev-parse", "HEAD")
	err := g.Push(ctx, f.clone, lease("refs/heads/main", late, base))
	require.ErrorIs(t, err, types.ErrStaleLease, "the branch moved since base")

	err = g.Push(ctx, f.clone, lease("refs/heads/gone", update, base))
	require.ErrorIs(t, err, types.ErrStaleLease, "a lease never creates a branch")
	assert.Empty(t, gitTestOutput(t, f.remote, "for-each-ref", "refs/heads/gone"))

	hook := filepath.Join(f.remote, "hooks", "pre-receive")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755))
	gitRun(t, f.clone, "commit", "-q", "--allow-empty", "-m", "refused")
	refused := gitTestOutput(t, f.clone, "rev-parse", "HEAD")
	err = g.Push(ctx, f.clone, lease("refs/heads/main", refused, update))
	var rejected *types.PushRejectedError
	require.ErrorAs(t, err, &rejected)
	assert.Equal(t, &types.PushRejectedError{Ref: "refs/heads/main", Reason: "pre-receive hook declined"}, rejected)
	assert.NotErrorIs(t, err, types.ErrStaleLease, "a refusal retrying cannot fix is not a lease failure")
}

// A lease the remote itself finds stale, after git's own check passed, is the same stale
// lease: the ref moved under a race. Only a refusal for a reason of the remote's own is a
// rejection.
func TestPushRefusalReadsARemoteLeaseLossAsStale(t *testing.T) {
	line := func(reason string) string {
		return "To /remote\n!\t" + strings.Repeat("a", 40) + ":refs/heads/main\t" + reason + "\nDone\n"
	}
	for _, reason := range []string{
		"[rejected] (stale info)",
		"[remote rejected] (cannot lock ref 'refs/heads/main': is at 111 but expected 222)",
		"[remote rejected] (failed to update ref)",
		"[remote rejected] (incorrect old value provided)",
	} {
		assert.ErrorIs(t, pushRefusal(line(reason), "refs/heads/main"), types.ErrStaleLease, reason)
	}
	assert.Equal(t, &types.PushRejectedError{Ref: "refs/heads/main", Reason: "protected branch hook declined"},
		pushRefusal(line("[remote rejected] (protected branch hook declined)"), "refs/heads/main"))
	assert.NoError(t, pushRefusal(line("[rejected] (stale info)"), "refs/heads/other"), "another ref's line is not this one's")
}

// B6: no hook on the box runs under a commit, a push or a checkout magus makes.
func TestCommitPushAndCheckoutRunNoHooks(t *testing.T) {
	f := newRemoteFixture(t)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hooks := filepath.Join(f.clone, ".git", "hooks")
	for _, name := range []string{"pre-commit", "commit-msg", "prepare-commit-msg", "post-commit", "pre-push", "post-checkout", "reference-transaction"} {
		body := fmt.Sprintf("#!/bin/sh\necho %s >> %q\n", name, marker)
		require.NoError(t, os.WriteFile(filepath.Join(hooks, name), []byte(body), 0o755))
	}
	g := gitVCS{}
	ctx := t.Context()
	base := gitTestOutput(t, f.clone, "rev-parse", "HEAD")

	require.NoError(t, os.WriteFile(filepath.Join(f.clone, "new.txt"), []byte("new\n"), 0o644))
	id, err := g.Commit(ctx, f.clone, types.CheckoutCommit{
		CommitMeta: types.CommitMeta{Message: "add new", Author: queueIdentity, Committer: queueIdentity},
		Paths:      []string{"new.txt"},
	})
	require.NoError(t, err)
	require.NoError(t, g.Push(ctx, f.clone, types.PushLease{Remote: "origin", Ref: "refs/heads/main", To: id, Expected: base}))
	require.NoError(t, g.CreateCheckout(ctx, f.clone, filepath.Join(t.TempDir(), "co"), id))

	_, statErr := os.Stat(marker)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "a hook ran")
}

// B11: a path is a path. "*.txt" commits the file of that name, not every .txt file.
func TestCommitRecordsLiteralPaths(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"keep.txt": "one\n"})
	writeRepoFile(t, dir, "*.txt", "star\n")
	writeRepoFile(t, dir, "keep.txt", "two\n")

	id, err := gitVCS{}.Commit(t.Context(), dir, types.CheckoutCommit{
		CommitMeta: types.CommitMeta{Message: "literal\n\nbody kept verbatim\n", Author: queueIdentity, Committer: queueIdentity},
		Paths:      []string{"*.txt"},
	})
	require.NoError(t, err)
	assert.Equal(t, "*.txt", gitTestOutput(t, dir, "diff-tree", "--no-commit-id", "--name-only", "-r", id))
	assert.Equal(t, "literal\n\nbody kept verbatim", gitTestOutput(t, dir, "log", "-1", "--format=%B", id))
	assert.Equal(t, "queue queue@magus.invalid", gitTestOutput(t, dir, "log", "-1", "--format=%cn %ce", id))
}

func TestCommitRefusesAnIncompleteIdentityAndAnEmptyCommit(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "a\n"})
	head := gitTestOutput(t, dir, "rev-parse", "HEAD")

	_, err := gitVCS{}.Commit(t.Context(), dir, types.CheckoutCommit{CommitMeta: types.CommitMeta{Message: "x", Author: queueIdentity}})
	require.ErrorContains(t, err, "committer")
	_, err = gitVCS{}.Commit(t.Context(), dir, types.CheckoutCommit{CommitMeta: queueMeta("x")})
	require.Error(t, err, "nothing recorded and nothing in progress")
	assert.Equal(t, head, gitTestOutput(t, dir, "rev-parse", "HEAD"))
}

// mergeFixture has base, and two branches off it: "ours" edits line two of f.txt and
// deletes gone.txt; "theirs" edits the same line and edits gone.txt; "clean" only adds.
func mergeFixture(t *testing.T) (dir string, revs map[string]string) {
	t.Helper()
	isolateGitConfig(t)
	dir = t.TempDir()
	gitInitRepo(t, dir, map[string]string{"f.txt": "a\nb\nc\n", "gone.txt": "g\n"})
	gitRun(t, dir, "branch", "-M", "main")
	revs = map[string]string{"base": gitTestOutput(t, dir, "rev-parse", "HEAD")}
	branch := func(name string, edit func()) {
		gitRun(t, dir, "checkout", "-q", "-b", name, revs["base"])
		edit()
		gitRun(t, dir, "add", "-A")
		gitRun(t, dir, "commit", "-q", "-m", name)
		revs[name] = gitTestOutput(t, dir, "rev-parse", "HEAD")
	}
	branch("ours", func() {
		writeRepoFile(t, dir, "f.txt", "a\nOURS\nc\n")
		require.NoError(t, os.Remove(filepath.Join(dir, "gone.txt")))
	})
	branch("theirs", func() {
		writeRepoFile(t, dir, "f.txt", "a\nTHEIRS\nc\n")
		writeRepoFile(t, dir, "gone.txt", "edited\n")
	})
	branch("clean", func() { writeRepoFile(t, dir, "new.txt", "n\n") })
	return dir, revs
}

func TestMergeTreesReportsConflictKindsAsAResult(t *testing.T) {
	dir, revs := mergeFixture(t)
	g := gitVCS{}

	res, err := g.MergeTrees(t.Context(), dir, types.TreeMerge{Ours: revs["ours"], Theirs: revs["theirs"]})
	require.NoError(t, err, "a conflict is a result, not an error")
	assert.NotEmpty(t, res.Tree)
	assert.Equal(t, []types.Conflict{
		{Path: "f.txt", Kind: types.ConflictKindContent},
		{Path: "gone.txt", Kind: types.ConflictKindDeleted},
	}, res.Conflicts)

	clean, err := g.MergeTrees(t.Context(), dir, types.TreeMerge{Ours: revs["ours"], Theirs: revs["clean"]})
	require.NoError(t, err)
	assert.Empty(t, clean.Conflicts)
	changed, err := g.DiffTrees(t.Context(), dir, revs["ours"], clean.Tree)
	require.NoError(t, err)
	assert.Equal(t, []string{"new.txt"}, changed)

	_, err = g.MergeTrees(t.Context(), dir, types.TreeMerge{Ours: revs["ours"], Theirs: "no-such-rev"})
	require.Error(t, err)
}

// B1: a change stacked on another deletes a line its parent added. Predicting the
// child's merge from the plan's base brings the line back; from the commit the child was
// built on, it stays deleted, as validated.
func TestMergeTreesWithAnExplicitBaseMergesOnlyWhatTheirsAdded(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"src.txt": "one\n"})
	g := gitVCS{}
	ctx := t.Context()
	commit := func(content, msg string) string {
		writeRepoFile(t, dir, "src.txt", content)
		gitRun(t, dir, "commit", "-q", "-am", msg)
		return gitTestOutput(t, dir, "rev-parse", "HEAD")
	}
	planBase := gitTestOutput(t, dir, "rev-parse", "HEAD")
	onto := commit("one\nL\n", "A adds L") // B's stage is built onto A
	stage := commit("one\n", "B deletes L")
	gitRun(t, dir, "checkout", "-q", planBase)
	tip := commit("one\nL\n", "A squash-merged") // the base branch once A lands

	wrong, err := g.MergeTrees(ctx, dir, types.TreeMerge{Base: planBase, Ours: tip, Theirs: stage})
	require.NoError(t, err)
	right, err := g.MergeTrees(ctx, dir, types.TreeMerge{Base: onto, Ours: tip, Theirs: stage})
	require.NoError(t, err)

	validated, err := g.TreeID(ctx, dir, stage)
	require.NoError(t, err)
	assert.NotEqual(t, validated, wrong.Tree, "the plan base resurrects L, which is the bug")
	assert.Equal(t, validated, right.Tree, "merged from onto, the prediction is the validated tree")
}

func TestCommitTreeIsDeterministicAndCreatesNoRef(t *testing.T) {
	dir, revs := mergeFixture(t)
	g := gitVCS{}
	ctx := t.Context()
	tree, err := g.TreeID(ctx, dir, revs["clean"])
	require.NoError(t, err)
	before := refsOf(t, dir)
	c := types.TreeCommit{
		CommitMeta: types.CommitMeta{
			Message: "update", Author: types.Person{Name: "author", Email: "a@x"}, Committer: queueIdentity,
			Date: time.Unix(946684800, 0).UTC(),
		},
		Tree: tree, Parents: []string{revs["ours"], revs["clean"]},
	}
	first, err := g.CommitTree(ctx, dir, c)
	require.NoError(t, err)
	second, err := g.CommitTree(ctx, dir, c)
	require.NoError(t, err)
	assert.Equal(t, first, second, "the same inputs yield the same commit id")
	assert.Equal(t, before, refsOf(t, dir))
	assert.Equal(t, revs["ours"]+" "+revs["clean"], gitTestOutput(t, dir, "log", "-1", "--format=%P", first))
	assert.Equal(t, "author queue", gitTestOutput(t, dir, "log", "-1", "--format=%an %cn", first))

	c.Committer = types.Person{}
	_, err = g.CommitTree(ctx, dir, c)
	require.ErrorContains(t, err, "committer")
	c.Committer, c.Tree = queueIdentity, "HEAD^{tree}"
	_, err = g.CommitTree(ctx, dir, c)
	require.Error(t, err, "the tree is an id, not an expression")
}

// A zero Date is the time of the call for both dates. An environment carrying
// GIT_AUTHOR_DATE, as a hook or a rebase exports it, must not date the commit instead.
func TestCommitTreeZeroDateIsNowNotTheEnvironments(t *testing.T) {
	dir, revs := mergeFixture(t)
	t.Setenv("GIT_AUTHOR_DATE", "@978307200 +0000")
	t.Setenv("GIT_COMMITTER_DATE", "@978307200 +0000")
	tree, err := gitVCS{}.TreeID(t.Context(), dir, revs["clean"])
	require.NoError(t, err)

	before := time.Now().Add(-time.Minute).Unix()
	id, err := gitVCS{}.CommitTree(t.Context(), dir, types.TreeCommit{CommitMeta: queueMeta("now"), Tree: tree})
	require.NoError(t, err)
	dates := strings.Fields(gitTestOutput(t, dir, "log", "-1", "--format=%at %ct", id))
	require.Len(t, dates, 2)
	assert.Equal(t, dates[0], dates[1], "author and committer dates differ")
	at, err := strconv.ParseInt(dates[0], 10, 64)
	require.NoError(t, err)
	assert.Greater(t, at, before, "the commit took its date from the environment")
}

// A refs/replace/ ref would substitute one commit for another under every read.
func TestTreeIDIgnoresReplaceRefs(t *testing.T) {
	dir, revs := mergeFixture(t)
	gitRun(t, dir, "replace", revs["ours"], revs["theirs"])
	want := gitTestOutput(t, dir, "--no-replace-objects", "rev-parse", revs["ours"]+"^{tree}")

	got, err := gitVCS{}.TreeID(t.Context(), dir, revs["ours"])
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// B9: plumbing, so diff.renames cannot fold a rename into its new path alone.
func TestRangeFilesReportsBothSidesOfARename(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"old.txt": "content that survives the move\n"})
	gitRun(t, dir, "config", "diff.renames", "true")
	base := gitTestOutput(t, dir, "rev-parse", "HEAD")
	gitRun(t, dir, "mv", "old.txt", "new.txt")
	gitRun(t, dir, "commit", "-q", "-m", "move")

	got, err := gitVCS{}.RangeFiles(t.Context(), dir, base, "HEAD", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"new.txt", "old.txt"}, got)
}

// The affected set: a rename's old path belongs to the project it left, so ChangedFiles and
// BranchChanges must report it under diff.renames (git's default) too.
func TestChangedFilesAndBranchChangesReportBothSidesOfARename(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a/moved.txt": "content that survives the move\n"})
	gitRun(t, dir, "config", "diff.renames", "true")
	gitRun(t, dir, "branch", "-M", "main")
	gitRun(t, dir, "checkout", "-q", "-b", "move")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "b"), 0o755))
	gitRun(t, dir, "mv", "a/moved.txt", "b/moved.txt")
	gitRun(t, dir, "commit", "-q", "-m", "move")

	changed, err := gitVCS{}.ChangedFiles(t.Context(), dir, "main")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a/moved.txt", "b/moved.txt"}, changed)

	gitRun(t, dir, "checkout", "-q", "main")
	branches, err := gitVCS{}.BranchChanges(t.Context(), dir, "main", 5)
	require.NoError(t, err)
	require.Len(t, branches, 1)
	assert.ElementsMatch(t, []string{"a/moved.txt", "b/moved.txt"}, branches[0].Paths)
}

func TestRangeFilesAndRangeCommitsNarrowToPaths(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"keep/a.txt": "a\n", "drop.txt": "d\n"})
	base := gitTestOutput(t, dir, "rev-parse", "HEAD")
	writeRepoFile(t, dir, "keep/a.txt", "a2\n")
	gitRun(t, dir, "commit", "-q", "-am", "keep")
	writeRepoFile(t, dir, "drop.txt", "d2\n")
	gitRun(t, dir, "commit", "-q", "-am", "drop")

	files, err := gitVCS{}.RangeFiles(t.Context(), filepath.Join(dir, "keep"), base, "HEAD", []string{"keep"})
	require.NoError(t, err)
	assert.Equal(t, []string{"keep/a.txt"}, files, "paths are repository-relative wherever dir is")

	_, err = gitVCS{}.RangeCommits(t.Context(), dir, "", "HEAD", nil)
	require.Error(t, err, "an empty base is refused, not read as the whole history")
}

// B12: the mark comes from the revision's tree only. The working copy's attributes and a
// global attributes file do not count, and $GIT_DIR/info/attributes, which git cannot be
// told to skip, is an error.
func TestGeneratedPathsReadsTheRevisionsMarkOnly(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{".gitattributes": "gen/** linguist-generated\n", "gen/a.txt": "a\n", "src.go": "x\n"})
	rev := gitTestOutput(t, dir, "rev-parse", "HEAD")
	writeRepoFile(t, dir, ".gitattributes", "src.go linguist-generated\n")
	global := filepath.Join(t.TempDir(), "attributes")
	require.NoError(t, os.WriteFile(global, []byte("src.go linguist-generated\n"), 0o644))
	gitRun(t, dir, "config", "core.attributesFile", global)

	got, err := gitVCS{}.GeneratedPaths(t.Context(), dir, rev, []string{"gen/a.txt", "src.go", "gen/new.txt"})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"gen/a.txt": true, "gen/new.txt": true}, got)

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git", "info"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "info", "attributes"), []byte("src.go linguist-generated\n"), 0o644))
	_, err = gitVCS{}.GeneratedPaths(t.Context(), dir, rev, []string{"src.go"})
	require.ErrorContains(t, err, "overrides the revision's attributes")
}

func TestCheckoutLifecycle(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "a\n"})
	g := gitVCS{}
	ctx := t.Context()
	co := filepath.Join(t.TempDir(), "co")

	require.Error(t, g.CreateCheckout(ctx, dir, filepath.Join(dir, "inside"), "HEAD"), "inside root, discovery would index it")
	require.Error(t, g.CreateCheckout(ctx, dir, "relative", "HEAD"))
	require.NoError(t, g.CreateCheckout(ctx, dir, co, "HEAD"))
	require.Error(t, g.CreateCheckout(ctx, dir, co, "HEAD"), "an existing directory is not replaced")

	listed, err := g.Checkouts(ctx, dir)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	want, err := filepath.EvalSymlinks(co)
	require.NoError(t, err)
	got, err := filepath.EvalSymlinks(listed[0])
	require.NoError(t, err)
	assert.Equal(t, want, got)

	require.NoError(t, os.RemoveAll(co))
	require.NoError(t, g.RemoveCheckout(ctx, dir, co), "a checkout whose directory is gone still unregisters")
	listed, err = g.Checkouts(ctx, dir)
	require.NoError(t, err)
	assert.Empty(t, listed)
}

// RemoveCheckout touches only what CreateCheckout made: a worktree the user added by hand
// is neither listed nor removable through it.
func TestRemoveCheckoutRefusesACheckoutItDidNotCreate(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "a\n"})
	mine := filepath.Join(t.TempDir(), "mine")
	gitRun(t, dir, "worktree", "add", "-q", "--detach", mine, "HEAD")

	listed, err := gitVCS{}.Checkouts(t.Context(), dir)
	require.NoError(t, err)
	assert.Empty(t, listed)
	require.ErrorContains(t, gitVCS{}.RemoveCheckout(t.Context(), dir, mine), "not a checkout CreateCheckout made")
	assert.DirExists(t, mine)
}

// A checkout reached through a symlink that lands inside the repository is inside it.
func TestCreateCheckoutResolvesSymlinksBeforeRefusingAPathInside(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "a\n"})
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(dir, link))

	err := gitVCS{}.CreateCheckout(t.Context(), dir, filepath.Join(link, "inside"), "HEAD")
	require.ErrorContains(t, err, "lies inside")
}

// StartMerge in a linked checkout, where .git is a file: the conflict must still read as
// a merge in progress.
func TestStartMergeInALinkedCheckoutReportsConflicts(t *testing.T) {
	dir, revs := mergeFixture(t)
	g := gitVCS{}
	co := filepath.Join(t.TempDir(), "co")
	require.NoError(t, g.CreateCheckout(t.Context(), dir, co, revs["ours"]))

	require.NoError(t, g.StartMerge(t.Context(), co, revs["theirs"]))
	conflicts, err := g.Conflicts(t.Context(), co)
	require.NoError(t, err)
	assert.NotEmpty(t, conflicts)
}

// A merge already underway is not the one StartMerge was asked for. Before, git refused
// the second merge, and the first merge's MERGE_HEAD made that refusal read as success.
func TestStartMergeRefusesWhenAMergeIsAlreadyUnderway(t *testing.T) {
	dir, revs := mergeFixture(t)
	g := gitVCS{}
	gitRun(t, dir, "checkout", "-q", revs["ours"])
	require.NoError(t, g.StartMerge(t.Context(), dir, revs["theirs"]))

	err := g.StartMerge(t.Context(), dir, revs["clean"])
	require.ErrorContains(t, err, "already in progress")
	assert.Equal(t, revs["theirs"], gitTestOutput(t, dir, "rev-parse", "MERGE_HEAD"))
}

func TestBundleCarriesCommitsWithoutRefs(t *testing.T) {
	f := newRemoteFixture(t)
	g := gitVCS{}
	ctx := t.Context()
	base := gitTestOutput(t, f.clone, "rev-parse", "HEAD")
	gitRun(t, f.clone, "commit", "-q", "--allow-empty", "-m", "carried")
	carried := gitTestOutput(t, f.clone, "rev-parse", "HEAD")
	file := filepath.Join(t.TempDir(), "stage.bundle")

	require.NoError(t, g.Bundle(ctx, f.clone, file, types.BundleRange{Base: base, Head: carried}))
	assert.NotContains(t, refsOf(t, f.clone), "refs/magus/")

	other := t.TempDir()
	gitRun(t, other, "clone", "-q", "file://"+f.remote, ".")
	before := refsOf(t, other)
	require.NoError(t, g.Unbundle(ctx, other, file))
	gitRun(t, other, "cat-file", "-e", carried+"^{commit}")
	assert.Equal(t, before, refsOf(t, other), "unbundling created a ref")

	whole := filepath.Join(t.TempDir(), "whole.bundle")
	require.NoError(t, g.Bundle(ctx, f.clone, whole, types.BundleRange{Head: carried}), "an empty Base bundles the whole history")
	empty := t.TempDir()
	gitRun(t, empty, "init", "-q")
	require.NoError(t, g.Unbundle(ctx, empty, whole))
	gitRun(t, empty, "cat-file", "-e", carried+"^{commit}")
}

// A scratch ref a crashed call left behind is swept once it is old; a fresh one, which a
// concurrent call may still be using, is not.
func TestFetchSweepsOnlyStaleScratchRefs(t *testing.T) {
	f := newRemoteFixture(t)
	id := gitTestOutput(t, f.clone, "rev-parse", "HEAD")
	stale := fmt.Sprintf("refs/magus/fetch/%d-1-aa", time.Now().Add(-2*scratchRetention).Unix())
	fresh := fmt.Sprintf("refs/magus/fetch/%d-1-bb", time.Now().Unix())
	gitRun(t, f.clone, "update-ref", stale, id)
	gitRun(t, f.clone, "update-ref", fresh, id)

	_, err := gitVCS{}.FetchRef(t.Context(), f.clone, "origin", "refs/heads/main")
	require.NoError(t, err)
	refs := refsOf(t, f.clone)
	assert.NotContains(t, refs, stale)
	assert.Contains(t, refs, fresh)
}

// B13: an exported GIT_DIR, which git exports into every hook, must not send magus's own
// git calls into another repository, the fetch recovery included.
func TestGitCallsIgnoreAHostileGitDir(t *testing.T) {
	origin, _ := gitDivergedOrigin(t, 40)
	clone := gitCloneShallow(t, origin, 1)
	elsewhere := t.TempDir()
	gitInitRepo(t, elsewhere, map[string]string{"x.txt": "x\n"})
	before := refsOf(t, elsewhere)

	t.Run("hostile environment", func(t *testing.T) {
		t.Setenv("GIT_DIR", filepath.Join(elsewhere, ".git"))
		t.Setenv("GIT_WORK_TREE", elsewhere)
		t.Setenv("GIT_INDEX_FILE", filepath.Join(elsewhere, ".git", "index"))

		// ChangedFiles deepens the shallow clone, which fetches a remote-tracking ref.
		files, err := gitVCS{}.ChangedFiles(t.Context(), clone, "origin/main")
		require.NoError(t, err)
		assert.Contains(t, files, "app.txt")
		_, err = gitVCS{}.Commit(t.Context(), clone, types.CheckoutCommit{CommitMeta: queueMeta("x")})
		require.Error(t, err, "the clone has nothing to commit; succeeding means another repository was the target")
	})

	assert.Equal(t, before, refsOf(t, elsewhere), "a magus git call wrote into the repository GIT_DIR named")
	assert.NotEmpty(t, gitTestOutput(t, clone, "for-each-ref", "refs/remotes/origin/main"), "the fetch landed somewhere other than the clone")
}

// The hardening every call gets, pinned on the command gitExec builds.
func TestGitExecPinsTheSettingsItsParsersDependOn(t *testing.T) {
	t.Setenv("GIT_REPLACE_REF_BASE", "refs/elsewhere/")
	t.Setenv("GIT_ATTR_SOURCE", "HEAD~1")
	t.Setenv("GIT_GLOB_PATHSPECS", "1")
	t.Setenv("GIT_SHALLOW_FILE", "/elsewhere")
	cmd := gitExec(t.Context(), "/repo", gitOpts{Isolated: true, Literal: true}, "status")

	joined := strings.Join(cmd.Args, " ")
	for _, pin := range []string{"color.ui=false", "color.diff=false", "color.status=false", "core.quotePath=false",
		"log.showSignature=false", "diff.noprefix=false", "diff.mnemonicPrefix=false",
		"rerere.enabled=false", "commit.gpgSign=false", "push.gpgSign=false", "core.hooksPath=" + os.DevNull} {
		assert.Contains(t, joined, pin)
	}
	// The user's own checkout keeps its rerere, signing and hooks.
	plain := strings.Join(gitExec(t.Context(), "/repo", gitOpts{}, "merge").Args, " ")
	for _, pin := range []string{"rerere.enabled", "gpgSign", "core.hooksPath"} {
		assert.NotContains(t, plain, pin)
	}
	env := strings.Join(cmd.Env, "\n")
	for _, set := range []string{"GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_LITERAL_PATHSPECS=1"} {
		assert.Contains(t, cmd.Env, set)
	}
	for _, gone := range []string{"GIT_REPLACE_REF_BASE=", "GIT_ATTR_SOURCE=", "GIT_GLOB_PATHSPECS=", "GIT_SHALLOW_FILE="} {
		assert.NotContains(t, env, gone)
	}
	assert.Equal(t, gitWaitDelay, cmd.WaitDelay)
	assert.Equal(t, "/repo", cmd.Dir)
}

func TestCheckRefNameAndCommitID(t *testing.T) {
	for _, ok := range []string{"refs/heads/main", "refs/pull/482/head", "refs/heads/rel/1.0"} {
		assert.NoError(t, checkRefName(ok), ok)
	}
	for _, bad := range []string{"main", "refs/", "refs/heads/a..b", "refs/heads/x.lock", "refs/heads/.x",
		"refs/heads/a:b", "+refs/heads/a", "refs/heads/*", "refs/heads/a b", "refs/heads/a^", "refs/heads/a~1",
		"refs/heads/a@{1}", "refs/heads/a/", "refs/heads//a", "refs/heads/a\\b"} {
		assert.Error(t, checkRefName(bad), bad)
	}
	assert.NoError(t, checkCommitID(strings.Repeat("a", 40)))
	assert.NoError(t, checkCommitID(strings.Repeat("0", 64)))
	for _, bad := range []string{"", "HEAD", strings.Repeat("A", 40), strings.Repeat("a", 39)} {
		assert.Error(t, checkCommitID(bad), bad)
	}
	for _, bad := range []string{"", "-x", ".", "a/b", "https://x", "a b", "a:b"} {
		assert.Error(t, checkRemoteName(bad), bad)
	}
	assert.NoError(t, checkRemoteName("upstream"))
	assert.Error(t, checkRev("+refs/heads/*:refs/heads/*"))
	assert.Error(t, checkRev("HEAD:path"))
	assert.NoError(t, checkRev("origin/main~2"))
}

// `bisect run` runs the user's test command, which inherits whatever git exports. magus's
// pins must not reach it: a test that runs git itself would see magus's config, not its own.
func TestBisectRunCarriesNoMagusPins(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "0\n"})
	good := gitTestOutput(t, dir, "rev-parse", "HEAD")
	for i := 1; i <= 2; i++ {
		writeRepoFile(t, dir, "a.txt", fmt.Sprintf("%d\n", i))
		gitRun(t, dir, "commit", "-q", "-am", fmt.Sprintf("c%d", i))
	}
	dump := filepath.Join(t.TempDir(), "env")

	_, err := gitVCS{}.Bisect(t.Context(), dir, types.BisectOptions{Good: good, TestCmd: "env >> " + dump + "; exit 1"})
	require.NoError(t, err)
	env, err := os.ReadFile(dump)
	require.NoError(t, err)
	for _, pin := range []string{"color.ui", "GIT_NO_REPLACE_OBJECTS", "GIT_TERMINAL_PROMPT"} {
		assert.NotContains(t, string(env), pin)
	}
}

// The deepening fetch magus makes unasked runs no hook of the user's.
func TestShallowRecoveryRunsNoHook(t *testing.T) {
	origin, _ := gitDivergedOrigin(t, 40)
	clone := gitCloneShallow(t, origin, 1)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hook := fmt.Sprintf("#!/bin/sh\necho ran >> %q\n", marker)
	require.NoError(t, os.WriteFile(filepath.Join(clone, ".git", "hooks", "reference-transaction"), []byte(hook), 0o755))

	_, err := gitVCS{}.ChangedFiles(t.Context(), clone, "origin/main")
	require.NoError(t, err)
	_, statErr := os.Stat(marker)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "the recovery fetch ran the reference-transaction hook")
}

// The box's display settings must not change what magus parses: untracked files hidden by
// status.showUntrackedFiles, a diff produced by an external driver, prefixes dropped by
// diff.noprefix, and color forced on.
func TestStatusAndDiffIgnoreDisplayConfig(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "one\n"})
	for _, kv := range [][2]string{
		{"status.showUntrackedFiles", "no"}, {"diff.external", "false"}, {"diff.noprefix", "true"},
		{"color.diff", "always"}, {"color.status", "always"},
	} {
		gitRun(t, dir, "config", kv[0], kv[1])
	}
	writeRepoFile(t, dir, "new.txt", "new\n")
	writeRepoFile(t, dir, "a.txt", "two\n")
	g := gitVCS{}

	files, err := g.DirtyFiles(t.Context(), dir, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a.txt", "new.txt"}, files)
	meta, err := g.Metadata(t.Context(), dir)
	require.NoError(t, err)
	assert.True(t, meta.IsDirty)

	diff, err := g.DirtyDiff(t.Context(), dir, nil)
	require.NoError(t, err)
	assert.Contains(t, diff, "diff --git a/a.txt b/a.txt")
	assert.NotContains(t, diff, "\x1b[")
}

// Asking whether a backend installs a merge driver reads nothing and writes nothing.
func TestInstallsMergeDriverAsksWithoutEnsuring(t *testing.T) {
	probe := &ensureRecorder{VCSDriver: gitVCS{}}
	assert.True(t, installsMergeDriver(probe))
	assert.False(t, probe.ensured, "the probe called EnsureMergeDriver, which may write")
	assert.False(t, installsMergeDriver(jjVCS{}))
}

type ensureRecorder struct {
	types.VCSDriver
	ensured bool
}

func (r *ensureRecorder) EnsureMergeDriver(context.Context, string, []string) (bool, error) {
	r.ensured = true
	return false, nil
}
