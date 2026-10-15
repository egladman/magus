package guard

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
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

// pendingSpawnPrefix starts the kind of a spawn that passed and has not yet been joined to
// the child it started, named by when it was judged.
const pendingSpawnPrefix = "spawn-pending-"

// pendingSpawnJoined ends the kind marking a pending spawn as joined to its child.
const pendingSpawnJoined = "-joined"

// pendingSpawnWindow is how long after its spawn a child's start is still paired with it.
// A host starts the child as the spawn runs, so a start later than this is another spawn's.
const pendingSpawnWindow = 2 * time.Minute

// pendingSpawn is what a spawn handed its child, kept until the child's start names it.
type pendingSpawn struct {
	Record  spawnedAgent `json:"record"`
	Spawner string       `json:"spawner,omitempty"`
}

// recordPendingSpawn files a spawn that passed, for the start of the child it makes to be
// joined to it. A host whose finished spawn already names the child never reads it.
func recordPendingSpawn(facts hint.Gate, env hookRequest, spawner string, now time.Time) {
	body, err := json.Marshal(pendingSpawn{
		Record:  spawnedAgent{Description: env.Spawn.Description, Name: env.Spawn.Name, Model: env.DeclaredModel},
		Spawner: spawner,
	})
	if err != nil {
		return
	}
	writeAgentMarker(facts, hint.MarkerKind(pendingSpawnPrefix+strconv.FormatInt(now.UnixNano(), 10)), body)
}

// pendingSpawnEntry is one pending spawn still waiting for its child, with the marker kind
// it is filed under.
type pendingSpawnEntry struct {
	kind  hint.MarkerKind
	spawn pendingSpawn
}

// livePendingSpawns lists, oldest first, the spawns of this session that no child has joined
// and that are not older than pendingSpawnWindow.
func livePendingSpawns(facts hint.Gate, now time.Time) []pendingSpawnEntry {
	var out []pendingSpawnEntry
	for _, kind := range facts.Kinds(pendingSpawnPrefix) {
		if strings.HasSuffix(string(kind), pendingSpawnJoined) {
			continue
		}
		nanos, err := strconv.ParseInt(strings.TrimPrefix(string(kind), pendingSpawnPrefix), 10, 64)
		if err != nil || now.Sub(time.Unix(0, nanos)) > pendingSpawnWindow {
			continue
		}
		body, err := os.ReadFile(hint.MarkerPath(facts.CacheDir(), facts.Session(), kind))
		var p pendingSpawn
		if err != nil || json.Unmarshal(body, &p) != nil {
			continue
		}
		out = append(out, pendingSpawnEntry{kind: kind, spawn: p})
	}
	return out
}

// joinStartedChild pairs a child whose start names only its own id with the oldest spawn
// in this session still waiting for one, and files the child under that spawn as a
// finished spawn call would: its title's job, its name and its model. A host whose spawn
// is answered by a name, and whose child's own calls carry an id, joins the two here.
// Nothing is joined to a child magus already has a record of, or to a spawn older than
// pendingSpawnWindow.
//
// A host that sends no start for a foreground child has only the child's first call to join
// it by, which names no spawn, and several foreground spawns can wait at once. That join
// (firstCall) guesses nothing: it takes the one pending spawn whose title names a live job
// equal to the lease the caller declares (see [trail.LeaseFromEnv]). When spawns are waiting
// and the declaration matches none, or more than one, nothing is joined and the returned
// refusal says why and what to declare. It is empty when the child was joined, was already
// known, or no spawn naming a job was waiting.
func joinStartedChild(ctx context.Context, at location, facts hint.Gate, who hookAttribution, child string, now time.Time, firstCall bool) (refusal string) {
	if _, known := readSpawnedAgent(facts, child); known {
		return ""
	}
	pending := livePendingSpawns(facts, now)
	join := func(e pendingSpawnEntry) {
		if facts.MarkFired(e.kind + pendingSpawnJoined) {
			return
		}
		recordSpawnedAgent(ctx, at, facts, who, hookRequest{
			SpawnedAgent:  child,
			DeclaredModel: e.spawn.Record.Model,
			Spawn:         spawnFields{Description: e.spawn.Record.Description, Name: e.spawn.Record.Name},
		}, e.spawn.Spawner)
	}
	if !firstCall {
		if len(pending) > 0 {
			join(pending[0])
		}
		return ""
	}
	var titled, matched []pendingSpawnEntry
	var jobs []string
	claim := trail.LeaseFromEnv()
	for _, e := range pending {
		row, ok := spawnTitleJob(ctx, at, e.spawn.Record.Description)
		if !ok {
			continue
		}
		titled = append(titled, e)
		jobs = append(jobs, row.ID)
		if claim != "" && row.ID == claim {
			matched = append(matched, e)
		}
	}
	switch {
	case len(titled) == 0:
		return ""
	case len(matched) == 1:
		join(matched[0])
		return ""
	case len(matched) > 1:
		return fmt.Sprintf("magus workspace: this worker's first call matches %d waiting spawns of job %s, and magus will not guess between them: "+
			"wait for one spawn of that job to be joined, or title the spawns for different jobs.", len(matched), claim)
	case claim == "":
		return fmt.Sprintf("magus workspace: this worker's first call declares no lease, and %d spawn(s) are waiting for a worker (%s), so magus cannot tell which is its own: "+
			"declare the job it works as `%s=<job>` in its environment.", len(titled), strings.Join(jobs, ", "), envHookLease)
	}
	return fmt.Sprintf("magus workspace: this worker declares lease %s, and none of the %d spawn(s) waiting for a worker names that job (%s): "+
		"declare the job its spawn title names as `%s=<job>` in its environment.", claim, len(titled), strings.Join(jobs, ", "), envHookLease)
}

// scheduleShapeKeys are the tool_input fields of a call that queues a prompt for a later
// turn of the same session: a delay, or a cron expression.
var scheduleShapeKeys = []string{"cron", "delaySeconds", "delay_seconds"}

// envelopeSchedules reports a prompt queued for later in this session, which carries a
// prompt as a spawn does and briefs no new agent.
func envelopeSchedules(input map[string]any) bool {
	return envelopeString(input, "prompt") != "" && slices.ContainsFunc(scheduleShapeKeys, func(k string) bool {
		_, present := input[k]
		return present
	})
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
