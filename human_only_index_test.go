package magus

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/gen/mocks"
)

// TestHumanOnlyIndexStopsAtTheFirstHumanOnlyTarget pins the walk the record shares with
// the guard: a composer reaches a human_only target through any depth and across projects,
// but never through another human_only target, whose approval covers its own chain.
func TestHumanOnlyIndexStopsAtTheFirstHumanOnlyTarget(t *testing.T) {
	root := &types.Project{
		Path: ".",
		TargetPolicies: map[string]types.Target{
			"bench": {HumanOnly: true, HumanOnlyReason: "launches paid sessions", SkipCache: true},
			"gated": {HumanOnly: true, HumanOnlyReason: "asks a person"},
			"inner": {HumanOnly: true, HumanOnlyReason: "runs under gated"},
			"lint":  {SkipCache: true},
		},
		TargetChains: map[string][]types.ChainStep{
			"suite": {{Target: "bench"}, {Target: "lint"}},
			"outer": {{Target: "suite"}},
			"cross": {{Project: "evals", Target: "grade"}},
			"gated": {{Target: "inner"}},
			"via":   {{Target: "gated"}},
		},
	}
	evals := &types.Project{Path: "evals", TargetPolicies: map[string]types.Target{"grade": {HumanOnly: true, HumanOnlyReason: "spends tokens"}}}
	projects := []*types.Project{root, evals}
	ws := mocks.NewMockWorkspaceReader(t)
	ws.EXPECT().All().Return(projects)
	ws.EXPECT().Get(mock.Anything).RunAndReturn(func(path string) *types.Project {
		for _, p := range projects {
			if p.Path == path {
				return p
			}
		}
		return nil
	}).Maybe()

	ref := func(project, target string) types.TargetRef { return types.TargetRef{Project: project, Target: target} }
	assert.Equal(t, types.HumanOnlyIndex{
		Projects: []string{".", "evals"},
		Targets: []types.HumanOnlyTarget{
			{TargetRef: ref(".", "bench"), Reason: "launches paid sessions", SkipCache: true, ReachedBy: []types.TargetRef{ref(".", "outer"), ref(".", "suite")}},
			{TargetRef: ref(".", "gated"), Reason: "asks a person", ReachedBy: []types.TargetRef{ref(".", "via")}},
			{TargetRef: ref(".", "inner"), Reason: "runs under gated"},
			{TargetRef: ref("evals", "grade"), Reason: "spends tokens", ReachedBy: []types.TargetRef{ref(".", "cross")}},
		},
	}, humanOnlyIndex(ws))
}

// TestHumanOnlyIndexOfAWorkspaceDeclaringNone pins the cheap case: the projects are listed
// so a reader can tell a declaration of none from no record, and no chain is walked.
func TestHumanOnlyIndexOfAWorkspaceDeclaringNone(t *testing.T) {
	ws := mocks.NewMockWorkspaceReader(t)
	ws.EXPECT().All().Return([]*types.Project{{Path: ".", TargetChains: map[string][]types.ChainStep{"ci": {{Target: "test"}}}}})
	assert.Equal(t, types.HumanOnlyIndex{Projects: []string{"."}}, humanOnlyIndex(ws))
}

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
func syncedHumanOnlyWorkspace(tb testing.TB) (*testkit.Workspace, types.WorkspaceRepository) {
	tb.Helper()
	tb.Setenv("MAGUS_VCS_ENABLED", "false") // see TestKnowledgeFastStampsFoldWhatTheEvaluationRead
	w := testkit.NewWorkspace(tb)
	w.Magusfile(humanOnlyMagusfile)
	ctx := context.Background()
	ws, err := Inspect(ctx, w.Root())
	require.NoError(tb, err)
	_, err = BuildKnowledgeGraph(ctx, ws, w.Root(), config.Config{}, false, slog.New(slog.DiscardHandler))
	require.NoError(tb, err)
	return w, ws
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
	w, ws := syncedHumanOnlyWorkspace(t)
	root := w.Root()
	cfg := config.Config{}
	opens := 0

	got, ok := RecordedHumanOnly(ctx, unopenable(root, &opens), cfg)
	require.True(t, ok, "the sync recorded the targets and nothing has moved since")
	assert.Equal(t, humanOnlyIndex(ws), got, "the record is what the evaluation declares")
	require.Len(t, got.Targets, 1)
	assert.Equal(t, []types.TargetRef{{Project: ".", Target: "suite"}}, got.Targets[0].ReachedBy)

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
	w, _ := syncedHumanOnlyWorkspace(t)
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
// recorded read against a fresh process's view of the tree, and the workspace open it
// replaces.
func BenchmarkHumanOnlyRead(b *testing.B) {
	ctx := context.Background()
	w, _ := syncedHumanOnlyWorkspace(b)
	root := w.Root()
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
			_ = humanOnlyIndex(ws)
		}
	})
}
