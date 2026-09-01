package hint

// This file is the follow-up-hint half of the MCP tool surface: the canonical
// tool names plus the decision of which one-line hint, if any, a call outcome
// earns. The static per-session map (serverInstructions in the MCP transport)
// teaches the flow once for free; the FollowUp functions add the paid,
// context-sensitive part: at most one terse follow-up line, returned only where
// the tool name plus the call outcome make a next step obvious. Three outcomes
// earn a line - an error (recover with the naming tool), a success that found
// nothing (recover in the layer this tool does not cover), and a success that
// mints an ID the agent will chain. A plain non-empty SUCCESS gets nothing: a
// blanket "related tools" footer on every call is pure context tax, and output
// bytes are the agent's measured context cost (see magus.mcp.tool.output.size).

// ToolName is a canonical MCP tool name - the "magus_"-prefixed identifier the
// daemon registers. Declaring each once here makes a tool rename a compile error
// at every cross-link site rather than silent drift: the MCP Registry entries
// (internal/handler/mcp) bind their Name to these constants, the hint maps below
// build their keys and the tool names embedded in hint text from them, and
// TestMCPToolHintsResolve walks every reference back to a real Registry entry.
// This mirrors clicommand.go, the single source of truth for magus CLI command
// paths shown in user-facing output.
type ToolName string

// String renders the bare tool name, e.g. "magus_run_target". Call sites use it
// to concatenate a tool name into hint prose so the reference tracks a rename.
func (t ToolName) String() string { return string(t) }

// The full MCP tool surface. Every Registry[].Name is bound to one of these, so
// this block is the one place a tool name is spelled out.
const (
	ToolDescribe        ToolName = "magus_describe"
	ToolDescribeFile    ToolName = "magus_describe_file"
	ToolWhere           ToolName = "magus_where"
	ToolAffectedExplain ToolName = "magus_affected_explain"
	ToolInsight         ToolName = "magus_insight"
	ToolRunTarget       ToolName = "magus_run_target"
	ToolRunAffected     ToolName = "magus_run_affected"
	ToolDoctor          ToolName = "magus_doctor"
	ToolStatus          ToolName = "magus_status"
	ToolAffectedPlan    ToolName = "magus_affected_plan"
	ToolConfigGet       ToolName = "magus_config_get"
	ToolTailLog         ToolName = "magus_tail_log"
	ToolMemory          ToolName = "magus_memory"
	ToolQuery           ToolName = "magus_query"
	ToolOutput          ToolName = "magus_output"
	ToolExplain         ToolName = "magus_explain"
	ToolRefs            ToolName = "magus_refs"
	ToolPath            ToolName = "magus_path"
	ToolStats           ToolName = "magus_stats"
	ToolDiff            ToolName = "magus_diff"
	ToolVCSCheckpoint   ToolName = "magus_vcs_checkpoint"
	ToolLedger          ToolName = "magus_ledger"
)

// AllToolNames is every declared tool-name constant, for the drift test to walk.
// Keep new constants registered here - TestAllDeclaredToolsAreRegistered reads
// this file and fails if a declaration is missing, the way a hand-maintained
// list cannot.
var AllToolNames = []ToolName{
	ToolDescribe, ToolDescribeFile, ToolWhere, ToolAffectedExplain, ToolInsight,
	ToolRunTarget, ToolRunAffected, ToolDoctor, ToolStatus,
	ToolAffectedPlan, ToolConfigGet, ToolTailLog, ToolMemory,
	ToolQuery, ToolOutput, ToolExplain, ToolRefs, ToolPath, ToolStats,
	ToolDiff,
	ToolVCSCheckpoint, ToolLedger,
}

// errorHints maps a tool to the one-line recovery step returned ONLY when that
// tool returns an error/empty result. Values name tools, never argument values,
// and stay a single lean line so the failure path costs the agent almost nothing.
// Keys and the tool names embedded in each value are ToolName constants from
// above, so a tool rename is a compile error rather than a hint entry that
// silently stops matching or points at a tool that no longer exists.
var errorHints = map[ToolName]string{
	ToolRunTarget:   "next: list valid targets with " + ToolDescribe.String() + " (kind=targets)",
	ToolRunAffected: "next: list valid targets with " + ToolDescribe.String() + " (kind=targets)",
	ToolWhere:       "next: list projects with " + ToolDescribe.String() + " (kind=projects)",
	ToolOutput:      "next: output refs come from " + ToolRunTarget.String() + " or " + ToolTailLog.String(),
	ToolExplain:     "next: locate a node with " + ToolQuery.String() + ", then explain it",
	ToolPath:        "next: locate the endpoints with " + ToolQuery.String(),
	ToolRefs:        "next: locate a symbol with " + ToolQuery.String(),
}

// emptyHints maps a tool to the line an EMPTY but successful result earns - a
// call that worked and found nothing, which no other map covers because it is
// not an error.
//
// magus_query is the only entry, and it is the one that matters: magus_explain,
// magus_path and magus_refs all recover by saying "locate a node with
// magus_query", so a query that comes back empty was, until this, the end of the
// chain. An agent that reaches it concludes the graph does not hold the thing
// and falls back to grep. The line names the two layers the query did not
// search rather than repeating the verdict already in the payload.
var emptyHints = map[ToolName]string{
	ToolQuery: "next: no match; code symbols are a separate layer - try " + ToolRefs.String() +
		", or list what exists with " + ToolDescribe.String(),
}

// staticChainHints maps a tool to a chain hint returned on a SUCCESS that always
// leads somewhere fixed. Only tools whose whole purpose is to feed a follow-up
// tool belong here - never general read tools, which get no footer.
var staticChainHints = map[ToolName]string{
	ToolAffectedPlan: "next: run the affected set with " + ToolRunAffected.String(),
}

// refChainTools mint an output reference the agent chains into magus_output. On
// success their result text is scanned for a ref token; when one is present the
// fetch hint names that exact ref so the agent can pull the captured output
// without re-reading the run's event wall.
var refChainTools = map[ToolName]bool{
	ToolRunTarget:   true,
	ToolRunAffected: true,
}

// FollowUpError returns the recovery line an error or empty result from tool
// earns, or "" when it earns nothing.
func FollowUpError(tool ToolName) string {
	return errorHints[tool]
}

// FollowUpEmpty returns the recovery line a successful-but-empty result earns,
// or "" when it earns nothing. Separate from FollowUpSuccess because the caller
// has to establish emptiness from the payload first, and only a tool in
// emptyHints is worth that read.
func FollowUpEmpty(tool ToolName) string {
	return emptyHints[tool]
}

// WantsEmptyCheck reports whether a successful result from tool is worth
// inspecting for emptiness before calling FollowUpEmpty, so a tool with no
// empty-result line never pays for the parse. Mirrors MintsRef.
func WantsEmptyCheck(tool ToolName) bool {
	_, ok := emptyHints[tool]
	return ok
}

// FollowUpSuccess returns the at-most-one chain line a successful call earns, or
// "" when it earns nothing. mintedRef is the output reference the caller found in
// the result ("" when absent); it is only consulted for tools MintsRef reports,
// so callers may skip the scan otherwise.
func FollowUpSuccess(tool ToolName, mintedRef string) string {
	if h := staticChainHints[tool]; h != "" {
		return h
	}
	if mintedRef != "" && refChainTools[tool] {
		return "next: fetch the captured output with " + ToolOutput.String() + " (ref=" + mintedRef + ")"
	}
	return ""
}

// MintsRef reports whether a success from tool may carry an output reference
// worth scanning for before calling FollowUpSuccess.
func MintsRef(tool ToolName) bool {
	return refChainTools[tool]
}
