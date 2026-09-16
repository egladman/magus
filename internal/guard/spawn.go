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
// It was measured at ZERO loads across 2,147 session transcripts, under every name and
// every variant, while the two skills a hook demands loaded 415 times. That is the whole
// argument for this rule rather than better prose: this workspace has already established
// that a skill nobody is required to read is a skill nobody reads, and internal/guard/dir.go
// is the other worked example.
//
// agent.MustSkill, not a string literal: it resolves against the embedded catalog at init
// and panics on a name magus does not ship, so renaming the skill breaks the build rather
// than leaving this rule quietly unsatisfiable. A hardcoded name would deny every spawn
// forever, since no load could ever match it.
var multiAgentSkill = agent.MustSkill("magus-multi-agent")

// skillMarker keys a session marker on a skill NAME, so the guard can ask "has this
// session loaded that" without reading the host's transcript.
//
// A marker rather than a lookup in the host's session store: the store is per host, and
// the guard is forbidden from branching on host vocabulary. A marker is magus's own file,
// written by the same gate every advisory uses, and the host's only contribution is
// telling magus that a skill was loaded at all.
func skillMarker(name string) hint.MarkerKind {
	return hint.MarkerKind("skill-" + name)
}

// denySpawnWithoutBrief is the verdict for a spawn made before the multi-agent skill
// loaded, or "" when the session has loaded it.
//
// It grades SESSION STATE, never the prompt. That distinction is what lets this rule live
// on the spawn path at all: Evaluate passes every spawn unjudged because a handed-over
// prompt is prose, and a prompt that merely mentions a denied command would otherwise
// block the spawn describing it. Asking whether a file exists reads none of that prose.
//
// Fires once per session by construction: the first spawn is denied, the model loads the
// skill, the marker lands, and every later spawn passes. A session that never spawns never
// sees it.
// denySpawnWithoutBrief takes observesSkillLoads: whether the HOST wiring reports a skill
// load to this guard at all.
//
// It is the difference between a rule and a trap. Only a host that wires skill observation
// can ever satisfy this one, and the four harnesses magus ships do not agree: one matches a
// skill tool, one wires four other matchers, one uses its own event names, and one wires no
// hook config at all and drives a plugin instead. On the three that report nothing, a rule
// that denied anyway would deny EVERY spawn for the life of the session with no action the
// reader could take. So the capability is declared by the wiring that provides it, and the
// rule stands down wherever it is absent.
//
// Declared rather than detected, and never inferred from the host's NAME: the name is the
// host's vocabulary and guard code is forbidden it (TestNoHostSpecificBehaviorInCode), while
// a wiring that observes skill loads is the one thing that can honestly report that it does.
func denySpawnWithoutBrief(markers hint.Gate, observesSkillLoads bool) string {
	if !observesSkillLoads {
		return ""
	}
	if markers.Session() == "" {
		// No session pointer means no marker can be keyed, so every spawn in every
		// session would share one bucket: the first spawn anywhere denies and the rest
		// pass, which is worse than not asking. Hosts that report a session get the
		// rule; hosts that do not are unguarded here and say so in `magus doctor`.
		return ""
	}
	// Either copy clears it: the twins carry the same rules, so a session that read the
	// full one has read the brief. Only the SUGGESTION varies by lease.
	if markers.AlreadyFired(skillMarker(multiAgentSkill.String())) ||
		markers.AlreadyFired(skillMarker(agent.FullTwinName(multiAgentSkill.String()))) {
		return ""
	}
	// The SHORT name, always. The full twin is not installed everywhere (one shipped
	// harness asks for the short form alone), and naming a skill the reader cannot load is
	// the failure agent.MustSkill exists to prevent. Choosing between the twins needs the
	// host's install form, which belongs where the form is known rather than here.
	return "spawning before the " + multiAgentSkill.String() + " skill loaded.\n" +
		"It carries the lease, worktree, model-naming and git rules a spawn cannot be fixed for later.\n" +
		"Load Skill(" + multiAgentSkill.String() + "), then spawn."
}

// recordSkillLoad marks a skill as loaded for this session, and reports whether the
// session now carries that mark.
//
// NOT MarkFired's own return, which answers "was it already there": a second load of the
// same skill is an ordinary thing for a session to do, and reporting false for it would
// read as a failure to record.
func recordSkillLoad(markers hint.Gate, name string) bool {
	if name == "" || markers.Session() == "" {
		return false
	}
	markers.MarkFired(skillMarker(name))
	return markers.AlreadyFired(skillMarker(name))
}
