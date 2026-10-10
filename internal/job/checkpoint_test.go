package job

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/config"
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
	root = loadableRoot(t)
	return root, commitAll(t, root)
}

// commitAll makes root a git repository holding whatever root holds in one commit, and
// returns that commit's full revision.
func commitAll(t *testing.T, root string) string {
	t.Helper()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-q", "--allow-empty", "-m", "seed")
	return git("rev-parse", "HEAD")
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
	s := NewStore(tmpLoc(t, root))

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
// base exec reads from a checkout; a dirty token keeps its digest.
func TestARealAbbreviatedCheckpointIsStoredExpanded(t *testing.T) {
	t.Parallel()

	root, head := committedRepo(t)
	s := NewStore(tmpLoc(t, root))
	digest := strings.Repeat("0123456789abcdef", 2)

	forked, err := ForkMerge(t.Context(), s, "forked", checkpointMerge(head[:9]+"+"+digest), config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Equal(t, head+"+"+digest, forked.Checkpoint)

	applied, err := Apply(t.Context(), s, []types.Declaration{checkpointRecord("applied", head[:9])}, config.Jobs{}, nil, nil, false)
	require.NoError(t, err)
	assert.Equal(t, head, applied[0].Next.Checkpoint)
}

// The digest says whether two trees match, so one of the wrong shape names no tree at all.
func TestACheckpointDigestOfTheWrongShapeIsRefused(t *testing.T) {
	t.Parallel()

	root, head := committedRepo(t)
	s := NewStore(tmpLoc(t, root))
	for _, digest := range []string{"xyz", strings.Repeat("ab", 8), strings.Repeat("AB", 16), ""} {
		token := head + "+" + digest
		_, err := ForkMerge(t.Context(), s, "forked", checkpointMerge(token), config.Jobs{}, nil)
		require.Error(t, err, "digest %q", digest)
		assert.Contains(t, err.Error(), token)
	}
}

// A store with no repository behind it cannot prove a checkpoint exists, so it refuses one
// rather than recording it unchecked. A row naming none is still written.
func TestACheckpointIsRefusedWhereNoVersionControlAnswers(t *testing.T) {
	t.Parallel()

	s := NewStore(tmpLoc(t, loadableRoot(t)))
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
	s := NewStore(tmpLoc(t, root))
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
