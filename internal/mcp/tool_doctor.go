//go:build mcp

package mcp

import (
	"context"

	"github.com/egladman/magus/internal/doctor"
	"github.com/egladman/magus/types"
)

// doctorResult extends doctor.Report with a strict-failure indicator without
// mutating the doctor.Report struct (which belongs to a different package).
type doctorResult struct {
	doctor.Report
	StrictFail bool `json:"strict_fail,omitempty"`
}

type doctorTool struct {
	opts ServerOptions
}

func (t *doctorTool) Name() string { return "magus_doctor" }

func (t *doctorTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	strict := paramBool(req.Params, "strict", false)

	ws := t.opts.Magus
	out := doctor.Run(
		ws.Root(), ws, nil,
		doctor.WithConfig(t.opts.Config),
		doctor.WithVersion(t.opts.Version),
		// WithFix intentionally not offered via MCP — file mutations should
		// be initiated by the user at the CLI, not an LLM tool.
	)

	dr := doctorResult{Report: out}
	if strict && out.Summary.Warn > 0 {
		dr.StrictFail = true
	}
	return types.InvokeResponse{Data: dr}, nil
}

var _ types.SpellDriver = (*doctorTool)(nil)
