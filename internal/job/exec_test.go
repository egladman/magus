package job

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The three tokens the verdicts are read off, in the form `magus vcs checkpoint -o name`
// prints: a clean tree is its revision, a dirty one carries the patch digest after a "+".
const (
	baseA      = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
	baseADirty = baseA + "+00112233445566778899aabbccddeeff"
	baseB      = "b0000000000000000000000000000000000000000"
)

func TestStoreExec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		checkpoint string
		reported   string
		want       types.JobBaseVerdict
		says       []string
		saysNot    []string
	}{
		{
			name:       "the same token is a match",
			checkpoint: baseA,
			reported:   baseA,
			want:       types.BaseMatch,
		},
		{
			name:       "a dirty tree on the handed revision is a revision-match, not a divergence",
			checkpoint: baseA,
			reported:   baseADirty,
			want:       types.BaseRevisionMatch,
			// Naming BOTH digests is the point: the revision agreeing is what makes this
			// confusing, so the reading has to show the half that did not.
			// "have the orchestrator commit", NOT the "Materialize the files you touch from
			// the checkpoint" this pinned until 2026-09-08. A revision-match means the same
			// commit and a DIFFERENT uncommitted patch, and a checkpoint carries only that
			// patch's digest, so there was nothing to materialize from and the advice told
			// a worker to perform an impossible recovery. The negative assertion below is
			// what keeps a restore promise from coming back.
			says:    []string{baseA, "00112233445566778899aabbccddeeff", "none (clean tree)", "have the orchestrator commit"},
			saysNot: []string{"Materialize"},
		},
		{
			name:       "a different revision is a divergence",
			checkpoint: baseA,
			reported:   baseB,
			want:       types.BaseDiverged,
			says:       []string{baseA, baseB, "Respawn from"},
		},
		{
			name:       "a lease declared without a checkpoint has nothing to compare against",
			checkpoint: "",
			reported:   baseA,
			want:       types.BaseUnknown,
			says:       []string{"magus vcs checkpoint -o name"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			s := tmpStore(t, t.TempDir())
			seed(t, s, types.Job{ID: "u1", Checkpoint: tt.checkpoint, State: types.StateDeclared})

			got, err := s.Exec(ctx, "u1", tt.reported)
			require.NoError(t, err, "the ledger records every verdict and refuses none of them")
			assert.Equal(t, tt.want, got.BaseVerdict)
			assert.Equal(t, tt.reported, got.ReportedBase)
			assert.NotZero(t, got.Registered, "the store stamps Registered")
			assert.Equal(t, tt.checkpoint, got.Checkpoint, "registering does not overwrite the checkpoint it compares against")
			assert.Equal(t, types.StateRunning, got.State, "taking a declared job is what starts it")

			advice := BaseAdvice(got)
			assert.Contains(t, advice, "u1")
			for _, want := range tt.says {
				assert.Contains(t, advice, want)
			}
			for _, never := range tt.saysNot {
				assert.NotContains(t, advice, never,
					"the advice offers a recovery the record cannot support")
			}

			// Stored, not just returned: the orchestrator reading the plan later sees the
			// same verdict the worker was handed.
			listed, err := s.List()
			require.NoError(t, err)
			require.Len(t, listed, 1)
			assert.Equal(t, tt.want, listed[0].BaseVerdict)
			assert.Equal(t, tt.reported, listed[0].ReportedBase)
		})
	}
}

// A worker registering an id nobody declared has been handed the wrong id. Every other
// write here creates the row it names, so the message has to say where the real ids are.
func TestStoreExecRefusesAnUnknownLease(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := tmpStore(t, t.TempDir())
	seed(t, s, types.Job{ID: "declared", Checkpoint: baseA})

	_, err := s.Exec(ctx, "typo", baseA)
	require.ErrorIs(t, err, ErrUnknownJob)
	assert.Contains(t, err.Error(), hint.LsJobs.String(), "the message names where the declared ids are")
	assert.Contains(t, err.Error(), "typo")

	got, err := s.List()
	require.NoError(t, err)
	require.Len(t, got, 1, "a refused registration writes no row")
	assert.Equal(t, "declared", got[0].ID)
}

func TestStoreExecRequiresABase(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := tmpStore(t, t.TempDir())
	seed(t, s, types.Job{ID: "u1", Checkpoint: baseA})

	_, err := s.Exec(ctx, "u1", "   ")
	require.ErrorIs(t, err, errNoBase)
	assert.Contains(t, err.Error(), "magus vcs checkpoint -o name", "the message names how to produce one")

	got, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, got[0].ReportedBase)
	assert.Empty(t, got[0].BaseVerdict, "an absent verdict is not a judgment")
}

// Registering twice is ordinary: a worker that rebases reports its new base, and the row
// carries where it stands NOW rather than where it first stood.
func TestStoreExecIsIdempotentPerBase(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := tmpStore(t, t.TempDir())
	seed(t, s, types.Job{ID: "u1", Checkpoint: baseA, Criteria: "declared goal"})

	diverged, err := s.Exec(ctx, "u1", baseB)
	require.NoError(t, err)
	require.Equal(t, types.BaseDiverged, diverged.BaseVerdict)

	settled, err := s.Exec(ctx, "u1", baseA)
	require.NoError(t, err)
	assert.Equal(t, types.BaseMatch, settled.BaseVerdict)
	assert.Equal(t, "declared goal", settled.Criteria, "registering erased nothing the orchestrator declared")
	assert.Equal(t, diverged.Created, settled.Created)
}

// A checkpoint handed as an abbreviated revision names the same commit as the full one a
// worker reports, so the verdict is read from the revisions the VCS resolves, not from
// the two spellings.
func TestStoreExecResolvesAnAbbreviatedRevision(t *testing.T) {
	t.Parallel()

	root := gitRepo(t, map[string]string{"a.txt": "a\n"})
	rev := commitRepo(t, root)
	short := rev[:9]
	const digest = "+00112233445566778899aabbccddeeff"

	for _, tt := range []struct {
		name, checkpoint, reported string
		want                       types.JobBaseVerdict
	}{
		{name: "an abbreviated checkpoint", checkpoint: short, reported: rev, want: types.BaseMatch},
		{name: "an abbreviated report", checkpoint: rev, reported: short, want: types.BaseMatch},
		{name: "an abbreviated checkpoint against a dirty tree", checkpoint: short, reported: rev + digest, want: types.BaseRevisionMatch},
		{name: "a revision the VCS cannot place still diverges", checkpoint: short, reported: baseB, want: types.BaseDiverged},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := tmpStore(t, root)
			seed(t, s, types.Job{ID: "u1", Checkpoint: tt.checkpoint})
			got, err := s.Exec(t.Context(), "u1", tt.reported)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.BaseVerdict)
		})
	}
}

// A job that already ended has nothing left to take, and recording a base on it would
// read as work resumed on a closed row.
func TestStoreExecRefusesAnEndedJob(t *testing.T) {
	t.Parallel()

	for _, state := range []types.JobState{types.StatePass, types.StateFail, types.StateNoReturn} {
		s := tmpStore(t, t.TempDir())
		seed(t, s, types.Job{ID: "u1", Checkpoint: baseA, State: state})

		_, err := s.Exec(t.Context(), "u1", baseA)
		require.Error(t, err, state)
		got, err := s.List()
		require.NoError(t, err)
		assert.Empty(t, got[0].ReportedBase, state)
	}
}

// Untracked files on a clean checkpoint's revision are work the job's diff later counts (the
// footprint job wait grades lists them), so the tree is not the one handed out: the verdict
// is revision-match, and the digest it reports is the untracked files', never the digest of
// the empty tracked patch.
func TestStoreExecCountsUntrackedFilesAsThePatch(t *testing.T) {
	t.Parallel()

	const emptyPatch = "e3b0c44298fc1c149afbf4c8996fb924"
	for _, tt := range []struct {
		name  string
		edits map[string]string
	}{
		{name: "untracked only", edits: map[string]string{"new.txt": "fresh\n"}},
		{name: "tracked edits plus untracked", edits: map[string]string{"a.txt": "a changed\n", "new.txt": "fresh\n"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			root := gitRepo(t, map[string]string{"a.txt": "a\n"})
			rev := commitRepo(t, root)
			for name, body := range tt.edits {
				require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o644))
			}
			res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
			require.NoError(t, err)
			cp, err := vcs.Checkpoint(ctx, root, res, false)
			require.NoError(t, err)

			s := tmpStore(t, root)
			seed(t, s, types.Job{ID: "u1", Checkpoint: rev, State: types.StateDeclared})
			got, err := s.Exec(ctx, "u1", vcs.CheckpointToken(cp))
			require.NoError(t, err)

			assert.Equal(t, types.BaseRevisionMatch, got.BaseVerdict)
			assert.NotContains(t, got.ReportedBase, emptyPatch)
			assert.NotContains(t, BaseAdvice(got), emptyPatch)
			seen := changedSince(ctx, res.VCS, "", root, rev)
			assert.Contains(t, seen.Changed, "new.txt", "the footprint counts the untracked file as the job's work")
		})
	}
}

// A job one checkout took is not taken again from another: wait grades the checkout the
// row names, so a second exec would silently point that grade at the wrong tree.
func TestStoreExecRefusesAJobAnotherCheckoutHolds(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	loc, other := twoCheckouts(t)
	ana := NewStore(loc)
	seed(t, ana, types.Job{ID: "u1", Checkpoint: baseA, State: types.StateDeclared})
	taken, err := ana.Exec(ctx, "u1", baseA)
	require.NoError(t, err)

	_, err = NewStore(other).Exec(ctx, "u1", baseB)
	require.Error(t, err)
	for _, want := range []string{"u1", taken.CheckoutRoot, "running", "ago", hint.JobExit.With("u1"), hint.JobApply.String()} {
		assert.Contains(t, err.Error(), want)
	}

	got, err := ana.List()
	require.NoError(t, err)
	assert.Equal(t, taken.CheckoutRoot, got[0].CheckoutRoot, "the refusal leaves the holder's checkout in place")
	assert.Equal(t, baseA, got[0].ReportedBase)

	again, err := ana.Exec(ctx, "u1", baseA)
	require.NoError(t, err, "the holder's own checkout takes it again")
	assert.Equal(t, taken.CheckoutRoot, again.CheckoutRoot)
}

// The way a job moves to another checkout: its holder ends it, and whoever forked it
// declares it again, which hands it out with no checkout attached.
func TestStoreExecTakesAJobDeclaredAgainAfterItEnded(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	loc, other := twoCheckouts(t)
	ana := NewStore(loc)
	seed(t, ana, types.Job{ID: "u1", Checkpoint: baseA, State: types.StateDeclared})
	_, err := ana.Exec(ctx, "u1", baseA)
	require.NoError(t, err)
	_, err = Exit(ctx, ana, "u1", nil, nil)
	require.NoError(t, err)

	revived, err := ana.Update(ctx, "u1", func(row *types.Job) { row.State = types.StateDeclared })
	require.NoError(t, err)
	assert.Empty(t, revived.CheckoutRoot, "a job declared again names no holder")
	assert.Zero(t, revived.Registered)
	assert.Empty(t, revived.ReportedBase)
	assert.Empty(t, revived.BaseVerdict)

	ben, err := NewStore(other).Exec(ctx, "u1", baseA)
	require.NoError(t, err)
	assert.Equal(t, other.Root, ben.CheckoutRoot)
	assert.Equal(t, types.StateRunning, ben.State)
}

// twoCheckouts places two worktrees of one repository, which share its job store.
// A fork records the forking checkout, but nobody has taken the job yet, so a worker in
// another checkout takes it: the refusal is for a job an exec already holds.
func TestStoreExecTakesAJobForkedInAnotherCheckout(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	loc, other := twoCheckouts(t)
	forker := NewStore(loc)
	// A hook may also record a base before anyone takes the job; it is still declared.
	seed(t, forker, types.Job{ID: "u1", Checkpoint: baseA, State: types.StateDeclared, CheckoutRoot: loc.Root, ReportedBase: baseA})

	got, err := NewStore(other).Exec(ctx, "u1", baseB)
	require.NoError(t, err)
	assert.Equal(t, types.StateRunning, got.State)
	assert.NotEqual(t, loc.Root, got.CheckoutRoot, "the worker's checkout takes it")
}

func twoCheckouts(t *testing.T) (main, worktree Location) {
	t.Helper()
	main = tmpLoc(t, t.TempDir())
	worktree = main
	worktree.Root = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(worktree.Root, ".git"),
		[]byte("gitdir: "+filepath.Join(main.Root, ".git", "worktrees", "w")+"\n"), 0o644))
	return main, worktree
}

// A holder bound to its job takes it like anyone else: the move to running is the exec,
// not a state the holder chose for itself.
func TestStoreExecByItsBoundHolderStartsTheJob(t *testing.T) {
	t.Parallel()

	loc := tmpLoc(t, t.TempDir())
	seed(t, NewStore(loc), types.Job{ID: "u1", Checkpoint: baseA, State: types.StateDeclared})

	got, err := boundStore(loc, "u1").Exec(t.Context(), "u1", baseA)
	require.NoError(t, err)
	assert.Equal(t, types.StateRunning, got.State)
}

func TestCompareBase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		checkpoint, reported string
		want                 types.JobBaseVerdict
	}{
		{name: "identical clean tokens", checkpoint: baseA, reported: baseA, want: types.BaseMatch},
		{name: "identical dirty tokens", checkpoint: baseADirty, reported: baseADirty, want: types.BaseMatch},
		{name: "clean against dirty on one revision", checkpoint: baseA, reported: baseADirty, want: types.BaseRevisionMatch},
		{name: "dirty against clean on one revision", checkpoint: baseADirty, reported: baseA, want: types.BaseRevisionMatch},
		{
			name:       "two dirty trees on one revision",
			checkpoint: baseADirty,
			reported:   baseA + "+ffffffffffffffffffffffffffffffff",
			want:       types.BaseRevisionMatch,
		},
		{name: "different revisions", checkpoint: baseA, reported: baseB, want: types.BaseDiverged},
		{
			name:       "a dirty digest cannot rescue a different revision",
			checkpoint: baseADirty,
			reported:   baseB + "+00112233445566778899aabbccddeeff",
			want:       types.BaseDiverged,
		},
		{name: "no checkpoint to compare against", checkpoint: "", reported: baseA, want: types.BaseUnknown},
		{name: "no reported base", checkpoint: baseA, reported: "", want: types.BaseUnknown},
		// Surrounding whitespace is a transport artifact, not a different tree.
		{name: "whitespace is trimmed on both sides", checkpoint: " " + baseA, reported: baseA + "\n", want: types.BaseMatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, compareBase(tt.checkpoint, tt.reported))
		})
	}
}
