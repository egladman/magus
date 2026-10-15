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
	ws, err := Inspect(ctx, w.Root())
	require.NoError(tb, err)
	_, err = BuildKnowledgeGraph(ctx, ws, w.Root(), config.Config{}, false, slog.New(slog.DiscardHandler))
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

	got, ok := RecordedHumanOnly(ctx, unopenable(root, &opens), cfg)
	require.True(t, ok, "the sync recorded the targets and nothing has moved since")
	assert.Equal(t, types.HumanOnlyIndex{
		Projects: []string{"."},
		Targets: []types.HumanOnlyTarget{{
			TargetRef: types.TargetRef{Project: ".", Target: "bench"},
			Reason:    "launches paid sessions",
			SkipCache: true,
			ReachedBy: []types.TargetRef{{Project: ".", Target: "suite"}},
		}},
	}, got, "the record is what the evaluation declares")

	w.Magusfile(humanOnlyMagusfile + "\nexport fun extra(ctx: magus\\Context, args: [str]) > void {}\n")
	_, ok = RecordedHumanOnly(ctx, unopenable(root, &opens), cfg)
	assert.False(t, ok, "an edited magusfile moves the domain fast stamp: a stale record is never an answer")

	_, ok = RecordedHumanOnly(ctx, unopenable(t.TempDir(), &opens), cfg)
	assert.False(t, ok, "a workspace with no store has nothing recorded")
	assert.Zero(t, opens, "RecordedHumanOnly never opens; the caller decides")
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
	require.Equal(t, true, man["human_only_known"], "the sync recorded it")
	delete(man, "human_only")
	delete(man, "human_only_known")
	b, err = json.Marshal(man)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o644))

	opens := 0
	_, ok := RecordedHumanOnly(ctx, unopenable(w.Root(), &opens), config.Config{})
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
			if _, ok := RecordedHumanOnly(ctx, unopenable(root, &opens), config.Config{}); !ok {
				b.Fatal("the record went stale under the benchmark")
			}
		}
	})
	b.Run("inspect", func(b *testing.B) {
		for b.Loop() {
			ws, err := Inspect(ctx, root)
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
