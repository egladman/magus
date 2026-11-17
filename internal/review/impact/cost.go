package impact

import (
	"fmt"
	"time"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/text/plural"
	"github.com/egladman/magus/types/impact"
)

// costEstimate is the estimated cost of rebuilding the reach, from recorded run durations.
type costEstimate struct {
	TotalMs  int64         `json:"total_ms"           yaml:"total_ms"`
	Projects []projectCost `json:"projects,omitempty" yaml:"projects,omitempty"`
}

type projectCost struct {
	Project string `json:"project" yaml:"project"`
	Target  string `json:"target"  yaml:"target"`
	Ms      int64  `json:"ms"      yaml:"ms"`
	// Samples is how many runs the estimate rests on, carried so a reader can weigh it. An
	// estimate from three runs and one from three hundred print the same duration otherwise.
	Samples int     `json:"samples"           yaml:"samples"`
	HitRate float64 `json:"hit_rate,omitempty" yaml:"hit_rate,omitempty"`
}

// costTargets is the target a project's rebuild is estimated by, most representative
// first. ci is the whole gate; test is the bulk of it where no ci target is declared.
var costTargets = []string{"ci", "test"}

// minSamples mirrors the tier-3 gate inside forecast.resolvePrediction: below it the
// prediction is the workspace fallback rather than a measurement of this project, and printing
// that as an estimate would be inventing a number.
const minSamples = 3

// estimateCost sums the recorded history's per-project prediction over the reach.
//
// nil when the history has nothing to say about ANY project in reach. That case matters more
// than the happy one: forecast falls back to a workspace-wide default and then to a compiled-in
// constant, so a total is always computable and a total computed from those is a fabrication
// wearing a duration. Only projects with enough samples for forecast's own project tier are
// counted, and a reach whose projects all fall short reports no history at all.
func estimateCost(h *forecast.History, affected []impact.Project) *costEstimate {
	c := &costEstimate{}
	for _, p := range affected {
		target, stats, ok := costTarget(h, p)
		if !ok {
			continue
		}
		// Tags are derived from the changed files inside the project, which is what selects
		// forecast's per-subdirectory bucket over the project-wide percentile. A transitively
		// affected project has none, and Tags answers "transitive" for it.
		d := h.PredictDuration(p.Path, target, forecast.Tags(p.Path, p.Files))
		c.TotalMs += d.Milliseconds()
		c.Projects = append(c.Projects, projectCost{
			Project: p.Path,
			Target:  target,
			Ms:      d.Milliseconds(),
			Samples: stats.Samples,
			HitRate: stats.HitRate,
		})
	}
	if len(c.Projects) == 0 {
		return nil
	}
	return c
}

// costTarget picks the target a project's rebuild is estimated by, and reports false
// when the history has no measurement of this project worth quoting.
func costTarget(h *forecast.History, p impact.Project) (string, forecast.Stats, bool) {
	targets, ok := h.Projects[p.Path]
	if !ok {
		return "", forecast.Stats{}, false
	}
	for _, name := range costTargets {
		s, timed := targets[name]
		if timed && s.Samples >= minSamples && s.P75Ms > 0 {
			return name, s, true
		}
	}
	return "", forecast.Stats{}, false
}

func costLines(c *costEstimate) []string {
	if c == nil {
		return []string{
			"COST: no run history yet, so there is nothing to estimate from",
			fmt.Sprintf("      Run `%s` once and the next impact can price this.", hint.Affected.With("ci")),
		}
	}
	out := []string{fmt.Sprintf(
		"COST: ~%s to rebuild the reach (history-based estimate: the p75 of past runs, discounted by the cache hit rate they recorded)",
		durationText(c.TotalMs))}
	for _, p := range capList(c.Projects) {
		line := fmt.Sprintf("      %s %s ~%s (%d run%s)",
			p.Project, p.Target, durationText(p.Ms), p.Samples, plural.Word(p.Samples, "", "s"))
		if p.HitRate > 0 {
			line += fmt.Sprintf(", %d%% cache hits", int(p.HitRate*100+0.5))
		}
		out = append(out, line)
	}
	return append(out, moreLine(len(c.Projects))...)
}

// durationText renders an estimate at the precision it deserves. Seconds is the floor:
// this is a p75 of past runs, and a millisecond figure would claim an accuracy it has not got.
func durationText(ms int64) string {
	d := (time.Duration(ms) * time.Millisecond).Round(time.Second)
	if d < time.Second {
		return "<1s"
	}
	return d.String()
}
