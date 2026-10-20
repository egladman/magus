package job

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/ci/volatility"
	"github.com/egladman/magus/types"
)

// GroupingRecords folds outcomes into the grouping lens, one row per shape in chained,
// split, solo order, each present even with no job so an empty shape reads as unknown.
//
// Two or more outcomes forked under one attempt of one parent are a split group. Any other
// outcome is its own group: chained when its write paths reached two or more projects,
// solo when they reached at most one, and unclassified when it had write paths whose
// projects did not resolve, as every outcome recorded before history version 8 is.
//
// A non-nil projects keeps only the groups one of whose jobs wrote in one of them, and
// then counts no unclassified job, since none can be placed.
func GroupingRecords(outcomes []types.JobOutcome, projects []string) types.GroupingOutput {
	type parentAttempt struct {
		parent  string
		attempt int
	}
	siblings := map[parentAttempt][]types.JobOutcome{}
	var order []parentAttempt
	var alone []types.JobOutcome
	for _, o := range outcomes {
		if o.Parent == "" || o.ParentAttempt == 0 {
			alone = append(alone, o)
			continue
		}
		k := parentAttempt{o.Parent, o.ParentAttempt}
		if _, ok := siblings[k]; !ok {
			order = append(order, k)
		}
		siblings[k] = append(siblings[k], o)
	}

	out := types.GroupingOutput{Definition: types.GroupingDefinition, MinSample: types.GroupingMinSample}
	shapes := []types.GroupingShape{types.GroupingChained, types.GroupingSplit, types.GroupingSolo}
	folds := make(map[types.GroupingShape]*groupingFold, len(shapes))
	for _, shape := range shapes {
		folds[shape] = &groupingFold{rec: types.GroupingRecord{Shape: shape}}
	}
	keep := func(group []types.JobOutcome) bool {
		return projects == nil || slices.ContainsFunc(group, func(o types.JobOutcome) bool {
			return slices.ContainsFunc(o.Projects, func(p string) bool { return slices.Contains(projects, p) })
		})
	}
	for _, k := range order {
		group := siblings[k]
		if len(group) == 1 {
			alone = append(alone, group[0])
			continue
		}
		if keep(group) {
			folds[types.GroupingSplit].add(group)
		}
	}
	for _, o := range alone {
		group := []types.JobOutcome{o}
		switch {
		case len(o.Projects) >= 2:
			if keep(group) {
				folds[types.GroupingChained].add(group)
			}
		case o.WriteFiles > 0 && o.Projects == nil:
			if projects == nil {
				out.Unclassified++
			}
		default:
			if keep(group) {
				folds[types.GroupingSolo].add(group)
			}
		}
	}
	for _, shape := range shapes {
		out.Rows = append(out.Rows, folds[shape].record())
	}
	return out
}

// groupingFold accumulates one shape's groups for [GroupingRecords].
type groupingFold struct {
	rec                            types.GroupingRecord
	pass, denials                  int
	unattributed, rework, widening int
	setupTokens                    []int64
	setupContext, totalContext     int64
	groupContext, groupWall        []float64
}

func (f *groupingFold) add(group []types.JobOutcome) {
	f.rec.Groups++
	var groupContext int64
	contextKnown, wallKnown := true, true
	var start, end int64
	for i, o := range group {
		f.rec.Jobs++
		if o.State == types.StatePass {
			f.pass++
		}
		f.unattributed += o.Unattributed
		f.rework += o.ReworkRounds
		f.widening += o.Widenings
		if begun := o.Ended - o.DurationMs; i == 0 || begun < start {
			start = begun
		}
		end = max(end, o.Ended)
		wallKnown = wallKnown && o.DurationMs > 0
		w := o.Work
		if w == nil {
			contextKnown = false
			continue
		}
		f.rec.Known++
		f.denials += w.Denials
		if w.SetupTokens != nil {
			f.setupTokens = append(f.setupTokens, *w.SetupTokens)
		}
		if w.SetupContext != nil && w.ContextTokens != nil && *w.ContextTokens > 0 {
			f.setupContext += *w.SetupContext
			f.totalContext += *w.ContextTokens
		}
		if w.ContextTokens == nil {
			contextKnown = false
			continue
		}
		groupContext += *w.ContextTokens
	}
	if contextKnown {
		f.groupContext = append(f.groupContext, float64(groupContext))
	}
	if wallKnown {
		f.groupWall = append(f.groupWall, float64(end-start))
	}
}

// record is the shape's row. Below the minimum sample of groups, or with fewer than half
// its jobs carrying a work profile, every figure stays nil.
func (f *groupingFold) record() types.GroupingRecord {
	rec := f.rec
	if rec.Groups < types.GroupingMinSample || rec.Known*2 < rec.Jobs {
		rec.Verdict = types.GroupingUnknown
		return rec
	}
	rec.Verdict = types.GroupingMeasured
	jobs := float64(rec.Jobs)
	perJob := func(n int) *float64 {
		v := float64(n) / jobs
		return &v
	}
	passLower := volatility.WilsonLower(f.pass, rec.Jobs)
	rec.PassLower = &passLower
	if len(f.setupTokens) > 0 {
		mid := median(f.setupTokens)
		rec.SetupTokensMedian = &mid
	}
	if f.totalContext > 0 {
		share := float64(f.setupContext) / float64(f.totalContext)
		rec.SetupShare = &share
	}
	rec.ContextPerGroup = mean(f.groupContext)
	rec.WallMsPerGroup = mean(f.groupWall)
	denials := float64(f.denials) / float64(rec.Known)
	rec.DenialsPerJob = &denials
	rec.UnattributedPerJob = perJob(f.unattributed)
	rec.ReworkPerJob = perJob(f.rework)
	rec.WideningsPerJob = perJob(f.widening)
	return rec
}

// groupingLines renders the lens one shape a line, facts with their sample and never
// advice; a lens with no job at all is one line saying so.
func groupingLines(out types.GroupingOutput) []string {
	if !slices.ContainsFunc(out.Rows, func(r types.GroupingRecord) bool { return r.Jobs > 0 }) {
		return []string{"none recorded for these projects"}
	}
	lines := make([]string, 0, len(out.Rows))
	for _, r := range out.Rows {
		sample := fmt.Sprintf("%d groups of %d jobs, %d with a known trail", r.Groups, r.Jobs, r.Known)
		if r.Verdict != types.GroupingMeasured {
			lines = append(lines, fmt.Sprintf("%s: unknown, %s (fewer than %d groups, or most trails unknown)", r.Shape, sample, out.MinSample))
			continue
		}
		facts := []string{sample}
		if r.ContextPerGroup != nil {
			facts = append(facts, fmt.Sprintf("%.0f context tokens per group", *r.ContextPerGroup))
		}
		if r.WallMsPerGroup != nil {
			facts = append(facts, fmt.Sprintf("%s wall per group", (time.Duration(*r.WallMsPerGroup)*time.Millisecond).Round(time.Second)))
		}
		if r.SetupTokensMedian != nil {
			facts = append(facts, fmt.Sprintf("median %d tokens at the first write", *r.SetupTokensMedian))
		}
		if r.SetupShare != nil {
			facts = append(facts, fmt.Sprintf("%.0f%% of context spent before it", *r.SetupShare*100))
		}
		facts = append(facts,
			fmt.Sprintf("%.1f denials and %.1f unattributed writes per job", *r.DenialsPerJob, *r.UnattributedPerJob),
			fmt.Sprintf("pass rate at least %.0f%%", *r.PassLower*100))
		lines = append(lines, fmt.Sprintf("%s: %s", r.Shape, strings.Join(facts, "; ")))
	}
	return lines
}

func mean(xs []float64) *float64 {
	if len(xs) == 0 {
		return nil
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	m := sum / float64(len(xs))
	return &m
}

// GroupingLens is [GroupingRecords] over the outcomes the workspace's history file holds.
// A workspace with no history file configured has no outcome, so every shape is unknown.
func (s *Store) GroupingLens(ctx context.Context, projects []string) (types.GroupingOutput, error) {
	outcomes, err := s.jobHistory(ctx)
	if err != nil {
		return types.GroupingOutput{}, err
	}
	return GroupingRecords(outcomes, projects), nil
}
