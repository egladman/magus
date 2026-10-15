package job

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/testkit"
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

// execRepo is a git checkout for a store to exec in: Exec reads its base from the tree.
func execRepo(t *testing.T) *testkit.Repo {
	t.Helper()
	return testkit.NewRepo(t, map[string]string{"a.txt": "a\n"})
}

// checkoutToken is the token `magus vcs checkpoint -o name` prints for the checkout at
// root, which is what Exec records.
func checkoutToken(t *testing.T, root string) string {
	t.Helper()
	res, err := vcs.Resolve(t.Context(), root, "", types.VCSOptions{})
	require.NoError(t, err)
	cp, err := vcs.Checkpoint(t.Context(), root, res, false)
	require.NoError(t, err)
	return cp.Token()
}

func TestStoreExec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// checkpoint is the token the job was handed, given the repository at its first
		// commit; work then moves the checkout before the exec.
		checkpoint func(repo *testkit.Repo) string
		work       func(repo *testkit.Repo)
		want       types.JobBaseVerdict
		says       func(checkpoint, reported string) []string
		saysNot    []string
	}{
		{
			name:       "the same token is a match",
			checkpoint: (*testkit.Repo).Head,
			want:       types.BaseMatch,
		},
		{
			name:       "a dirty tree on the handed revision is a revision-match, not a divergence",
			checkpoint: (*testkit.Repo).Head,
			work:       func(repo *testkit.Repo) { repo.WriteTree(map[string]string{"a.txt": "a changed\n"}) },
			want:       types.BaseRevisionMatch,
			// Naming BOTH digests is the point: the revision agreeing is what makes this
			// confusing, so the reading has to show the half that did not.
			// "have the orchestrator commit", NOT the "Materialize the files you touch from
			// the checkpoint" this pinned until 2026-09-08. A revision-match means the same
			// commit and a DIFFERENT uncommitted patch, and a checkpoint carries only that
			// patch's digest, so there was nothing to materialize from and the advice told
			// a worker to perform an impossible recovery. The negative assertion below is
			// what keeps a restore promise from coming back.
			says: func(checkpoint, reported string) []string {
				_, digest := types.ParseCheckpointToken(reported)
				return []string{checkpoint, digest, "none (clean tree)", "have the orchestrator commit"}
			},
			saysNot: []string{"Materialize"},
		},
		{
			name:       "a different revision is a divergence",
			checkpoint: (*testkit.Repo).Head,
			work:       func(repo *testkit.Repo) { repo.Commit("next", map[string]string{"b.txt": "b\n"}) },
			want:       types.BaseDiverged,
			says: func(checkpoint, reported string) []string {
				return []string{checkpoint, reported, "Respawn from"}
			},
		},
		{
			name:       "a lease declared without a checkpoint has nothing to compare against",
			checkpoint: func(*testkit.Repo) string { return "" },
			want:       types.BaseUnknown,
			says:       func(string, string) []string { return []string{"magus vcs checkpoint -o name"} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			repo := execRepo(t)
			checkpoint := tt.checkpoint(repo)
			if tt.work != nil {
				tt.work(repo)
			}
			reported := checkoutToken(t, repo.Root())
			s := tmpStore(t, repo.Root())
			seed(t, s, types.Job{ID: "u1", Checkpoint: checkpoint, State: types.StateDeclared})

			got, err := s.Exec(ctx, "u1")
			require.NoError(t, err, "the ledger records every verdict and refuses none of them")
			assert.NotZero(t, got.Registered, "the store stamps Registered")
			wantRow := stamped(types.Job{
				ID:           "u1",
				Checkpoint:   checkpoint,
				State:        types.StateRunning,
				ReportedBase: reported,
				BaseVerdict:  tt.want,
				Registered:   got.Registered,
				CheckoutRoot: got.CheckoutRoot,
			}, got)
			assert.Equal(t, wantRow, got, "registering does not overwrite the checkpoint it compares against, and taking a declared job is what starts it")

			advice := BaseAdvice(got)
			assert.Contains(t, advice, "u1")
			if tt.says != nil {
				for _, want := range tt.says(checkpoint, reported) {
					assert.Contains(t, advice, want)
				}
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
			assert.Equal(t, wantRow, listed[0])
		})
	}
}

// The base is the tree's, never one a worker types. The row cited-runs-real recorded a
// match from a clean token typed over an edited tracked file: read from the tree, the same
// checkout is its revision plus the patch digest, a revision-match.
func TestExecReadsTheBaseFromTheCheckout(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := execRepo(t)
	clean := repo.Head()
	repo.WriteTree(map[string]string{"a.txt": "a edited\n"})
	s := tmpStore(t, repo.Root())
	seed(t, s, types.Job{ID: "u1", Checkpoint: clean, State: types.StateDeclared})

	got, err := s.Exec(ctx, "u1")
	require.NoError(t, err)

	rev, digest := types.ParseCheckpointToken(got.ReportedBase)
	assert.Equal(t, clean, rev, "the base names the checkout's revision")
	assert.NotEmpty(t, digest, "a dirty tree records its patch digest")
	assert.Equal(t, checkoutToken(t, repo.Root()), got.ReportedBase, "the base is what `magus vcs checkpoint -o name` prints there")
	assert.Equal(t, types.BaseRevisionMatch, got.BaseVerdict, "the clean checkpoint and the dirty tree share only the revision")
}

// A worker registering an id nobody declared has been handed the wrong id. Every other
// write here creates the row it names, so the message has to say where the real ids are.
func TestStoreExecRefusesAnUnknownLease(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := execRepo(t)
	s := tmpStore(t, repo.Root())
	seed(t, s, types.Job{ID: "declared", Checkpoint: repo.Head()})

	_, err := s.Exec(ctx, "typo")
	require.ErrorIs(t, err, ErrUnknownJob)
	assert.Contains(t, err.Error(), hint.LsJobs.String(), "the message names where the declared ids are")
	assert.Contains(t, err.Error(), "typo")

	got, err := s.List()
	require.NoError(t, err)
	require.Len(t, got, 1, "a refused registration writes no row")
	assert.Equal(t, "declared", got[0].ID)
}

// A checkout no version control answers in has no base to record, so the exec is refused
// and the row is left as it was.
func TestStoreExecRefusesACheckoutWithNoRevision(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	root := t.TempDir()
	s := tmpStore(t, root)
	seed(t, s, types.Job{ID: "u1", Checkpoint: baseA, State: types.StateDeclared})

	_, err := s.Exec(ctx, "u1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), root, "the message names the checkout it read")
	assert.Contains(t, err.Error(), hint.VCSCheckpoint.With("-o name"), "the message names how to see what version control answers")

	got, err := s.List()
	require.NoError(t, err)
	assert.Equal(t, types.StateDeclared, got[0].State)
	assert.Empty(t, got[0].ReportedBase)
	assert.Empty(t, got[0].BaseVerdict, "an absent verdict is not a judgment")
}

// Registering twice is ordinary: a worker that moves to another revision records its new
// base, and the row carries where it stands NOW rather than where it first stood.
func TestStoreExecIsIdempotentPerBase(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := execRepo(t)
	handed := repo.Head()
	repo.Commit("elsewhere", map[string]string{"b.txt": "b\n"})
	s := tmpStore(t, repo.Root())
	seed(t, s, types.Job{ID: "u1", Checkpoint: handed, Criteria: "declared goal"})

	diverged, err := s.Exec(ctx, "u1")
	require.NoError(t, err)
	require.Equal(t, types.BaseDiverged, diverged.BaseVerdict)

	repo.Git("checkout", "-q", handed)
	settled, err := s.Exec(ctx, "u1")
	require.NoError(t, err)
	want := stamped(types.Job{
		ID:           "u1",
		Criteria:     "declared goal",
		Checkpoint:   handed,
		State:        types.StateRunning,
		ReportedBase: handed,
		BaseVerdict:  types.BaseMatch,
		Registered:   settled.Registered,
		CheckoutRoot: settled.CheckoutRoot,
	}, settled)
	want.Created = diverged.Created
	assert.Equal(t, want, settled, "registering erased nothing the orchestrator declared and kept the row it first wrote")
}

// A checkpoint handed as an abbreviated revision names the same commit as the full one the
// checkout reports, so the verdict is read from the revisions the VCS resolves, not from
// the two spellings.
func TestStoreExecResolvesAnAbbreviatedRevision(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		short bool
		dirty bool
		want  types.JobBaseVerdict
	}{
		{name: "an abbreviated checkpoint", short: true, want: types.BaseMatch},
		{name: "an abbreviated checkpoint against a dirty tree", short: true, dirty: true, want: types.BaseRevisionMatch},
		{name: "a revision the VCS cannot place still diverges", want: types.BaseDiverged},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := execRepo(t)
			checkpoint := baseB
			if tt.short {
				checkpoint = repo.Head()[:9]
			}
			if tt.dirty {
				repo.WriteTree(map[string]string{"a.txt": "a changed\n"})
			}
			s := tmpStore(t, repo.Root())
			seed(t, s, types.Job{ID: "u1", Checkpoint: checkpoint})
			got, err := s.Exec(t.Context(), "u1")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.BaseVerdict)
		})
	}
}

// A job that already ended has nothing left to take, and recording a base on it would
// read as work resumed on a closed row.
func TestStoreExecRefusesAnEndedJob(t *testing.T) {
	t.Parallel()

	repo := execRepo(t)
	for _, state := range []types.JobState{types.StatePass, types.StateFail, types.StateNoReturn} {
		s := tmpStore(t, repo.Root())
		seed(t, s, types.Job{ID: "u1", Checkpoint: repo.Head(), State: state})

		_, err := s.Exec(t.Context(), "u1")
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
			repo := execRepo(t)
			root := repo.Root()
			rev := repo.Head()
			for name, body := range tt.edits {
				require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o644))
			}
			res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
			require.NoError(t, err)

			s := tmpStore(t, root)
			seed(t, s, types.Job{ID: "u1", Checkpoint: rev, State: types.StateDeclared})
			got, err := s.Exec(ctx, "u1")
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
	loc, other, head := twoCheckouts(t)
	ana := NewStore(loc)
	seed(t, ana, types.Job{ID: "u1", Checkpoint: head, State: types.StateDeclared})
	taken, err := ana.Exec(ctx, "u1")
	require.NoError(t, err)

	_, err = NewStore(other).Exec(ctx, "u1")
	require.ErrorIs(t, err, ErrHeldElsewhere)
	for _, want := range []string{"u1", taken.CheckoutRoot, "running", "ago", hint.JobExit.With("u1"), hint.JobApply.String()} {
		assert.Contains(t, err.Error(), want)
	}

	got, err := ana.List()
	require.NoError(t, err)
	assert.Equal(t, head, taken.ReportedBase)
	assert.Equal(t, taken, got[0], "the refusal leaves the holder's row, checkout included, as it was")

	again, err := ana.Exec(ctx, "u1")
	require.NoError(t, err, "the holder's own checkout takes it again")
	assert.Equal(t, taken.CheckoutRoot, again.CheckoutRoot)
}

// The way a job moves to another checkout: its holder ends it, and whoever forked it
// declares it again, which hands it out with no checkout attached.
func TestStoreExecTakesAJobDeclaredAgainAfterItEnded(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	loc, other, head := twoCheckouts(t)
	ana := NewStore(loc)
	seed(t, ana, types.Job{ID: "u1", Checkpoint: head, State: types.StateDeclared})
	_, err := ana.Exec(ctx, "u1")
	require.NoError(t, err)
	_, err = Exit(ctx, ana, "u1", nil, nil)
	require.NoError(t, err)

	revived, err := ana.Update(ctx, "u1", func(row *types.Job) { row.State = types.StateDeclared })
	require.NoError(t, err)
	assert.Empty(t, revived.CheckoutRoot, "a job declared again names no holder")
	assert.Zero(t, revived.Registered)
	assert.Empty(t, revived.ReportedBase)
	assert.Empty(t, revived.BaseVerdict)

	ben, err := NewStore(other).Exec(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, other.Root, ben.CheckoutRoot)
	assert.Equal(t, types.StateRunning, ben.State)
}

// Ending a job that already ended says so, and how and when, rather than the refusal a
// caller bound to a different row gets for writing a row that is not its own.
func TestExitSaysAnEndedJobAlreadyEnded(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	loc, _, head := twoCheckouts(t)
	s := NewStore(loc)
	seed(t, s, types.Job{ID: "u1", Checkpoint: head, State: types.StateDeclared})
	_, err := s.Exec(ctx, "u1")
	require.NoError(t, err)
	_, err = Exit(ctx, s, "u1", nil, nil)
	require.NoError(t, err)

	_, err = Exit(ctx, s, "u1", nil, nil)
	require.Error(t, err)
	for _, want := range []string{"u1", "already ended", string(types.StateNoReturn), "ago", "abandoned by its holder"} {
		assert.Contains(t, err.Error(), want)
	}
}

// A fork records the forking checkout, but nobody has taken the job yet, so a worker in
// another checkout takes it: the refusal is for a job an exec already holds.
func TestStoreExecTakesAJobForkedInAnotherCheckout(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	loc, other, head := twoCheckouts(t)
	forker := NewStore(loc)
	// A hook may also record a base before anyone takes the job; it is still declared.
	seed(t, forker, types.Job{ID: "u1", Checkpoint: head, State: types.StateDeclared, CheckoutRoot: loc.Root, ReportedBase: head})

	got, err := NewStore(other).Exec(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, types.StateRunning, got.State)
	assert.NotEqual(t, loc.Root, got.CheckoutRoot, "the worker's checkout takes it")
}

// twoCheckouts places two worktrees of one repository, which share its job store, and
// returns the revision both are on.
func twoCheckouts(t *testing.T) (main, worktree Location, head string) {
	t.Helper()
	repo := execRepo(t)
	main = tmpLoc(t, repo.Root())
	worktree = main
	worktree.Root = repo.AddWorktree("w").Root()
	return main, worktree, repo.Head()
}

// A holder bound to its job takes it like anyone else: the move to running is the exec,
// not a state the holder chose for itself.
func TestStoreExecByItsBoundHolderStartsTheJob(t *testing.T) {
	t.Parallel()

	repo := execRepo(t)
	loc := tmpLoc(t, repo.Root())
	seed(t, NewStore(loc), types.Job{ID: "u1", Checkpoint: repo.Head(), State: types.StateDeclared})

	got, err := boundStore(loc, "u1").Exec(t.Context(), "u1")
	require.NoError(t, err)
	assert.Equal(t, types.StateRunning, got.State)
}

func TestSameRevisionMatchesAnAbbreviationOfSevenOrMoreHexDigits(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"07e1c163ea67b4d6", "07e1c16", true},
		{"07e1c16", "07e1c163ea67b4d6", true},
		{"07e1c16", " 07e1c16\n", true},
		{"07E1C163EA67B4D6", "07e1c16", true},
		{"07e1c16", "24114cc", false},
		{"07e1c16", "", false},
		{"", "", false},
		{"07e1c163ea67b4d6", "0", false},
		{"07e1c163ea67b4d6", "07e1c1", false},
		{"main", "main", true},
		{"mainline", "mainlin", false},
	} {
		assert.Equal(t, tc.want, SameRevision(tc.a, tc.b), "%q %q", tc.a, tc.b)
	}
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
