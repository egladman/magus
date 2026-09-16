package guard

import (
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
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
func denySpawnWithoutBrief(markers hint.Gate, observesSkillLoads bool) string {
	return denyUntilSkillLoaded(markers, observesSkillLoads, multiAgentSkill,
		"spawning",
		"It carries the lease, worktree, model-naming and git rules a spawn cannot be fixed for later.")
}
