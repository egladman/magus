package types

import "slices"

// HookPoint is a moment in a host session where magus has a job. Every
// shipped harness binds each point or says why its host cannot.
type HookPoint string

const (
	HookPointShell        HookPoint = "shell"
	HookPointEdit         HookPoint = "edit"
	HookPointStop         HookPoint = "stop"
	HookPointSubagentStop HookPoint = "subagent-stop"
	HookPointSpawn        HookPoint = "spawn"
	HookPointSessionStart HookPoint = "session-start"
	HookPointCompact      HookPoint = "compact"
	HookPointMCP          HookPoint = "mcp"
	// HookPointAttach shows the person a file magus wrote, inline.
	HookPointAttach HookPoint = "attach"
)

// HookPoints is every hook point, in binding table order.
func HookPoints() []HookPoint { return slices.Clone(hookPoints) }

// BindingStatus has no third value on purpose: a point with no binding
// is a gap to report, never a default.
type BindingStatus string

const (
	Bound BindingStatus = "bound"
	// Unsupported carries a reason citing the host's docs, or
	// "not verified: <what>".
	Unsupported BindingStatus = "unsupported"
)

// HookBinding is one harness's answer for one hook point. A Bound row names
// the Adapter, the glue script the host runs there. An Unsupported row
// carries the Reason.
//
// A spell returns only Unsupported rows (mgs_listUnsupportedHooks). Bound
// rows come from the points its hooks list, so the two cannot disagree.
type HookBinding struct {
	Harness string        `json:"harness,omitempty"`
	Point   HookPoint     `json:"point"`
	Status  BindingStatus `json:"status"`
	Adapter string        `json:"adapter,omitempty"`
	Reason  string        `json:"reason,omitempty"`
}
