package guard

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/agent"
)

// unjudgedTool is the tool of a call that carries nothing a rule reads, "" when the host's
// tool rows list the tool or env is no call before a tool runs. A listed tool's call is
// judged by the rules for what the tool does, and one carrying none of it (a shell call
// with an empty command) has nothing to run. A finished call is past stopping, so it is
// never named.
func unjudgedTool(env hookEnvelope, tools agent.ToolRows) string {
	if env.ToolName == "" || env.ToolResponse != nil || tools.Capability(env.ToolName) != "" {
		return ""
	}
	return env.ToolName
}

// inertCall reports a call the read-only fast path answers with a pass before any rule
// loads: a tool the host's rows call inert, whose input carries nothing a rule reads and
// nothing the session records. Installed glue that names no host is left to Judge, which
// refuses it.
func inertCall(req Request) bool {
	if req.Observe || req.Message || req.Form != "" && strings.TrimSpace(req.Host) == "" {
		return false
	}
	env, ok := decodeHookEnvelope(req.Input, req.Tools)
	return ok && env.Inert
}

// denyRuleUnjudgedTool is a tool call no rule could read.
const denyRuleUnjudgedTool denyRuleName = "unjudged-tool"

// denyUnjudgedTool is unjudged-tool's refusal of a call to tool. With no tool rows to read,
// the refusal says so and names the install that writes them.
func denyUnjudgedTool(tool string, tools agent.ToolRows) denial {
	if tools.Err != nil {
		return denial{
			Say: fmt.Sprintf("magus workspace: no rule could judge this %s call: its input carries no command, file path, search, prompt or skill, "+
				"and no tool row says what %s does: %v.", tool, tool, tools.Err),
			Why: "Every tool call reaches the guard, and the host's tool rows say what a tool does where the shape of its input cannot. " +
				"A shell command is still judged by its shape, so the install that writes the rows runs.",
		}
	}
	return denial{
		Say: fmt.Sprintf("magus workspace: no rule could judge this %s call, and %s is in no tool row: use a tool the guard judges, or ask a person to add a row for %s to the harness spell's mgs_listTools.", tool, tool, tool),
		Why: "Every tool call reaches the guard, and the guard judges a call by the shape of its input: a command, file path, search, prompt or skill. A tool whose input has none of the shapes it reads " +
			"could do anything a rule exists to stop, and nothing would say so. A shell command or a file read or write is judged; a row for the tool in the harness spell's mgs_listTools says what it does. " +
			"Setting unjudged-tool to advise or off in magus\\guard.builtins, which allows it unjudged, is the person's call.",
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
