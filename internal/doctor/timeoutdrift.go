package doctor

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/types"
)

const (
	// crowdingRatio is the share of a ceiling a recorded run may reach before the
	// declaration is reported. A run at 75% of its ceiling is one slow machine, one
	// cold cache, or one larger input away from failing, and a guard that starts
	// failing legitimate builds is worse than no guard: it teaches its author to raise
	// it without reading, which is how a ceiling stops meaning anything.
	crowdingRatio = 0.75

	// looseRatio is where a ceiling stops bounding anything a human would wait for. A
	// guard is SUPPOSED to sit well above the measurements - this repository's own
	// declarations run 7x and 20x their worst recorded run - so the threshold has to be
	// far enough out that a correctly written one never trips it. At a hundred times
	// the worst run on record, a hung target still holds its locks for most of a day.
	looseRatio = 100

	// measuredFloor is the shortest run worth reasoning about. Below it the noise in a
	// single measurement (a cold toolchain, a busy machine) is a large fraction of the
	// figure, and a ratio computed against it says more about the machine than the
	// declaration.
	measuredFloor = 2 * time.Second
)

// checkTimeoutDeclarations is MGS1032: a target whose declared timeout no longer
// matches the durations magus has recorded for it.
//
// The sibling of checkMemoryDeclarations, and it exists for the same reason: a
// declared ceiling is only as good as the evidence that it still describes the
// target, and a ceiling rots in both directions. A suite that grows into its ceiling
// starts failing builds that were fine; a ceiling left at a figure the target
// outgrew downward stops being a guard at all.
//
// Advice, never a failure. How much headroom a runaway guard should carry is a
// judgment about the worst machine the target will ever run on, which magus has not
// seen.
func (r *runner) checkTimeoutDeclarations(projects []*types.Project) types.DoctorCheck {
	const name = "timeout-declarations"

	var h forecast.History
	if err := h.Load(r.runCtx(), r.opts.cfg.HistoryPath); err != nil {
		// Advice, not OK: the check could not RUN, which is a different answer from
		// "everything agrees" and the reader needs to know which one they got.
		return types.DoctorCheck{
			Name: name, Status: types.DoctorAdvice,
			Message: "run history is unreadable, so declared timeouts could not be checked against what magus measured",
			Details: []string{err.Error()},
		}
	}

	var declared int
	var details []string
	for _, p := range projects {
		label := types.ProjectDisplayName(p.Path, p.Name, p.Dir)
		longest := measuredLongestRuns(&h, p.Path)
		for target, pol := range p.TargetPolicies {
			ceiling := pol.TimeoutDuration()
			if ceiling <= 0 {
				continue // undeclared is unbounded, on purpose; there is nothing to keep honest
			}
			declared++
			if d := gradeCeiling(label, target, pol.Timeout, ceiling, longest[target]); d != "" {
				details = append(details, d)
			}
		}
	}

	if declared == 0 {
		return types.DoctorCheck{
			Name: name, Status: types.DoctorOK,
			Message: "no target declares a timeout, so every target is unbounded",
		}
	}
	if len(details) == 0 {
		return types.DoctorCheck{
			Name: name, Status: types.DoctorOK,
			Message: fmt.Sprintf("%d declared timeout(s) still bracket what magus measured", declared),
		}
	}
	slices.Sort(details)
	details = slices.Compact(details)
	return types.DoctorCheck{
		Name:   name,
		Status: types.DoctorAdvice,
		Message: fmt.Sprintf(
			"%d of %d declared timeout(s) no longer describe the target; a ceiling is only a guard while it brackets real runs (see %s)",
			len(details), declared, types.CodeURL(types.TimeoutDeclarationDrift)),
		Details: details,
	}
}

// gradeCeiling renders the finding for one declared ceiling, or "" when it and the
// longest recorded run still bracket each other. A zero longest is a target that has
// never run under this history, which is silence rather than a finding.
//
// spelled is the magusfile's own text for the ceiling, not the parsed duration's
// String: the reader is being sent to edit a line, and "15m0s" is not the line.
func gradeCeiling(project, target, spelled string, ceiling, longest time.Duration) string {
	if longest < measuredFloor {
		return ""
	}
	switch {
	case float64(longest) >= float64(ceiling)*crowdingRatio:
		return fmt.Sprintf(
			"%s %s declares a %s timeout and has already run for %s; the next slow machine fails a build that was fine",
			project, target, spelled, longest.Round(time.Second))
	case ceiling > longest*looseRatio:
		return fmt.Sprintf(
			"%s %s declares a %s timeout and has never run longer than %s; a hang would hold its locks that long before anything noticed",
			project, target, spelled, longest.Round(time.Second))
	}
	return ""
}

// measuredLongestRuns returns the longest run recorded for each of a project's
// targets, keyed by the bare target name.
//
// The WORST run, not a percentile, and that is the whole point: a ceiling is a
// statement about the slowest case, so a p75 would argue for tightening a guard
// against exactly the runs it exists to survive.
//
// The history keys a target as "<spell>/<target>" because the unit that runs is a
// spell's implementation, while a magusfile declares against the bare name. One
// target served by two spells has two histories, and the ceiling has to cover both.
func measuredLongestRuns(h *forecast.History, project string) map[string]time.Duration {
	targets, ok := h.Projects[project]
	if !ok {
		return nil
	}
	out := map[string]time.Duration{}
	for key, st := range targets {
		name := key
		if i := strings.LastIndex(key, "/"); i >= 0 {
			name = key[i+1:]
		}
		for _, o := range st.RecentOutcomes {
			if d := time.Duration(o.DurationMs) * time.Millisecond; d > out[name] {
				out[name] = d
			}
		}
	}
	return out
}
