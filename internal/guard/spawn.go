package guard

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
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
func denySpawnWithoutBrief(markers hint.Gate, reportsSkills bool, workspace string) string {
	return denyUntilSkillLoaded(markers, reportsSkills, workspace, multiAgentSkill,
		"spawning",
		"It carries the lease, worktree, model-naming and git rules a spawn cannot be fixed for later.")
}

// adviseSharedCheckoutSpawn is the context a spawn carries when this checkout is ALREADY
// somebody's: the ids live here, the proof the new worker's write paths do not collide
// with them, and the one move that makes the proof unnecessary.
//
// It advises and never denies, for the reason every spawn rule here does: the prompt is
// prose magus cannot read, so it cannot know whether the child is a second writer or a
// read-only reviewer, and a deny on a guess is a deny agents learn to route around. What
// it CAN say is that the checkout is shared and that nothing has checked the write paths, which
// is exactly the fact three workers sharing this tree never had in front of them.
//
// Fires once per session: the orchestrator handing out a wave is told at the first spawn,
// not at each of six.
func adviseSharedCheckoutSpawn(ctx context.Context, markers hint.Gate, at location) advice {
	if at.cacheDir == "" || at.workspace == "" {
		return advice{}
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return advice{} // an unreadable plan is the guard's standing fail-open
	}
	held := job.HeldIn(rows, at.workspace)
	if len(held) == 0 {
		return advice{}
	}
	ids := make([]string, 0, len(held))
	declared := map[string]bool{}
	for _, row := range held {
		ids = append(ids, row.ID)
		for _, p := range row.WritePaths {
			declared[p] = true
		}
	}
	union := make([]string, 0, len(declared))
	for p := range declared {
		union = append(union, p)
	}
	slices.Sort(union)
	full := fmt.Sprintf("magus workspace: this checkout already holds %s, still live and writing %s; prove the write paths disjoint with `%s`, or give the new worker its own worktree.",
		strings.Join(ids, ", "), strings.Join(union, " "), hint.DescribeFile.With(append(union, "<the new write paths>")...))
	shown := markers.OnceOrBrief(advisorySharedCheckout, full,
		fmt.Sprintf("this checkout still holds %s: prove the write paths disjoint with `%s`, or give the new worker its own worktree",
			strings.Join(ids, ", "), hint.DescribeFile.With("<the union of the write paths>")))
	if shown != full {
		return advice{Say: shown}
	}
	return advice{
		Say: full,
		Why: "Two workers in one checkout share every file in it, and the pair nobody survives is a magusfile, magus.yaml or spell source: " +
			"half-saved, it stops the workspace loading for everybody here at once. " +
			"A worktree of its own skips the proof, and it is the answer whenever the write paths touch workspace configuration.",
	}
}

// spawnShapeKeys are the tool_input fields only a spawn carries. A message with none of
// them could be any tool's, so it is not read as a spawn.
var spawnShapeKeys = []string{"agent_type", "task_name", "fork_context", "fork_turns", "reasoning_effort", "model"}

// envelopeSpawn reads the prompt of a spawn by shape, and reports whether tool_input is one.
//
// `prompt` is the spelling most hosts use. Another host names it `message`, or sends
// `items` of text parts, and tells a spawn from a continuation by the absence of an
// addressee: a call with a target is [envelopeContinuation]'s.
func envelopeSpawn(input map[string]any) (prompt string, ok bool) {
	if p := envelopeString(input, "prompt"); p != "" {
		return p, true
	}
	if envelopeTarget(input) != "" {
		return "", false
	}
	_, hasItems := input["items"].([]any)
	text := cmp.Or(envelopeString(input, "message"), envelopeItemsText(input))
	if text == "" && !hasItems {
		return "", false
	}
	if !hasItems && !slices.ContainsFunc(spawnShapeKeys, func(k string) bool { _, present := input[k]; return present }) {
		return "", false
	}
	return text, true
}

// envelopeTarget is the agent a call addresses: `to` on one host, `target` on another.
func envelopeTarget(input map[string]any) string {
	return cmp.Or(envelopeString(input, "to"), envelopeString(input, "target"))
}

// envelopeContinuation reads a message to an agent that already exists, with the text it
// carries. A structured message has no text to hand a rule, so it yields "".
func envelopeContinuation(input map[string]any) (target, message string, ok bool) {
	target = envelopeTarget(input)
	if target == "" {
		return "", "", false
	}
	_, hasItems := input["items"].([]any)
	if input["message"] == nil && !hasItems {
		return "", "", false
	}
	return target, cmp.Or(envelopeString(input, "message"), envelopeItemsText(input)), true
}

// envelopeItemsText joins the text parts of an `items` list, the form a host uses to hand
// a prompt as parts rather than as one string. A part that is not text carries none.
func envelopeItemsText(input map[string]any) string {
	list, _ := input["items"].([]any)
	var parts []string
	for _, item := range list {
		fields, _ := item.(map[string]any)
		if text := envelopeString(fields, "text"); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// writeShapeKeys are the tool_input fields only a write carries. The envelope cannot tell
// a read from a write by tool, so a call holding any of them is never a read.
var writeShapeKeys = []string{
	"content", "contents", "old_string", "new_string", "old_str", "new_str", "edits",
	"replace_all", "patch", "patchText", "command", "text", "insert_text",
}

// envelopeSkillRead is the skill a call reads by opening its SKILL.md, or "".
//
// A host that loads a skill by reading the file has no skill tool to report it, so the
// read is the load. Only `.../skills/<name>/SKILL.md` counts, and only from a call that
// names nothing to write: a write to that path loads nothing.
func envelopeSkillRead(input map[string]any) string {
	for _, key := range writeShapeKeys {
		if _, present := input[key]; present {
			return ""
		}
	}
	rest, ok := strings.CutSuffix(filepath.ToSlash(envelopeWritePath(input)), "/SKILL.md")
	if !ok {
		return ""
	}
	dir, name := path.Split(rest)
	if !strings.HasSuffix("/"+dir, "/skills/") || !isSkillName(name) {
		return ""
	}
	return name
}
