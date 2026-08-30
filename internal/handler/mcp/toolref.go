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
	toolDescribe        ToolName = "magus_describe"
	toolDescribeFile    ToolName = "magus_describe_file"
	toolWhere           ToolName = "magus_where"
	toolAffectedExplain ToolName = "magus_affected_explain"
	toolInsight         ToolName = "magus_insight"
	toolRunTarget       ToolName = "magus_run_target"
	toolRunAffected     ToolName = "magus_run_affected"
	toolDoctor          ToolName = "magus_doctor"
	toolStatus          ToolName = "magus_status"
	toolAffectedPlan    ToolName = "magus_affected_plan"
	toolConfigGet       ToolName = "magus_config_get"
	toolTailLog         ToolName = "magus_tail_log"
	toolMemory          ToolName = "magus_memory"
	toolQuery           ToolName = "magus_query"
	toolOutput          ToolName = "magus_output"
	toolExplain         ToolName = "magus_explain"
	toolRefs            ToolName = "magus_refs"
	toolPath            ToolName = "magus_path"
	toolStats           ToolName = "magus_stats"
	toolDiff            ToolName = "magus_diff"
	toolVCSCheckpoint   ToolName = "magus_vcs_checkpoint"
	toolLedger          ToolName = "magus_ledger"
)

// allToolNames is every declared tool-name constant, for the drift test to walk.
// Keep new constants registered here.
var allToolNames = []ToolName{
	toolDescribe, toolDescribeFile, toolWhere, toolAffectedExplain, toolInsight,
	toolRunTarget, toolRunAffected, toolDoctor, toolStatus,
	toolAffectedPlan, toolConfigGet, toolTailLog, toolMemory,
	toolQuery, toolOutput, toolExplain, toolRefs, toolPath, toolStats,
	toolDiff,
	toolVCSCheckpoint, toolLedger,
}
