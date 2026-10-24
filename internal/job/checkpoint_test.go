package job

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// madeUpRevision is the full-length revision an orchestrator once forked a job with. No
// repository a test builds holds it.
const madeUpRevision = "34b41fd546ba8fa9c71a6a64e4ea0f3e93d5a7bc"

// committedRepo is a git repository holding loadableRoot's files in one commit, and that
// commit's full revision.
func committedRepo(t *testing.T) (root, head string) {
	t.Helper()
	repo := testkit.NewRepo(t, loadableFiles)
	return repo.Root(), repo.Head()
}

func checkpointMerge(token string) func(*types.Job) {
	return func(u *types.Job) {
		u.Check, u.State, u.WritePaths, u.Checkpoint = forkCheck(), types.StateDeclared, []string{"internal/job/store.go"}, token
	}
}

func checkpointRecord(id, token string) types.Declaration {
	return types.Declaration{ID: id, WritePaths: []string{"internal/job/store.go"}, Check: forkCheck(), Checkpoint: token}
}

// The defect: a fork named a full-length revision the repository never held, and nothing
// checked it until `job exec` reported a diverged base. Every door resolves it first.
func TestAMadeUpCheckpointIsRefusedAtForkAndAtApply(t *testing.T) {
	t.Parallel()

	root, _ := committedRepo(t)
	s := NewStore(tempLocation(t, root))

	_, err := ForkMerge(t.Context(), s, "forked", checkpointMerge(madeUpRevision), config.Jobs{}, nil)
	require.Error(t, err, "magus\\job.put stored a revision the repository does not hold")
	assert.Contains(t, err.Error(), madeUpRevision)
	assert.Contains(t, err.Error(), "`magus vcs checkpoint -o name`")

	_, err = Apply(t.Context(), s, []types.Declaration{checkpointRecord("applied", madeUpRevision)}, config.Jobs{}, nil, nil, false)
	require.Error(t, err, "magus job apply stored a revision the repository does not hold")
	assert.Contains(t, err.Error(), madeUpRevision)
	assert.Contains(t, err.Error(), "`magus vcs checkpoint -o name`")

	rows, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, rows, "a refused checkpoint writes no row")
}

// A real revision is stored as the full one it abbreviates, so the row compares with a
// base exec reads from a checkout. A dirty token on an older revision keeps its digest:
// the patch behind it is gone, so nothing can check it.
func TestARealAbbreviatedCheckpointIsStoredExpanded(t *testing.T) {
	t.Parallel()

	repo := testkit.NewRepo(t, loadableFiles)
	older := repo.Head()
	repo.Commit("later", map[string]string{"later.txt": "later\n"})
	head := repo.Head()
	s := NewStore(tempLocation(t, repo.Root()))
	digest := strings.Repeat("0123456789abcdef", 2)

	forked, err := ForkMerge(t.Context(), s, "forked", checkpointMerge(fmt.Sprintf("%s+%s", older[:9], digest)), config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("%s+%s", older, digest), forked.Checkpoint)

	applied, err := Apply(t.Context(), s, []types.Declaration{checkpointRecord("applied", head[:9])}, config.Jobs{}, nil, nil, false)
	require.NoError(t, err)
	assert.Equal(t, head, applied[0].Next.Checkpoint)
}

// A real commit the checkout's HEAD does not descend from names a tree no work here
// started from, which is the made-up checkpoint's subtler twin: it resolves, and widens
// the diff a job is graded against to other work's commits.
func TestACheckpointOffTheCheckoutsHistoryIsRefused(t *testing.T) {
	t.Parallel()

	repo := testkit.NewRepo(t, loadableFiles)
	repo.Git("switch", "-q", "-c", "elsewhere")
	elsewhere := repo.Commit("elsewhere", map[string]string{"elsewhere.txt": "x\n"})
	repo.Git("switch", "-q", "main")
	s := NewStore(tempLocation(t, repo.Root()))

	_, err := ForkMerge(t.Context(), s, "forked", checkpointMerge(elsewhere), config.Jobs{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nor an ancestor")

	_, err = Apply(t.Context(), s, []types.Declaration{checkpointRecord("applied", elsewhere[:9])}, config.Jobs{}, nil, nil, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nor an ancestor")

	rows, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, rows)
}

// A job forked into a checkout of its own starts from its checkpoint there, so a sibling
// branch the forker's HEAD does not descend from is placeable; only a revision the
// repository does not hold is not (MGS3041).
func TestACheckpointOnASiblingBranchIsPlaceableForItsOwnCheckout(t *testing.T) {
	t.Parallel()

	repo := testkit.NewRepo(t, loadableFiles)
	repo.Git("switch", "-q", "-c", "elsewhere")
	elsewhere := repo.Commit("elsewhere", map[string]string{"elsewhere.txt": "x\n"})
	repo.Git("switch", "-q", "main")
	s := NewStore(tempLocation(t, repo.Root()))
	own := func(token string) func(*types.Job) {
		return func(u *types.Job) {
			checkpointMerge(token)(u)
			u.Worktree = &types.JobWorktree{Root: filepath.Join(t.TempDir(), "own")}
		}
	}

	v, err := ValidateFork(t.Context(), s, "sibling", own(elsewhere[:9]), config.Jobs{}, nil)
	require.NoError(t, err)
	assert.NotContains(t, codes(v.Problems), types.JobCheckpointUnplaceable, "a sibling branch is where its own checkout starts: %v", v.Problems)
	assert.Equal(t, elsewhere, v.Job.Checkpoint, "it is stored expanded")

	v, err = ValidateFork(t.Context(), s, "made-up", own(madeUpRevision), config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Contains(t, codes(v.Problems), types.JobCheckpointUnplaceable)
	assert.ErrorContains(t, v.Problems.Err(), "names no revision this repository holds")
}

// At HEAD magus reproduces the digest, so a digest the tree does not hold is refused; the
// clean tree's own revision, and the dirty tree's own token, are accepted.
func TestACheckpointDigestAtHeadIsCheckedAgainstTheTree(t *testing.T) {
	t.Parallel()

	root, head := committedRepo(t)
	s := NewStore(tempLocation(t, root))
	madeUp := fmt.Sprintf("%s+%s", head, strings.Repeat("0123456789abcdef", 2))

	_, err := ForkMerge(t.Context(), s, "forked", checkpointMerge(madeUp), config.Jobs{}, nil)
	require.Error(t, err, "a clean tree reproduces no digest")
	assert.Contains(t, err.Error(), madeUp)

	require.NoError(t, os.WriteFile(filepath.Join(root, "new.go"), []byte("package new\n"), 0o644))
	_, err = Apply(t.Context(), s, []types.Declaration{checkpointRecord("applied", madeUp)}, config.Jobs{}, nil, nil, false)
	require.Error(t, err, "a dirty tree reproduces its own digest, not this one")
	assert.Contains(t, err.Error(), dirtyToken(t, root))
}

// A writing row forked by merge with no checkpoint records the checkout's, as `magus job
// fork` does, since a job with none could never be verified against its diff.
func TestAForkByMergeNamingNoCheckpointRecordsTheCheckouts(t *testing.T) {
	t.Parallel()

	root, _ := committedRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "new.go"), []byte("package new\n"), 0o644))
	s := NewStore(tempLocation(t, root))

	forked, err := ForkMerge(t.Context(), s, "forked", checkpointMerge(""), config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Equal(t, dirtyToken(t, root), forked.Checkpoint)

	reader, err := ForkMerge(t.Context(), s, "reader", func(u *types.Job) {
		u.State, u.ReadOnly = types.StateDeclared, true
	}, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Empty(t, reader.Checkpoint, "a read-only job is graded on no diff")
}

// The digest says whether two trees match, so one of the wrong shape names no tree at all.
func TestACheckpointDigestOfTheWrongShapeIsRefused(t *testing.T) {
	t.Parallel()

	root, head := committedRepo(t)
	s := NewStore(tempLocation(t, root))
	for _, digest := range []string{"xyz", strings.Repeat("ab", 8), strings.Repeat("AB", 16), ""} {
		token := fmt.Sprintf("%s+%s", head, digest)
		_, err := ForkMerge(t.Context(), s, "forked", checkpointMerge(token), config.Jobs{}, nil)
		require.Error(t, err, "digest %q", digest)
		assert.Contains(t, err.Error(), token)
	}
}

// A store with no repository behind it cannot prove a checkpoint exists, so it refuses one
// rather than recording it unchecked. A row naming none is still written.
func TestACheckpointIsRefusedWhereNoVersionControlAnswers(t *testing.T) {
	t.Parallel()

	s := NewStore(tempLocation(t, loadableRoot(t)))
	_, err := ForkMerge(t.Context(), s, "forked", checkpointMerge(madeUpRevision), config.Jobs{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), madeUpRevision)

	_, err = ForkMerge(t.Context(), s, "forked", checkpointMerge(""), config.Jobs{}, nil)
	require.NoError(t, err)
}

// The digest shape is the one vcs.Checkpoint writes: a token it mints for a dirty tree,
// untracked files and all, is accepted unchanged.
func TestADirtyCheckpointTokenFromTheTreeIsAccepted(t *testing.T) {
	t.Parallel()

	root, head := committedRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "internal", "job", "store.go"), []byte("package job\n\n// edited\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "new.go"), []byte("package new\n"), 0o644))
	s := NewStore(tempLocation(t, root))
	token := dirtyToken(t, root)
	require.True(t, strings.HasPrefix(token, head+"+"), token)

	forked, err := ForkMerge(t.Context(), s, "forked", checkpointMerge(token), config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Equal(t, token, forked.Checkpoint)
}

// dirtyToken is root's checkpoint as `magus vcs checkpoint -o name` prints it.
func dirtyToken(t *testing.T, root string) string {
	t.Helper()
	res, err := vcs.Resolve(t.Context(), root, "", types.VCSOptions{})
	require.NoError(t, err)
	cp, err := vcs.Checkpoint(t.Context(), root, res, false)
	require.NoError(t, err)
	require.True(t, cp.Dirty)
	return cp.Token()
}
