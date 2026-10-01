package vcs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkpointRepo makes a throwaway git repo on a named branch with one committed file,
// and returns its dir plus the resolution Checkpoint takes.
func checkpointRepo(t *testing.T) (string, types.VCSResolution) {
	t.Helper()
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "alpha\n"})
	// Named explicitly: git's default branch depends on the version and on an
	// init.defaultBranch the fixture env deliberately cannot see, so asserting on
	// Branch needs a name this test chose.
	gitRun(t, dir, "checkout", "-q", "-b", "work")
	return dir, types.VCSResolution{Name: "git", VCS: gitVCS{}}
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644))
}

// TestCheckpointCleanTree pins the whole record for the ordinary case: a resolved
// revision, the branch carrying it, not dirty, and NO digest: the digest is the one
// field whose absence is meaningful, since a clean tree has no patch to fingerprint.
func TestCheckpointCleanTree(t *testing.T) {
	dir, res := checkpointRepo(t)
	ctx := context.Background()

	cp, err := Checkpoint(ctx, dir, res, false)
	require.NoError(t, err)

	head, err := gitOutput(ctx, dir, gitOpts{}, "rev-parse", "HEAD")
	require.NoError(t, err)
	assert.Equal(t, head, cp.Revision, "revision must be the full head id, feedable back to a VCS")
	assert.Equal(t, "work", cp.Branch)
	assert.False(t, cp.Dirty)
	assert.Empty(t, cp.PatchDigest, "a clean tree has no patch, so no digest")
	assert.Equal(t, "git", cp.VCS)
}

// TestCheckpointDirtyTree covers the properties a ledger actually relies on: the digest
// exists, is the agreed width, does not move when nothing moved, and DOES move when the
// patch does. The last two are the whole point: a digest that drifted on its own could
// not answer "did these two workers see the same tree", and one that did not change with
// the patch would answer it wrong.
func TestCheckpointDirtyTree(t *testing.T) {
	dir, res := checkpointRepo(t)
	ctx := context.Background()

	writeFile(t, dir, "a.txt", "alpha changed\n")
	first, err := Checkpoint(ctx, dir, res, false)
	require.NoError(t, err)
	assert.True(t, first.Dirty)
	assert.Len(t, first.PatchDigest, 2*patchDigestBytes)
	assert.Regexp(t, `^[0-9a-f]+$`, first.PatchDigest, "digest is lowercase hex")

	again, err := Checkpoint(ctx, dir, res, false)
	require.NoError(t, err)
	assert.Equal(t, first, again, "the same tree, read twice, is the same checkpoint")

	writeFile(t, dir, "a.txt", "alpha changed differently\n")
	moved, err := Checkpoint(ctx, dir, res, false)
	require.NoError(t, err)
	assert.Equal(t, first.Revision, moved.Revision, "an uncommitted edit does not move the revision")
	assert.NotEqual(t, first.PatchDigest, moved.PatchDigest, "a different patch is a different digest")
}

// TestCheckpointRecordsWhatTheBackendReports pins the two places the record is a
// PASS-THROUGH rather than a judgement, because both look like bugs to a later reader.
//
// Detached HEAD: the record carries whatever Metadata reports, which is "" for a
// checkout with no branch. Normalizing here would be git knowledge inside a
// backend-agnostic function; gitVCS.Metadata owns it, where every caller sees it.
//
// Untracked-only: the tree is dirty (status --porcelain sees the file) while the patch
// is empty (diff does not), so the digest is the empty patch's. Two trees with different
// untracked files therefore share a digest; see VCSCheckpoint.PatchDigest.
func TestCheckpointRecordsWhatTheBackendReports(t *testing.T) {
	dir, res := checkpointRepo(t)
	ctx := context.Background()

	writeFile(t, dir, "residue.txt", "build output\n")
	untracked, err := Checkpoint(ctx, dir, res, false)
	require.NoError(t, err)
	assert.True(t, untracked.Dirty, "an untracked file makes the tree dirty")
	assert.Equal(t, patchDigest(""), untracked.PatchDigest, "no tracked change means the empty patch's digest")

	require.NoError(t, os.Remove(filepath.Join(dir, "residue.txt")))
	gitRun(t, dir, "checkout", "-q", "--detach")
	detached, err := Checkpoint(ctx, dir, res, false)
	require.NoError(t, err)
	assert.Empty(t, detached.Branch, "a detached head has no branch")
	assert.False(t, detached.Dirty)
}

func TestCheckpointWithoutAResolvedVCS(t *testing.T) {
	_, err := Checkpoint(context.Background(), t.TempDir(), types.VCSResolution{Name: "git"}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no VCS resolved")
}

// TestPatchDigest is the golden vector. The value is the first 16 BYTES of
// sha256("--- a/x\n+++ b/x\n") in hex (32 characters, the exact shape
// internal/changeset.PatchDigest produces), computed independently of this package, so a
// change to the algorithm (a different hash, a different width, hashing something
// other than the raw patch text) fails here rather than silently producing
// identities that no longer match the review session's.
func TestPatchDigest(t *testing.T) {
	for _, tc := range []struct {
		name  string
		patch string
		want  string
	}{
		{"fixed patch text", "--- a/x\n+++ b/x\n", "922dd52a81ff1c3d456cb861de7ad959"},
		{"empty patch", "", "e3b0c44298fc1c149afbf4c8996fb924"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, patchDigest(tc.patch))
			assert.Len(t, patchDigest(tc.patch), 2*patchDigestBytes)
		})
	}
}

// A tree holding only untracked files is dirty with an EMPTY tracked patch, so a token cut
// from PatchDigest alone named it by the digest of nothing: the same token every
// untracked-only tree on that revision produced. The untracked files are the patch there.
func TestCheckpointTokenCoversUntrackedFiles(t *testing.T) {
	emptyPatch := patchDigest("")
	ctx := context.Background()

	t.Run("untracked only", func(t *testing.T) {
		dir, res := checkpointRepo(t)
		clean, err := Checkpoint(ctx, dir, res, false)
		require.NoError(t, err)
		require.Equal(t, clean.Revision, CheckpointToken(clean), "a clean tree is its revision")

		writeFile(t, dir, "new.txt", "fresh\n")
		first, err := Checkpoint(ctx, dir, res, false)
		require.NoError(t, err)
		require.True(t, first.Dirty)
		require.Equal(t, emptyPatch, first.PatchDigest, "no tracked file moved")
		token := CheckpointToken(first)
		assert.NotEqual(t, first.Revision+"+"+emptyPatch, token)
		assert.Regexp(t, `^`+first.Revision+`\+[0-9a-f]{32}$`, token)

		writeFile(t, dir, "new.txt", "edited\n")
		edited, err := Checkpoint(ctx, dir, res, false)
		require.NoError(t, err)
		assert.NotEqual(t, token, CheckpointToken(edited), "an edit to an untracked file is a different tree")
	})

	t.Run("tracked edits plus untracked", func(t *testing.T) {
		dir, res := checkpointRepo(t)
		writeFile(t, dir, "a.txt", "alpha changed\n")
		tracked, err := Checkpoint(ctx, dir, res, false)
		require.NoError(t, err)
		require.Empty(t, tracked.UntrackedDigest)
		assert.Equal(t, tracked.Revision+"+"+tracked.PatchDigest, CheckpointToken(tracked),
			"a tracked-only token stays the review session's patch digest")

		writeFile(t, dir, "new.txt", "fresh\n")
		both, err := Checkpoint(ctx, dir, res, false)
		require.NoError(t, err)
		assert.Equal(t, tracked.PatchDigest, both.PatchDigest, "the tracked patch did not move")
		assert.NotEqual(t, CheckpointToken(tracked), CheckpointToken(both), "the untracked file is part of the tree")
	})
}

// The preserve flag is the only thing about a checkpoint that MINTS, so both sides are
// asserted: off leaves no handle and no ref, on gives a handle that resolves to the
// untracked file, which is why a digest alone is not enough.
func TestCheckpointPreservesOnlyWhenAsked(t *testing.T) {
	dir, res := checkpointRepo(t)
	ctx := context.Background()
	writeFile(t, dir, "tracked.txt", "v2\n")
	writeFile(t, dir, "scratch.txt", "unfinished\n")

	plain, err := Checkpoint(ctx, dir, res, false)
	require.NoError(t, err)
	assert.Empty(t, plain.Preserved, "a checkpoint minted a handle nobody asked for")
	refs, err := gitOutput(ctx, dir, gitOpts{}, "for-each-ref", "refs/magus/preserved/")
	require.NoError(t, err)
	assert.Empty(t, refs, "a checkpoint that preserved nothing still anchored a ref")

	kept, err := Checkpoint(ctx, dir, res, true)
	require.NoError(t, err)
	require.NotEmpty(t, kept.Preserved, "preserve was asked for and produced no handle")
	held, err := gitOutput(ctx, dir, gitOpts{}, "show", kept.Preserved+":scratch.txt")
	require.NoError(t, err, "the handle does not resolve")
	// gitOutput trims, so the stored newline is not in the comparison.
	assert.Equal(t, "unfinished", held, "the handle does not hold the untracked work")

	// The identity half of the record is unaffected by capturing: same tree, same digests.
	assert.Equal(t, plain.PatchDigest, kept.PatchDigest)
	assert.Equal(t, plain.UntrackedDigest, kept.UntrackedDigest)
}

// preserveFailsAfterMinting is a driver whose Preserve returns a handle AND an error, the
// shape hg and sl deliberately produce: the shelf or the snapshot commit exists from that
// point on, and only the handle can reach it. Everything else is the real git driver.
type preserveFailsAfterMinting struct {
	types.VCSDriver
}

func (preserveFailsAfterMinting) Preserve(context.Context, string) (string, error) {
	return "e5e5e5e5", errors.New("recorded e5e5e5e5, but the working copy still shows [scratch.txt] added")
}

// A capture that half-succeeded has already minted an object in the user's repository, so
// returning a zero checkpoint alongside the error throws away the only thing that reaches
// it, turning a recoverable failure into an orphan.
func TestCheckpointKeepsTheHandleWhenPreservingFails(t *testing.T) {
	dir, res := checkpointRepo(t)
	writeFile(t, dir, "scratch.txt", "unfinished\n")
	res.VCS = preserveFailsAfterMinting{VCSDriver: res.VCS}

	cp, err := Checkpoint(context.Background(), dir, res, true)

	require.Error(t, err, "a failed preserve is still a failure")
	assert.Equal(t, "e5e5e5e5", cp.Preserved, "the handle to the minted object was discarded")
	assert.NotEmpty(t, cp.Revision, "the identity the checkpoint had already resolved was discarded too")
}
