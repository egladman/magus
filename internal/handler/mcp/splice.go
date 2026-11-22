package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/internal/sandbox"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	scissors "github.com/egladman/magus/libs/safetyscissors/sandbox"
	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// spliceTool plans and applies edits through the Buzz splice implementation.
type spliceTool struct {
	root      string
	workspace *magus.Magus
	jobs      *job.Store
}

type spliceResponse struct {
	Plan    types.EditPlan                   `json:"plan"`
	Applied *types.Result[types.EditApplied] `json:"applied,omitempty"`
}

func (t *spliceTool) Name() string { return hint.ToolSplice.String() }

func (t *spliceTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	if t.workspace == nil || t.root == "" {
		return spells.InvokeResponse{}, fmt.Errorf("splice: workspace is unavailable")
	}
	document, ok := req.Params["document"].(string)
	if !ok {
		return spells.InvokeResponse{}, fmt.Errorf("splice: required parameter \"document\" must be a string")
	}
	apply, ok := req.Params["apply"].(bool)
	if req.Params["apply"] != nil && !ok {
		return spells.InvokeResponse{}, fmt.Errorf("splice: apply must be a boolean")
	}
	ctx, err := t.scopedContext(ctx)
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	parsed, err := std.SpliceBlocks(ctx, document, "MCP splice")
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	for _, change := range parsed.Changes {
		if filepath.IsAbs(change.Path) || filepath.Clean(change.Path) == ".." || strings.HasPrefix(filepath.Clean(change.Path), ".."+string(filepath.Separator)) {
			plan := types.EditPlan{Changes: parsed.Changes, Files: []types.EditedFile{}, Spans: []types.EditSpan{}, Refused: []types.EditRefusal{{Path: change.Path, Reason: "path must stay inside the workspace"}}}
			out := spliceResponse{Plan: plan}
			if apply {
				applied, err := std.SpliceApply(ctx, plan)
				if err != nil {
					return spells.InvokeResponse{}, err
				}
				out.Applied = &applied
			}
			return spells.InvokeResponse{Data: out}, nil
		}
	}
	plan, err := std.SplicePlan(ctx, parsed.Changes)
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	plan.Refused = append(plan.Refused, parsed.Refused...)
	out := spliceResponse{Plan: plan}
	if apply {
		applied, err := std.SpliceApply(ctx, plan)
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		out.Applied = &applied
	}
	return spells.InvokeResponse{Data: out}, nil
}

func (t *spliceTool) scopedContext(ctx context.Context) (context.Context, error) {
	ctx = types.WithWorkspace(ctx, t.workspace)
	ctx = std.WithCurrentWorkingDirectory(ctx, t.root)
	actor, stamped := callerActor(ctx)
	lease, source := actor.Lease, jobtypes.LeaseSourceEnv
	if !stamped {
		lease = recorder.LeaseFromEnv()
	}
	if actor.Unstamped {
		ctx = jobtypes.WithoutLease(ctx)
		ctx, err := t.workspace.ApplySandbox(ctx)
		if err != nil {
			return nil, err
		}
		current := sandbox.PolicyFromContext(ctx)
		if current == nil {
			return sandbox.WithPolicy(ctx, &sandbox.Policy{Workspace: t.root, FS: filesystem.Ruleset{Rules: []filesystem.Rule{{Path: t.root, Read: true}}}}), nil
		}
		narrowed := *current
		narrowed.FS.Rules = append([]filesystem.Rule(nil), current.FS.Rules...)
		for i := range narrowed.FS.Rules {
			narrowed.FS.Rules[i].Write = false
		}
		return sandbox.WithPolicy(ctx, &narrowed), nil
	}
	if lease != "" {
		var err error
		ctx, err = jobtypes.WithLease(ctx, lease)
		if err != nil {
			return nil, err
		}
		if t.jobs == nil {
			return nil, fmt.Errorf("splice: lease %q cannot be checked because the job store is unavailable", lease)
		}
		rows, err := t.jobs.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("splice: read lease %q: %w", lease, err)
		}
		var row jobtypes.Job
		for _, candidate := range rows {
			if candidate.ID == lease {
				row = candidate
				break
			}
		}
		if row.ID == "" || !row.State.Live() {
			return nil, fmt.Errorf("splice: lease %q is missing or not live", lease)
		}
		ctx, err = t.workspace.ApplySandbox(ctx)
		if err != nil {
			return nil, err
		}
		boundary, narrowed := scissors.LeaseBoundary(row, source, t.root)
		rules := []filesystem.Rule{{Path: t.root, Read: true, Write: true}}
		if narrowed {
			rules = boundary.Narrow(rules)
		}
		ctx = sandbox.WithPolicy(ctx, &sandbox.Policy{Workspace: t.root, Lease: lease, FS: filesystem.Ruleset{Rules: rules}})
		return ctx, nil
	}
	ctx = jobtypes.WithoutLease(ctx)
	ctx, err := t.workspace.ApplySandbox(ctx)
	if err != nil {
		return nil, err
	}
	if sandbox.PolicyFromContext(ctx) == nil {
		ctx = sandbox.WithPolicy(ctx, &sandbox.Policy{Workspace: t.root, FS: filesystem.Ruleset{Rules: []filesystem.Rule{{Path: t.root, Read: true, Write: true}}}})
	}
	return ctx, nil
}

var _ spells.Driver = (*spliceTool)(nil)
