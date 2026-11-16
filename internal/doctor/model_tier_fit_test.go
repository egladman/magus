package doctor

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/check"
)

// tieredWorkspace is a workspace that declares model tiers.
type tieredWorkspace struct {
	types.WorkspaceReader
	set *tier.Set
}

func (w tieredWorkspace) ModelTiers(context.Context) *tier.Set { return w.set }

func declaredTiers(t *testing.T) *tier.Set {
	t.Helper()
	set, err := tier.NewSet(tier.Declaration{
		Enum:    "ModelTier",
		Site:    "magusfile.buzz:3",
		Cases:   []string{"economy", "standard"},
		Default: 1,
		Bounds:  []tier.Bounds{{}, {}},
		Skills:  []tier.SkillVariant{tier.SkillVariantFull, tier.SkillVariantShort},
		Hosts:   map[string][]string{"claude-code": {"haiku", "sonnet"}},
	})
	require.NoError(t, err)
	return set
}

func tierOutcomes(name string, kind types.JobKind, source tier.Source, reason string, passed, failed int) []types.JobOutcome {
	var out []types.JobOutcome
	for i := range passed + failed {
		state := types.StatePass
		if i >= passed {
			state = types.StateFail
		}
		o := types.JobOutcome{Job: fmt.Sprintf("%s-%s-%s-job-%d", name, kind, source, i), ModelTier: name, Kind: kind, Source: source, Reason: reason, State: state}
		if source == tier.SourceMatrix {
			o.Site = "magusfile.buzz:40"
		}
		out = append(out, o)
	}
	return out
}

func tierFitRunner(t *testing.T, set *tier.Set, outcomes ...[]types.JobOutcome) *runner {
	t.Helper()
	h := forecast.History{Version: forecast.HistoryVersion}
	for _, o := range outcomes {
		h.Jobs = append(h.Jobs, o...)
	}
	return &runner{
		opts: options{cfg: config.Config{HistoryPath: writeHistory(t, h)}},
		ws:   tieredWorkspace{set: set},
	}
}

func TestModelTierFitNamesTheLineThatRoutedAFailingTier(t *testing.T) {
	r := tierFitRunner(t, declaredTiers(t),
		tierOutcomes("economy", types.JobKindSymbol, tier.SourceMatrix, "small and checked", 2, 6),
		tierOutcomes("economy", types.JobKindSymbol, tier.SourceDefault, "the declared default", 1, 1),
		tierOutcomes("standard", types.JobKindCheck, tier.SourceDefault, "the declared default", 12, 0),
	)

	got := r.checkModelTierFit(t.Context(), nil)

	assert.Equal(t, check.Advice, got.Status)
	assert.Contains(t, got.Message, "1 of 2")
	require.Len(t, got.Details, 1)
	detail := got.Details[0]
	assert.Contains(t, detail, "economy on symbol jobs: 3 of 10 passed")
	assert.Contains(t, detail, "2 took the declared default: change the default in the model tier declaration at magusfile.buzz:3")
	assert.Contains(t, detail, `8 were routed here by the function registered with magus\modelTier at magusfile.buzz:40 (most often "small and checked")`)
}

func TestModelTierFitIsQuietUntilAPairHasEnoughJobs(t *testing.T) {
	r := tierFitRunner(t, declaredTiers(t),
		tierOutcomes("economy", types.JobKindSymbol, tier.SourceDefault, "", 0, modelTierFitJobs-1),
	)

	got := r.checkModelTierFit(t.Context(), nil)

	assert.Equal(t, check.OK, got.Status)
	assert.Empty(t, got.Details)
}

func TestModelTierFitKeepsATierThatPasses(t *testing.T) {
	r := tierFitRunner(t, declaredTiers(t),
		tierOutcomes("economy", types.JobKindCheck, tier.SourceMatrix, "small", 11, 1),
	)

	got := r.checkModelTierFit(t.Context(), nil)

	assert.Equal(t, check.OK, got.Status)
	assert.Contains(t, got.Message, "1 model tier and job kind pair(s)")
}

func TestModelTierFitSkipsATierTheDeclarationDropped(t *testing.T) {
	r := tierFitRunner(t, declaredTiers(t),
		tierOutcomes("retired", types.JobKindCheck, tier.SourceDefault, "", 0, 20),
	)

	got := r.checkModelTierFit(t.Context(), nil)

	assert.Equal(t, check.OK, got.Status, "no line of the current declaration routes work to a tier it no longer names")
	assert.Empty(t, got.Details)
}

func TestModelTierFitHasNothingToSayWithoutADeclaration(t *testing.T) {
	r := tierFitRunner(t, nil, tierOutcomes("economy", types.JobKindCheck, tier.SourceDefault, "", 0, 20))

	got := r.checkModelTierFit(t.Context(), nil)

	want := got
	want.Status, want.Evidence = check.OK, check.EvidenceUnknown
	assert.Equal(t, want, got)
	assert.Empty(t, got.Details)
}
