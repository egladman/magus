package doctor

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

const (
	// modelTierFitJobs is how many recorded outcomes a (tier, job kind) pair needs before it
	// is judged: a handful of failures is a bad week, not a verdict about a model.
	modelTierFitJobs = 10
	// modelTierFitFloor is the Wilson lower bound of the pass rate below which a pair is
	// reported. The lower bound, not the rate, so a pair is reported only when the record
	// is bad enough to be unlikely to be luck.
	modelTierFitFloor = 0.5
)

// modelTiered is the workspace's model tier declaration, which the workspace the real
// loader returns carries and a bare WorkspaceReader does not.
type modelTiered interface {
	ModelTiers(ctx context.Context) *types.ModelTierSet
}

// checkModelTierFit reports each (model tier, job kind) pair whose recorded jobs mostly did
// not pass, and names the line a person would change: the declared default when the default
// chose the tier, the function registered with magus\job.modelTier when the matrix did, the
// job's own model when a person named it.
//
// It only reports. A tier that fails one kind of job may be right for every other, and
// moving work is a decision about cost the history cannot make, so nothing here edits a
// declaration and nothing re-routes a job. Advice, never a failure.
func (r *runner) checkModelTierFit(_ []*types.Project) types.Check {
	const name = "model-tier-fit"

	tiered, ok := r.ws.(modelTiered)
	if !ok || tiered.ModelTiers(r.runCtx()) == nil {
		return types.Check{
			Name:     name,
			Status:   types.CheckOK,
			Evidence: types.EvidenceUnknown,
			Message:  "this workspace declares no model tiers, so no job is routed by one",
		}
	}
	set := tiered.ModelTiers(r.runCtx())

	var h forecast.History
	if err := h.Load(r.runCtx(), r.opts.cfg.HistoryPath); err != nil {
		return historyUnreadableCheck(name, err)
	}

	records := job.ModelTierRecords(h.Jobs, set)
	var judged int
	var details []string
	for _, rec := range records {
		if rec.Rank < 0 || rec.Jobs < modelTierFitJobs {
			continue
		}
		judged++
		if rec.PassLower >= modelTierFitFloor {
			continue
		}
		details = append(details, modelTierFitDetail(rec, set, h.Jobs))
	}

	switch {
	case judged == 0:
		return types.Check{
			Name:     name,
			Status:   types.CheckOK,
			Evidence: types.EvidenceMeasured,
			Message:  fmt.Sprintf("no model tier has %d recorded jobs of one kind yet, so none is judged", modelTierFitJobs),
		}
	case len(details) == 0:
		return types.Check{
			Name:     name,
			Status:   types.CheckOK,
			Evidence: types.EvidenceMeasured,
			Message:  fmt.Sprintf("%d model tier and job kind pair(s) pass often enough to keep their tier", judged),
		}
	}
	return types.Check{
		Name:     name,
		Status:   types.CheckAdvice,
		Evidence: types.EvidenceMeasured,
		Message: fmt.Sprintf("%d of %d model tier and job kind pair(s) passed too rarely to trust at that tier; magus changes none of them",
			len(details), judged),
		Details: details,
	}
}

// modelTierFitDetail is one pair's finding: how it did, what it cost, and the line to edit.
func modelTierFitDetail(rec types.ModelTierRecord, set *types.ModelTierSet, outcomes []types.JobOutcome) string {
	lead := fmt.Sprintf("%s on %s jobs: %d of %d passed (pass rate lower bound %.2f), %.1f check(s), %.1f rework round(s) and %.1f widening(s) per job",
		rec.ModelTier, rec.Kind, rec.Pass, rec.Jobs, rec.PassLower, rec.ChecksPerJob, rec.ReworkPerJob, rec.WideningsPerJob)

	chose := map[types.ModelTierSource]int{}
	reasons, sites := map[string]int{}, map[string]int{}
	for _, o := range outcomes {
		if o.ModelTier != rec.ModelTier || o.Kind != rec.Kind || !o.State.Terminal() {
			continue
		}
		chose[o.Source]++
		if o.Source == types.ModelTierSourceMatrix {
			reasons[o.Reason]++
			if o.Site != "" {
				sites[o.Site]++
			}
		}
	}
	var fixes []string
	if n := chose[types.ModelTierSourceDefault]; n > 0 {
		fixes = append(fixes, fmt.Sprintf("%d took the declared default: change the default in the model tier declaration at %s", n, set.Site()))
	}
	if n := chose[types.ModelTierSourceMatrix]; n > 0 {
		matrix := "the function registered with magus\\job.modelTier"
		if site := commonest(sites); site != "" {
			matrix += " at " + site
		}
		fixes = append(fixes, fmt.Sprintf("%d were routed here by %s (most often %q): route %s jobs to another tier there",
			n, matrix, commonest(reasons), rec.Kind))
	}
	if n := chose[types.ModelTierSourceRow]; n > 0 {
		fixes = append(fixes, fmt.Sprintf("%d named the tier or model themselves: fork them with another --model", n))
	}
	if len(fixes) == 0 {
		return lead
	}
	return lead + "; " + strings.Join(fixes, "; ")
}

// commonest is the key of counts held most often, the lexically first on a tie so the
// finding does not change between runs.
func commonest(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int { return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a, b)) })
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}
