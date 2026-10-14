package doctor

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// tieredWorkspace is a workspace that declares model tiers.
type tieredWorkspace struct {
	types.WorkspaceReader
	set *types.ModelTierSet
}

func (w tieredWorkspace) ModelTiers() *types.ModelTierSet { return w.set }

func declaredTiers(t *testing.T) *types.ModelTierSet {
	t.Helper()
	set, err := types.NewModelTierSet(types.ModelTierDeclaration{
		Enum:    "ModelTier",
		Site:    "magusfile.buzz:3",
		Cases:   []string{"economy", "standard"},
		Default: 1,
		Bounds:  []types.ModelTierBounds{{}, {}},
		Skills:  []types.SkillVariant{types.SkillVariantFull, types.SkillVariantShort},
		Hosts:   map[types.HarnessKey][]string{{ID: "claude-code"}: {"haiku", "sonnet"}},
	})
	require.NoError(t, err)
	return set
}

func tierOutcomes(tier string, kind types.JobKind, source types.ModelTierSource, reason string, passed, failed int) []types.JobOutcome {
	var out []types.JobOutcome
	for i := range passed + failed {
		state := types.StatePass
		if i >= passed {
			state = types.StateFail
		}
		out = append(out, types.JobOutcome{Job: tier + "-job", ModelTier: tier, Kind: kind, Source: source, Reason: reason, State: state})
	}
	return out
}

func tierFitRunner(t *testing.T, set *types.ModelTierSet, outcomes ...[]types.JobOutcome) *runner {
	t.Helper()
	h := forecast.History{Version: forecast.HistoryVersion}
	for _, o := range outcomes {
		h.Jobs = append(h.Jobs, o...)
	}
	path := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, h.Save(t.Context(), path))
	return &runner{
		opts: options{cfg: config.Config{HistoryPath: path}},
		ws:   tieredWorkspace{set: set},
		ctx:  t.Context(),
	}
}

func TestModelTierFitNamesTheLineThatRoutedAFailingTier(t *testing.T) {
	r := tierFitRunner(t, declaredTiers(t),
		tierOutcomes("economy", types.JobKindSymbol, types.ModelTierSourceMatrix, "small and checked", 2, 6),
		tierOutcomes("economy", types.JobKindSymbol, types.ModelTierSourceDefault, "the declared default", 1, 1),
		tierOutcomes("standard", types.JobKindCheck, types.ModelTierSourceDefault, "the declared default", 12, 0),
	)

	got := r.checkModelTierFit(nil)

	assert.Equal(t, types.CheckAdvice, got.Status)
	assert.Contains(t, got.Message, "1 of 2")
	require.Len(t, got.Details, 1)
	detail := got.Details[0]
	assert.Contains(t, detail, "economy on symbol jobs: 3 of 10 passed")
	assert.Contains(t, detail, "2 took the declared default: change the default in the model tier declaration at magusfile.buzz:3")
	assert.Contains(t, detail, `8 were routed here by the function registered with magus\job.modelTier (most often "small and checked")`)
}

func TestModelTierFitIsQuietUntilAPairHasEnoughJobs(t *testing.T) {
	r := tierFitRunner(t, declaredTiers(t),
		tierOutcomes("economy", types.JobKindSymbol, types.ModelTierSourceDefault, "", 0, modelTierFitJobs-1),
	)

	got := r.checkModelTierFit(nil)

	assert.Equal(t, types.CheckOK, got.Status)
	assert.Empty(t, got.Details)
}

func TestModelTierFitKeepsATierThatPasses(t *testing.T) {
	r := tierFitRunner(t, declaredTiers(t),
		tierOutcomes("economy", types.JobKindCheck, types.ModelTierSourceMatrix, "small", 11, 1),
	)

	got := r.checkModelTierFit(nil)

	assert.Equal(t, types.CheckOK, got.Status)
	assert.Contains(t, got.Message, "1 model tier and job kind pair(s)")
}

func TestModelTierFitSkipsATierTheDeclarationDropped(t *testing.T) {
	r := tierFitRunner(t, declaredTiers(t),
		tierOutcomes("retired", types.JobKindCheck, types.ModelTierSourceDefault, "", 0, 20),
	)

	got := r.checkModelTierFit(nil)

	assert.Equal(t, types.CheckOK, got.Status, "no line of the current declaration routes work to a tier it no longer names")
	assert.Empty(t, got.Details)
}

func TestModelTierFitHasNothingToSayWithoutADeclaration(t *testing.T) {
	r := tierFitRunner(t, nil, tierOutcomes("economy", types.JobKindCheck, types.ModelTierSourceDefault, "", 0, 20))

	got := r.checkModelTierFit(nil)

	assert.Equal(t, types.CheckOK, got.Status)
	assert.Equal(t, types.EvidenceUnknown, got.Evidence)
	assert.Empty(t, got.Details)
}
