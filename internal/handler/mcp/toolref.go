package mcp

// ToolName is a canonical MCP tool name - the "magus_"-prefixed identifier the
// daemon registers. Declaring each once here makes a tool rename a compile error
// at every cross-link site rather than silent drift: the Registry entries bind
// their Name to these constants, hints.go builds its map keys and the tool names
// embedded in hint text from them, and TestMCPToolHintsResolve walks every
// reference back to a real Registry entry. This mirrors internal/interactive/
// clihint, which is the single source of truth for magus CLI command paths shown
// in user-facing output.
type ToolName string

// String renders the bare tool name, e.g. "magus_run_target". Call sites use it
// to concatenate a tool name into hint prose so the reference tracks a rename.
func (t ToolName) String() string { return string(t) }

// The full MCP tool surface. Every Registry[].Name is bound to one of these, so
// this block is the one place a tool name is spelled out.
const (
	ToolDescribe        ToolName = "magus_describe"
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
	ToolScratchpad      ToolName = "magus_scratchpad"
	ToolQuery           ToolName = "magus_query"
	ToolOutput          ToolName = "magus_output"
	ToolExplain         ToolName = "magus_explain"
	ToolRefs            ToolName = "magus_refs"
	ToolPath            ToolName = "magus_path"
	ToolStats           ToolName = "magus_stats"
)

// allToolNames is every declared tool-name constant, for the drift test to walk.
// Keep new constants registered here.
var allToolNames = []ToolName{
	ToolDescribe, ToolWhere, ToolAffectedExplain, ToolInsight,
	ToolRunTarget, ToolRunAffected, ToolDoctor, ToolStatus,
	ToolAffectedPlan, ToolConfigGet, ToolTailLog, ToolScratchpad,
	ToolQuery, ToolOutput, ToolExplain, ToolRefs, ToolPath, ToolStats,
}
