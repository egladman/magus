package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/hint"
)

// The MCP surface: how a call to one of magus's own tools becomes a line the command
// rules read, and how those rules read it back. Writer and reader in one file, because a
// rendering and a parse that disagree is a rule judging something nobody sent.

// mcpJudgedParams are the tool parameters a guard rule reads, in the order they render.
//
// Every field ledger.Merge applies, plus the two that name the call. A key this list omits
// reaches the row with no rule having seen it, which is how a bound worker rewrote the
// checkpoint its own work is graded against; TestMCPJudgedParamsCoverEveryMergedField holds
// the two sides together.
var mcpJudgedParams = append([]string{
	"op", "id", "owned_paths", "forbidden_paths", "focus", "depends_on",
	"validation", "read_only", "parent", "state", "checkpoint", "tier", "goal",
}, mcpRenamedParams...)

// mcpRenamedParams are the row's parameters under their other spelling.
//
// compat(until: no ledger door accepts the spellings above any more; observe it by calling
// ledger.Merge with each of those names and finding it rejected): both vocabularies are
// judged for one cycle, so a put cannot dodge a rule by picking the word on whichever side
// of the rename the guard has not learned yet.
var mcpRenamedParams = []string{"write_paths", "read_paths", "deny_paths", "model", "check"}

// The two spellings of the one list a bound caller may shrink. Both are named here rather
// than spelled at each use so the rebind rule and the renderer cannot learn one of them.
const (
	ownedPathsParam        = "owned_paths"
	ownedPathsRenamedParam = "write_paths"
)

// mcpElidedParams render as a presence marker instead of their value. No rule reads this
// one, and a goal is free prose: the rendered line is recorded in the activity trail, so
// copying it there would put a caller's sentences into an audit record shaped like a
// command. Presence is all the rebind rule needs, since naming it at all is a rewrite.
var mcpElidedParams = map[string]bool{"goal": true}

// mcpElidedValue stands in for an elided value. A word rather than an empty string: an
// empty value is how the merge spells an explicit clear, and the two must not render alike.
const mcpElidedValue = "..."

// mcpCLIEquivalent is the CLI command a magus MCP tool is the other door to.
type mcpCLIEquivalent struct {
	command hint.Command
	// operands are the tool parameters that render as positional arguments, in order.
	operands []string
	// flags are the fixed flags that make the rendering the same work the tool does, so
	// a REPORT about the gate does not render as a run of it.
	flags []string
}

// mcpCLIEquivalents route each magus tool to the command line that does the same thing, so
// the rules already written for the CLI judge the tool call rather than a second copy of
// them being written for MCP.
//
// magus_insight and magus_ledger are absent for opposite reasons: nothing in internal/hint
// spells `insight`, so there is no command to render; the ledger tool is judged on its
// PARAMETERS by the rebind rule, which is the one rule that reads an MCP call directly.
var mcpCLIEquivalents = map[hint.ToolName]mcpCLIEquivalent{
	hint.ToolRunTarget:       {command: hint.Run, operands: []string{"target", "projects"}},
	hint.ToolRunAffected:     {command: hint.Affected, operands: []string{"target"}},
	hint.ToolAffectedPlan:    {command: hint.Affected, operands: []string{"target"}, flags: []string{"--plan"}},
	hint.ToolAffectedExplain: {command: hint.Affected, operands: []string{"project"}, flags: []string{"--explain"}},
	hint.ToolVCSCheckpoint:   {command: hint.VCSCheckpoint},
	hint.ToolQuery:           {command: hint.Query, operands: []string{"query"}},
	hint.ToolExplain:         {command: hint.Explain, operands: []string{"node"}},
	hint.ToolRefs:            {command: hint.Refs, operands: []string{"symbol"}},
	hint.ToolPath:            {command: hint.Path, operands: []string{"from", "to"}},
	hint.ToolDescribe:        {command: hint.Describe, operands: []string{"kind", "name"}},
	hint.ToolDescribeFile:    {command: hint.DescribeFile, operands: []string{"paths"}},
	hint.ToolWhere:           {command: hint.Where, operands: []string{"filter"}},
	hint.ToolOutput:          {command: hint.QueryOutput, operands: []string{"ref"}},
	hint.ToolStats:           {command: hint.GraphStats},
	hint.ToolDiff:            {command: hint.Diff},
	hint.ToolDoctor:          {command: hint.Doctor},
	hint.ToolStatus:          {command: hint.Status},
	hint.ToolConfigGet:       {command: hint.ConfigView},
}

// renderMCPCall normalizes an MCP call to a magus tool into a command line.
//
// Three shapes, in the order they are decided. The ledger tool renders `<tool> key=value`,
// because its parameters ARE what the rebind rule judges. A tool with a CLI equivalent
// renders that argv, so the command rules read it as the work it is. Anything else renders
// its bare tool name: nothing judges it, and the activity trail still records that it
// happened.
//
// Rendering and re-parsing rather than handing the rules a map keeps ONE judged value per
// call: the string the rules read is the string the trail records, so what a person audits
// later is what was graded. A value holding a space is quoted, which the shell parser the
// rules already run unquotes.
func renderMCPCall(name string, input map[string]any) string {
	if name == hint.ToolLedger.String() {
		out := []string{name}
		for _, key := range mcpJudgedParams {
			value, ok := input[key]
			if !ok {
				continue
			}
			if mcpElidedParams[key] {
				out = append(out, key+"="+mcpElidedValue)
				continue
			}
			out = append(out, key+"="+quoteMCPValue(mcpValueString(value)))
		}
		return strings.Join(out, " ")
	}
	cli, ok := mcpCLIEquivalents[hint.ToolName(name)]
	if name == hint.ToolMemory.String() {
		// The one tool whose op picks the verb: a put writes, everything else reads.
		cli, ok = mcpCLIEquivalent{command: hint.MemoryLs}, true
		if envelopeString(input, "op") == "put" {
			cli = mcpCLIEquivalent{command: hint.MemoryPut, operands: []string{"name"}}
		}
	}
	if !ok {
		return name
	}
	out := append([]string{cli.command.String()}, cli.flags...)
	for _, key := range cli.operands {
		if value := mcpValueString(input[key]); value != "" {
			out = append(out, quoteMCPValue(value))
		}
	}
	return strings.Join(out, " ")
}

// mcpValueString flattens one parameter value. A list parameter arrives as either a
// comma-separated string or an array (both are accepted by the tool), and it renders the
// same way from both, so a rule cannot be dodged by picking a spelling.
func mcpValueString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, mcpValueString(item))
		}
		return strings.Join(parts, ",")
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

// quoteMCPValue keeps a value with whitespace as one word on the rendered line.
func quoteMCPValue(value string) string {
	if strings.ContainsAny(value, " \t\"'\\") {
		return strconv.Quote(value)
	}
	return value
}

// mcpMagusPrefix is how a host spells a call to magus's own MCP server. The prefix is the
// host's, the name after it is magus's.
const mcpMagusPrefix = "mcp__magus__"

// magusToolCall returns the magus MCP tool a host's tool name refers to, or "" when it
// refers to none.
//
// The bare name or magus's own prefix, and nothing else. A suffix match let any other
// server's `whatever__magus_ledger` decode as a magus ledger call, be rendered into
// magus's activity trail, and be judged by magus's rules.
func magusToolCall(toolName string) string {
	name := strings.TrimPrefix(toolName, mcpMagusPrefix)
	if !slices.ContainsFunc(hint.AllToolNames, func(t hint.ToolName) bool { return t.String() == name }) {
		return ""
	}
	return name
}

// mcpParams reads back the parameters renderMCPCall wrote. The guard sees a call to the
// ledger tool as `<tool name> <key>=<value>...`, normalized by the envelope decoder.
func mcpParams(args []string) map[string]string {
	params := make(map[string]string, len(args))
	for _, a := range args {
		if key, value, ok := strings.Cut(a, "="); ok {
			params[key] = value
		}
	}
	return params
}
