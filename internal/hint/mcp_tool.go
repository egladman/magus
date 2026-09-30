package hint

import (
	"fmt"
	"time"
)

// ToolName is a canonical MCP tool name, the bare identifier the server
// registers; hosts namespace it by server (mcp__magus__status). Declaring each
// once here makes a tool rename a compile error at every cross-link site rather
// than silent drift: the MCP Registry entries (internal/handler/mcp) bind their
// Name to these constants, the server instructions render them, and
// TestMCPToolNamesResolve walks every reference back to a real Registry entry.
// This mirrors cli_command.go, the single source of truth for magus CLI command
// paths shown in user-facing output.
type ToolName string

// String renders the bare tool name, e.g. "status". Call sites use it
// to concatenate a tool name into prose so the reference tracks a rename.
func (t ToolName) String() string { return string(t) }

// The full MCP tool surface. std/magus.go names every tool through one of these, and
// the generated catalog (internal/handler/mcp/gen) carries the rendered string, so
// this block is the one place a tool name is spelled by hand.
const (
	ToolClient  ToolName = "client"
	ToolStatus  ToolName = "status"
	ToolConfig  ToolName = "config"
	ToolDiff    ToolName = "diff"
	ToolConsole ToolName = "console"
	ToolBuzz    ToolName = "buzz"
)

// AllToolNames is every declared tool-name constant, for the drift test to walk.
// Keep new constants registered here; TestAllDeclaredToolsAreRegistered reads
// this file and fails if a declaration is missing, the way a hand-maintained
// list cannot.
var AllToolNames = []ToolName{
	ToolClient, ToolStatus, ToolConfig, ToolDiff, ToolConsole, ToolBuzz,
}

// LookupTool resolves a declared tool name, reporting false for one nobody declares.
//
// The counterpart of Lookup in cli_command.go, and there for the same reason: a
// GENERATED surface that names a tool in prose has no compiler to catch a rename,
// so it resolves the name instead of retyping it.
func LookupTool(name string) (ToolName, bool) {
	for _, t := range AllToolNames {
		if string(t) == name {
			return t, true
		}
	}
	return "", false
}

// ClientCallBound is how long one direct call to the client tool may run. A call
// a host runs as an MCP task has no bound; the host cancels it instead. Declared
// here rather than beside the handler so the generated tool description (std,
// which cannot import the handler) renders the same value the handler enforces.
const ClientCallBound time.Duration = 10 * time.Minute

// ClientBoundSentence is the one sentence that describes ClientCallBound wherever
// the client tool is described.
func ClientBoundSentence() string {
	return fmt.Sprintf("Bounded at %d minutes when called directly; a host that supports MCP tasks can run it as a task without that bound.", int(ClientCallBound/time.Minute))
}
