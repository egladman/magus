package ledger

import (
	"testing"

	"github.com/egladman/magus/types"
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

func TestStoreRegister(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		checkpoint string
		reported   string
		want       types.DelegationBaseVerdict
		says       []string
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
			says: []string{baseA, "00112233445566778899aabbccddeeff", "none (clean tree)", "Materialize the files you touch"},
		},
		{
			name:       "a different revision is a divergence",
			checkpoint: baseA,
			reported:   baseB,
			want:       types.BaseDiverged,
			says:       []string{baseA, baseB, "Respawn from"},
		},
		{
			name:       "a unit declared without a checkpoint has nothing to compare against",
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
			s := NewStore(Location{CacheDir: t.TempDir(), Root: t.TempDir()})
			_, err := s.Put(ctx, types.DelegationUnit{ID: "u1", Checkpoint: tt.checkpoint, State: types.StateDeclared})
			require.NoError(t, err)

			got, err := s.Register(ctx, "u1", tt.reported)
			require.NoError(t, err, "the ledger records every verdict and refuses none of them")
			assert.Equal(t, tt.want, got.BaseVerdict)
			assert.Equal(t, tt.reported, got.ReportedBase)
			assert.NotZero(t, got.Registered, "the store stamps Registered")
			assert.Equal(t, tt.checkpoint, got.Checkpoint, "registering does not overwrite the checkpoint it compares against")
			assert.Equal(t, types.StateDeclared, got.State, "registering advances no state; that is the caller's put")

			advice := RegistrationAdvice(got)
			assert.Contains(t, advice, "u1")
			for _, want := range tt.says {
				assert.Contains(t, advice, want)
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
func TestStoreRegisterRefusesAnUnknownUnit(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := NewStore(Location{CacheDir: t.TempDir(), Root: t.TempDir()})
	_, err := s.Put(ctx, types.DelegationUnit{ID: "declared", Checkpoint: baseA})
	require.NoError(t, err)

	_, err = s.Register(ctx, "typo", baseA)
	require.ErrorIs(t, err, ErrUnknownUnit)
	assert.Contains(t, err.Error(), "magus_ledger list", "the message names where the declared ids are")
	assert.Contains(t, err.Error(), "typo")

	got, err := s.List()
	require.NoError(t, err)
	require.Len(t, got, 1, "a refused registration writes no row")
	assert.Equal(t, "declared", got[0].ID)
}

func TestStoreRegisterRequiresABase(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := NewStore(Location{CacheDir: t.TempDir(), Root: t.TempDir()})
	_, err := s.Put(ctx, types.DelegationUnit{ID: "u1", Checkpoint: baseA})
	require.NoError(t, err)

	_, err = s.Register(ctx, "u1", "   ")
	require.ErrorIs(t, err, ErrNoBase)
	assert.Contains(t, err.Error(), "magus vcs checkpoint -o name", "the message names how to produce one")

	got, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, got[0].ReportedBase)
	assert.Empty(t, got[0].BaseVerdict, "an absent verdict is not a judgment")
}

// Registering twice is ordinary: a worker that rebases reports its new base, and the row
// carries where it stands NOW rather than where it first stood.
func TestStoreRegisterIsIdempotentPerBase(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := NewStore(Location{CacheDir: t.TempDir(), Root: t.TempDir()})
	_, err := s.Put(ctx, types.DelegationUnit{ID: "u1", Checkpoint: baseA, Goal: "declared goal"})
	require.NoError(t, err)

	diverged, err := s.Register(ctx, "u1", baseB)
	require.NoError(t, err)
	require.Equal(t, types.BaseDiverged, diverged.BaseVerdict)

	settled, err := s.Register(ctx, "u1", baseA)
	require.NoError(t, err)
	assert.Equal(t, types.BaseMatch, settled.BaseVerdict)
	assert.Equal(t, "declared goal", settled.Goal, "registering erased nothing the orchestrator declared")
	assert.Equal(t, diverged.Created, settled.Created)
}

func TestCompareBase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		checkpoint, reported string
		want                 types.DelegationBaseVerdict
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

			assert.Equal(t, tt.want, CompareBase(tt.checkpoint, tt.reported))
		})
	}
}
