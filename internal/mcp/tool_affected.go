//go:build mcp

package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/internal/wire"
	"github.com/egladman/magus/types"
)

type runAffectedTool struct {
	opts ServerOptions
}

func (t *runAffectedTool) Name() string { return "magus_run_affected" }

func (t *runAffectedTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	rawTarget := paramString(req.Params, "target", "")
	if rawTarget == "" {
		return types.InvokeResponse{}, errors.New("mcp: target is required")
	}
	parsed, err := types.ParseTarget(rawTarget)
	if err != nil {
		return types.InvokeResponse{}, fmt.Errorf("mcp: invalid target: %w", err)
	}
	base := paramString(req.Params, "base", "")
	dryRun := paramBool(req.Params, "dry_run", false)

	var buf bytes.Buffer
	rw := report.NewWriter(&buf)
	// Route graph events to this request's writer via context, not the shared
	// workspace observer: the daemon serves concurrent requests on one *Magus,
	// and a process-global observer would interleave their graph events.
	ctx = types.ContextWithGraphObserver(ctx, report.GraphObserver(rw))

	runOpts := []wire.RunOption{wire.WithReport(rw)}
	if dryRun {
		runOpts = append(runOpts, wire.WithDryRun())
	}
	if base != "" {
		runOpts = append(runOpts, wire.WithBaseRef(base))
	}
	if len(parsed.Charms) > 0 {
		runOpts = append(runOpts, wire.WithCharms(parsed.Charms...))
	}

	start := time.Now()
	runErr := t.opts.Magus.RunAffected(ctx, parsed.Name, runOpts...)
	dur := time.Since(start)

	// Close the writer to flush the drain goroutine before parsing.
	_ = rw.Close()
	events := parseEventLines(&buf)
	out := runResult{
		OK:         runErr == nil,
		DurationMs: dur.Milliseconds(),
		Events:     events,
	}
	if runErr != nil {
		out.Error = runErr.Error()
	}
	return types.InvokeResponse{Data: out}, nil
}

var _ types.SpellDriver = (*runAffectedTool)(nil)
