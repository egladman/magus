package impact

import (
	"fmt"

	"github.com/egladman/magus/libs/text/plural"
	difftypes "github.com/egladman/magus/types/diff"
)

// reachSummary is the blast radius: what was edited, and what rebuilds because of it.
type reachSummary struct {
	Seeds    int              `json:"seeds"              yaml:"seeds"`
	Rebuilds int              `json:"rebuilds"           yaml:"rebuilds"`
	Projects []reachedProject `json:"projects,omitempty" yaml:"projects,omitempty"`
}

type reachedProject struct {
	Path  string `json:"path"            yaml:"path"`
	Seed  bool   `json:"seed"            yaml:"seed"`
	Files int    `json:"files,omitempty" yaml:"files,omitempty"`
}

// computeReach renders what types.Diff has carried since the impact join landed and
// nothing has ever printed: which projects were edited, and which merely rebuild.
//
// nil when the closure is empty, which is a real state: a change entirely outside every
// project directory seeds nothing.
func computeReach(rev difftypes.Diff) *reachSummary {
	if len(rev.AffectedProjects) == 0 {
		return nil
	}
	r := &reachSummary{Seeds: len(rev.SeedProjects), Rebuilds: len(rev.AffectedProjects)}
	for _, p := range rev.AffectedProjects {
		r.Projects = append(r.Projects, reachedProject{Path: p.Path, Seed: p.Seed, Files: len(p.Files)})
	}
	return r
}

func reachLines(r *reachSummary) []string {
	if r == nil {
		return []string{"REACH: no project contains a changed file, so nothing rebuilds"}
	}
	out := []string{fmt.Sprintf("REACH: %d project%s edited, %d project%s rebuild",
		r.Seeds, plural.Word(r.Seeds, "", "s"), r.Rebuilds, plural.Word(r.Rebuilds, "", "s"))}
	for _, p := range capList(r.Projects) {
		if p.Seed {
			out = append(out, fmt.Sprintf("      %s - edited, %d file%s", p.Path, p.Files, plural.Word(p.Files, "", "s")))
			continue
		}
		out = append(out, fmt.Sprintf("      %s - rebuilds because it depends on one that was", p.Path))
	}
	return append(out, moreLine(len(r.Projects))...)
}
