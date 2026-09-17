package guard

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// The multi-agent skill is the one piece of guidance a spawn cannot be written well
// without: it carries the lease model, the worktree rule, the model-naming rule, and the
// "never delegate git" boundary. All four are decisions the orchestrator makes BEFORE the
// child starts, and none of them can be corrected afterwards.
//
// agent.MustSkill, not a string literal: it resolves against the embedded catalog at init
// and panics on a name magus does not ship, so renaming the skill breaks the build rather
// than leaving this rule quietly unsatisfiable. A hardcoded name would deny every spawn
// forever, since no load could ever match it.
var multiAgentSkill = agent.MustSkill("magus-multi-agent")

// denySpawnWithoutBrief is the verdict for a spawn made before the multi-agent skill
// loaded, or "" when the session has loaded it.
//
// It grades SESSION STATE, never the prompt. That distinction is what lets this rule live
// on the spawn path at all: Evaluate passes every spawn unjudged because a handed-over
// prompt is prose, and a prompt that merely mentions a denied command would otherwise block
// the spawn describing it. Asking whether a file exists reads none of that prose.
//
// Fires once per session by construction: the first spawn is denied, the model loads the
// skill, the marker lands, and every later spawn passes. A session that never spawns never
// sees it.
func denySpawnWithoutBrief(markers hint.Gate, observesSkillLoads bool, workspace string) string {
	return denyUntilSkillLoaded(markers, observesSkillLoads, workspace, multiAgentSkill,
		"spawning",
		"It carries the lease, worktree, model-naming and git rules a spawn cannot be fixed for later.")
}

// adviseSharedCheckoutSpawn is the context a spawn carries when this checkout is ALREADY
// somebody's: the ids live here, the proof the new worker's lane does not collide with
// them, and the one move that makes the proof unnecessary.
//
// It advises and never denies, for the reason every spawn rule here does: the prompt is
// prose magus cannot read, so it cannot know whether the child is a second writer or a
// read-only reviewer, and a deny on a guess is a deny agents learn to route around. What
// it CAN say is that the checkout is shared and that nothing has checked the lanes, which
// is exactly the fact three workers sharing this tree never had in front of them.
//
// Fires once per session: the orchestrator handing out a wave is told at the first spawn,
// not at each of six.
func adviseSharedCheckoutSpawn(ctx context.Context, markers hint.Gate, at location) string {
	if at.cacheDir == "" {
		return ""
	}
	bound := job.BoundLeases(at.cacheDir)
	if len(bound) == 0 {
		return ""
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return "" // an unreadable plan is the guard's standing fail-open
	}
	var held []types.Job
	for _, row := range rows {
		if slices.Contains(bound, row.ID) && row.State.Live() && len(row.WritePaths) > 0 {
			held = append(held, row)
		}
	}
	if len(held) == 0 {
		return ""
	}
	ids := make([]string, 0, len(held))
	lane := map[string]bool{}
	for _, row := range held {
		ids = append(ids, row.ID)
		for _, p := range row.WritePaths {
			lane[p] = true
		}
	}
	union := make([]string, 0, len(lane))
	for p := range lane {
		union = append(union, p)
	}
	slices.Sort(union)
	return markers.OnceOrBrief(advisorySharedCheckout,
		fmt.Sprintf("This checkout already holds %s, still live and writing %s."+
			" Two workers in one checkout share every file in it, and the pair nobody survives is a magusfile,"+
			" magus.yaml or spell source: half-saved, it stops the workspace loading for everybody here at once."+
			"\n  Prove the lanes disjoint before you hand the work out: `%s`"+
			"\n  Or skip the proof by giving the new worker its own worktree, which is the answer whenever the"+
			" lanes touch workspace configuration.",
			strings.Join(ids, ", "), strings.Join(union, " "),
			hint.DescribeFile.With(append(union, "<the new lane>")...)),
		fmt.Sprintf("this checkout still holds %s: prove the lanes disjoint with `%s`, or give the new worker its own worktree",
			strings.Join(ids, ", "), hint.DescribeFile.With("<the union of the lanes>")))
}
