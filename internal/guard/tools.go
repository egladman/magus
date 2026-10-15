package guard

import (
	"fmt"
	"strconv"
	"strings"
)

// toolCapability is what a host tool can do to a workspace, in magus's own words. The
// shape of a call's input decides how it is judged (a command field is a command line, a
// file path is a read or a write); the capability settles what shape cannot, and says
// which tools carry nothing to judge on purpose.
type toolCapability string

const (
	toolRuns   toolCapability = "runs"
	toolReads  toolCapability = "reads"
	toolWrites toolCapability = "writes"
	toolSpawns toolCapability = "spawns"
	// toolInert touches no workspace file and runs nothing: a plan, a todo list, a web
	// lookup, a read of a background task's output.
	toolInert toolCapability = "inert"
)

// knownTools is the one table of host tool names the guard reads. Every harness spell
// wires every tool to the guard, so the guard must tell a tool that has nothing to judge
// from one it cannot judge, and a file read from a path-only write such as a delete. The
// names are each host's own; a name missing here is judged by shape alone, and when shape
// finds nothing the unjudged-tool rule names it.
//
// Here and not behind the wiring's flags: a catch-all hook entry cannot say per tool what
// the tool does. Decided 2026-10-14 with the hook coverage inversion; see
// docs/guides/integrations/agents/guard.md, "Every tool reaches the guard".
//
//nolint:hostvocab // the decision is written down in the guard guide section named above
var knownTools = map[string]toolCapability{
	// Claude Code.
	"Bash":                 toolRuns,
	"Monitor":              toolRuns,
	"Read":                 toolReads,
	"NotebookRead":         toolReads,
	"Glob":                 toolReads,
	"Grep":                 toolReads,
	"LS":                   toolReads,
	"Write":                toolWrites,
	"Edit":                 toolWrites,
	"MultiEdit":            toolWrites,
	"NotebookEdit":         toolWrites,
	"Agent":                toolSpawns,
	"Task":                 toolSpawns,
	"SendMessage":          toolSpawns,
	"BashOutput":           toolInert,
	"KillShell":            toolInert,
	"TaskOutput":           toolInert,
	"TaskStop":             toolInert,
	"TodoWrite":            toolInert,
	"WebFetch":             toolInert,
	"WebSearch":            toolInert,
	"ExitPlanMode":         toolInert,
	"EnterPlanMode":        toolInert,
	"AskUserQuestion":      toolInert,
	"Skill":                toolInert,
	"ToolSearch":           toolInert,
	"ScheduleWakeup":       toolInert,
	"CronCreate":           toolInert,
	"ListMcpResourcesTool": toolInert,
	"ReadMcpResourceTool":  toolInert,
	"SubagentHandback":     toolInert,
	"ListAgents":           toolInert,
	"ListSkills":           toolInert,
	"SearchSkills":         toolInert,
	"ListPlugins":          toolInert,
	"SearchPlugins":        toolInert,
	// Codex.
	"apply_patch":              toolWrites,
	"spawn_agent":              toolSpawns,
	"collaborationspawn_agent": toolSpawns,
	"close_agent":              toolInert,
	"update_plan":              toolInert,
	"view_image":               toolInert,
	"web_search":               toolInert,
	// Cursor.
	"Shell":      toolRuns,
	"StrReplace": toolWrites,
	"Delete":     toolWrites,
	// OpenCode.
	"bash":      toolRuns,
	"read":      toolReads,
	"glob":      toolReads,
	"grep":      toolReads,
	"list":      toolReads,
	"write":     toolWrites,
	"edit":      toolWrites,
	"patch":     toolWrites,
	"task":      toolSpawns,
	"todowrite": toolInert,
	"todoread":  toolInert,
	"webfetch":  toolInert,
	"skill":     toolInert,
}

// capabilityOf is the table's capability for tool, "" for a tool it does not name.
func capabilityOf(tool string) toolCapability {
	return knownTools[tool]
}

// unjudgedTool is the tool of a call that carries nothing a rule reads, "" when the table
// knows the tool or env is no call before a tool runs. A known tool's call is judged by the
// rules for what the tool does, and one carrying none of it (a shell call with an empty
// command) has nothing to run. A finished call is past stopping, so it is never named.
func unjudgedTool(env hookEnvelope) string {
	if env.ToolName == "" || env.ToolResponse != nil || capabilityOf(env.ToolName) != "" {
		return ""
	}
	return env.ToolName
}

// inertCall reports a call the read-only fast path answers with a pass before any rule
// loads: a tool the table calls inert, whose input carries nothing a rule reads and
// nothing the session records. Installed glue that names no host is left to Judge, which
// refuses it.
func inertCall(req Request) bool {
	if req.Observe || req.Message || req.Form != "" && strings.TrimSpace(req.Host) == "" {
		return false
	}
	env, ok := decodeHookEnvelope(req.Input)
	return ok && env.Inert
}

// denyRuleUnjudgedTool is a tool call no rule could read.
const denyRuleUnjudgedTool denyRuleName = "unjudged-tool"

// denyUnjudgedTool is unjudged-tool's refusal of a call to tool.
func denyUnjudgedTool(tool string) denial {
	return denial{
		Say: fmt.Sprintf("magus guard: no rule could judge this %s call: its input carries no command, file path, search, prompt or skill, "+
			"and %s is not a tool the guard knows. Do this step with a tool the guard judges (a shell command, a file read or write), "+
			"or ask the person you work for whether this workspace should allow %s unjudged.", tool, tool, tool),
		Why: "Every tool call reaches the guard, and the guard judges a call by the shape of its input. A tool whose input has none of the shapes it reads " +
			"could do anything a rule exists to stop, and nothing would say so. Setting unjudged-tool to advise or off in magus\\guard.builtins is the person's call.",
	}
}

// wholeReadLines is the read-tool limit past which a read is the whole file, as the
// read-navigation rule counts it.
const wholeReadLines = 300

// readLine is the shell line a read tool's call stands for: a sed range over its offset
// and limit, or a cat of the whole file. The read rules then judge it as the command it
// replaces.
func readLine(file string, input map[string]any) string {
	word := shellWord(file)
	limit := envelopeInt(input, "limit")
	first := max(envelopeInt(input, "offset"), 1)
	switch {
	case limit > 0 && limit <= wholeReadLines:
		return "sed -n " + strconv.Itoa(first) + "," + strconv.Itoa(first+limit-1) + "p " + word
	case first > 1:
		return "sed -n " + strconv.Itoa(first) + ",$p " + word
	}
	return "cat " + word
}

// envelopeInt reads one tool_input number, 0 when it is absent or not a whole number. A
// JSON number decodes as float64; a host that sends one as a string is read too.
func envelopeInt(input map[string]any, key string) int {
	switch v := input[key].(type) {
	case float64:
		if v == float64(int(v)) {
			return int(v)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return 0
}
