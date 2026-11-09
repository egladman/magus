package magus

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/workspacegraph"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

const humanOnlyMagusfile = `import "magus";

magus\project({
    "targets": {
        "bench": {
            "skip_cache": "launches paid sessions",
            "human_only": "launches paid sessions",
        },
    },
});

export fun bench(ctx: magus\Context, args: [str]) > void {}

export fun lint(ctx: magus\Context, args: [str]) > void {}

export fun suite(ctx: magus\Context, args: [str]) > void {
    ctx.needs(bench, lint);
}
`

// syncedHumanOnlyWorkspace is a workspace declaring bench human_only, composed by suite,
// whose knowledge store an evaluated read has synced.
func syncedHumanOnlyWorkspace(tb testing.TB) *testkit.Workspace {
	tb.Helper()
	tb.Setenv("MAGUS_VCS_ENABLED", "false") // a temp dir no repository claims would otherwise ask git about one
	w := testkit.NewWorkspace(tb)
	w.Magusfile(humanOnlyMagusfile)
	ctx := context.Background()
	ws, err := Inspect(ctx, w.Root(), WithSpawnRule(TierCeiling))
	require.NoError(tb, err)
	_, err = workspacegraph.Build(ctx, ws, w.Root(), config.Config{}, false, slog.New(slog.DiscardHandler))
	require.NoError(tb, err)
	return w
}

// unopenable is a lazy workspace that fails, and counts, every open: a recorded read must
// answer without one.
func unopenable(root string, opens *int) *LazyWorkspace {
	return NewLazyWorkspace(root, func(context.Context) (*Magus, error) {
		*opens++
		return nil, errors.New("a recorded read must not open the workspace")
	})
}

// TestRecordedHumanOnlyAnswersOnlyWhileTheSourcesHold drives the record end to end: an
// evaluated knowledge read records what the workspace declares, a read that never opens
// the workspace answers it while the sources are as they were, and every case the record
// cannot vouch for sends the caller to the workspace.
func TestRecordedHumanOnlyAnswersOnlyWhileTheSourcesHold(t *testing.T) {
	ctx := context.Background()
	w := syncedHumanOnlyWorkspace(t)
	root := w.Root()
	cfg := config.Config{}
	opens := 0

	got, ok := workspacegraph.RecordedTargetPolicies(ctx, unopenable(root, &opens), cfg)
	require.True(t, ok, "the sync recorded the targets and nothing has moved since")
	assert.Equal(t, types.TargetPolicyIndex{
		Projects: []string{"."},
		Targets: []types.TargetPolicy{{
			TargetRef: types.TargetRef{Project: ".", Target: "bench"},
			HumanOnly: true,
			Reason:    "launches paid sessions",
			SkipCache: true,
			ReachedBy: []types.TargetRef{{Project: ".", Target: "suite"}},
		}},
	}, got, "the record is what the evaluation declares")

	w.Magusfile(humanOnlyMagusfile + "\nexport fun extra(ctx: magus\\Context, args: [str]) > void {}\n")
	_, ok = workspacegraph.RecordedTargetPolicies(ctx, unopenable(root, &opens), cfg)
	assert.False(t, ok, "an edited magusfile moves the domain fast stamp: a stale record is never an answer")

	_, ok = workspacegraph.RecordedTargetPolicies(ctx, unopenable(t.TempDir(), &opens), cfg)
	assert.False(t, ok, "a workspace with no store has nothing recorded")
	assert.Zero(t, opens, "RecordedTargetPolicies never opens; the caller decides")
}

// TestAnOpenRecordsWhatNoSyncDid drives the guard's record: a workspace no
// sync reached answers after one open records it, until its sources move.
func TestAnOpenRecordsWhatNoSyncDid(t *testing.T) {
	t.Setenv("MAGUS_VCS_ENABLED", "false")
	ctx := context.Background()
	w := testkit.NewWorkspace(t)
	w.Magusfile(humanOnlyMagusfile)
	root := w.Root()
	cfg := config.Config{}
	opens := 0

	_, ok := workspacegraph.RecordedTargetPolicies(ctx, unopenable(root, &opens), cfg)
	require.False(t, ok, "no sync and no open has recorded anything")

	opened := NewLazyWorkspace(root, func(ctx context.Context) (*Magus, error) {
		ws, err := Inspect(ctx, root, WithSpawnRule(TierCeiling))
		if err != nil {
			return nil, err
		}
		m, ok := ws.(*Magus)
		require.True(t, ok, "Inspect opens a *Magus")
		return m, nil
	})
	require.NoError(t, workspacegraph.RecordTargetPolicies(ctx, opened, cfg))

	got, ok := workspacegraph.RecordedTargetPolicies(ctx, unopenable(root, &opens), cfg)
	require.True(t, ok, "the open's record answers while nothing has moved")
	assert.Equal(t, types.TargetPolicyIndex{
		Projects: []string{"."},
		Targets: []types.TargetPolicy{{
			TargetRef: types.TargetRef{Project: ".", Target: "bench"},
			HumanOnly: true,
			Reason:    "launches paid sessions",
			SkipCache: true,
			ReachedBy: []types.TargetRef{{Project: ".", Target: "suite"}},
		}},
	}, got)

	w.Magusfile(humanOnlyMagusfile + "\nexport fun extra(ctx: magus\\Context, args: [str]) > void {}\n")
	_, ok = workspacegraph.RecordedTargetPolicies(ctx, unopenable(root, &opens), cfg)
	assert.False(t, ok, "an edited magusfile moves the stamp the record was written under")
	assert.Zero(t, opens)
}

// TestRecordedHumanOnlyIgnoresAManifestThatNeverRecordedIt pins the upgrade case: a store a
// binary from before the record wrote matches on its stamps but vouches for nothing.
func TestRecordedHumanOnlyIgnoresAManifestThatNeverRecordedIt(t *testing.T) {
	ctx := context.Background()
	w := syncedHumanOnlyWorkspace(t)
	path := filepath.Join(w.Root(), ".magus", "knowledge", "manifest.json")
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var man map[string]any
	require.NoError(t, json.Unmarshal(b, &man))
	require.Equal(t, true, man["target_policies_known"], "the sync recorded it")
	// What a magus from before the target-policy record wrote: the human_only keys alone.
	man["human_only"], man["human_only_known"] = man["target_policies"], true
	delete(man, "target_policies")
	delete(man, "target_policies_known")
	b, err = json.Marshal(man)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o644))

	opens := 0
	_, ok := workspacegraph.RecordedTargetPolicies(ctx, unopenable(w.Root(), &opens), config.Config{})
	assert.False(t, ok)
	assert.Zero(t, opens)
}

// BenchmarkHumanOnlyRead compares what a hook pays to learn the human_only targets: the
// recorded read against a fresh process's view of the tree, and the evaluated workspace
// open it replaces, read the way the guard reads an opened one.
func BenchmarkHumanOnlyRead(b *testing.B) {
	ctx := context.Background()
	root := syncedHumanOnlyWorkspace(b).Root()
	b.Run("recorded", func(b *testing.B) {
		opens := 0
		for b.Loop() {
			if _, ok := workspacegraph.RecordedTargetPolicies(ctx, unopenable(root, &opens), config.Config{}); !ok {
				b.Fatal("the record went stale under the benchmark")
			}
		}
	})
	b.Run("inspect", func(b *testing.B) {
		for b.Loop() {
			ws, err := Inspect(ctx, root, WithSpawnRule(TierCeiling))
			if err != nil {
				b.Fatal(err)
			}
			found := 0
			for _, p := range ws.All() {
				for _, pol := range p.TargetPolicies {
					if pol.HumanOnly {
						found++
					}
				}
			}
			if found != 1 {
				b.Fatalf("the open found %d human_only targets, want 1", found)
			}
		}
	})
}
