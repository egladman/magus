package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

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
	workspace *magus.Magus
	jobs      *job.Store
}

type spliceResponse struct {
	Plan    types.EditPlan                   `json:"plan"`
	Applied *types.Result[types.EditApplied] `json:"applied,omitempty"`
}

func (t *spliceTool) Name() string { return hint.ToolSplice.String() }

func (t *spliceTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	if t.workspace == nil || t.workspace.Root() == "" {
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
	plan := types.EditPlan{Changes: parsed.Changes, Files: []types.EditedFile{}, Spans: []types.EditSpan{}, Refused: parsed.Refused}
	for _, change := range parsed.Changes {
		if !filepath.IsLocal(change.Path) {
			plan.Refused = append(plan.Refused, types.EditRefusal{Path: change.Path, Reason: "path must stay inside the workspace"})
		}
	}
	if len(plan.Refused) == 0 {
		plan, err = std.SplicePlan(ctx, parsed.Changes)
		if err != nil {
			return spells.InvokeResponse{}, err
		}
	}
	out := spliceResponse{Plan: plan}
	if apply {
		if err := ctx.Err(); err != nil {
			return spells.InvokeResponse{}, err
		}
		applied, err := std.SpliceApply(ctx, plan)
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		out.Applied = &applied
	}
	return spells.InvokeResponse{Data: out}, nil
}

func (t *spliceTool) scopedContext(ctx context.Context) (context.Context, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	caller := sandbox.PolicyFromContext(ctx)
	callerJudge, hasJudge := std.EditJudgeFromContext(ctx)
	ctx = types.WithWorkspace(ctx, t.workspace)
	ctx = std.WithCurrentWorkingDirectory(ctx, t.workspace.Root())
	actor, stamped := callerActor(ctx)
	lease := actor.Lease
	if !stamped {
		lease, stamped = jobtypes.LookupLease(ctx)
		if !stamped {
			lease = recorder.LeaseFromEnv()
		}
	}
	root := filesystem.ResolveRulePath(t.workspace.Root())
	// Start with the workspace's own policy and edit judge. Apply the
	// validated caller's lease once, after capturing the workspace grants.
	ctx, err := t.workspace.ApplySandbox(jobtypes.WithoutLease(ctx))
	if err != nil {
		return nil, err
	}
	if hasJudge {
		workspaceJudge, ok := std.EditJudgeFromContext(ctx)
		if !ok {
			return nil, fmt.Errorf("splice: workspace edit judge is unavailable")
		}
		ctx = std.WithEditJudge(ctx, spliceEditJudge(callerJudge, workspaceJudge))
	}
	rules := []filesystem.Rule{{Path: root, Read: true, Write: !actor.Unstamped}}
	policy := sandbox.Policy{Workspace: root}
	base := sandbox.PolicyFromContext(ctx)
	if base != nil {
		policy = *base
	}
	if lease != "" {
		ctx, err = jobtypes.WithLease(ctx, lease)
		if err != nil {
			return nil, err
		}
		row, err := t.liveLease(ctx, lease)
		if err != nil {
			return nil, err
		}
		boundary, narrowed := scissors.LeaseBoundary(row, jobtypes.LeaseSourceEnv, root)
		if narrowed {
			rules = boundary.Narrow(rules)
		}
		policy.Lease, policy.LeaseFrom = lease, jobtypes.LeaseSourceEnv
	}
	if base != nil {
		rules = intersectSpliceGrants(rules, base.FS.Rules)
	}
	if caller != nil && caller != base {
		rules = intersectSpliceGrants(rules, caller.FS.Rules)
		policy.GitDirs = append(slices.Clone(policy.GitDirs), caller.GitDirs...)
	}
	// One Policy can protect control files for one workspace. Refuse a
	// mismatched caller instead of dropping its control-file protection.
	if caller != nil && caller.Workspace != "" && filesystem.ResolveRulePath(caller.Workspace) != root {
		return nil, fmt.Errorf("splice: caller and tool workspace differ")
	}
	policy.Workspace = root
	policy.FS.Rules = rules
	return sandbox.WithPolicy(ctx, &policy), nil
}

func (t *spliceTool) liveLease(ctx context.Context, lease string) (jobtypes.Job, error) {
	if t.jobs == nil {
		return jobtypes.Job{}, fmt.Errorf("splice: lease %q cannot be checked because the job store is unavailable", lease)
	}
	rows, err := t.jobs.List(ctx)
	if err != nil {
		return jobtypes.Job{}, fmt.Errorf("splice: read lease %q: %w", lease, err)
	}
	for _, row := range rows {
		if row.ID == lease && row.State.Live() {
			return row, nil
		}
	}
	return jobtypes.Job{}, fmt.Errorf("splice: lease %q is missing or not live", lease)
}

// Each judge grades the paths it owns. A refusal from either vetoes the edit.
func spliceEditJudge(caller, workspace std.EditJudge) std.EditJudge {
	return std.EditJudge{
		Covers: func(ctx context.Context, path string) bool {
			return caller.Covers(ctx, path) || workspace.Covers(ctx, path)
		},
		Grade: func(ctx context.Context, path string, before, after []byte) error {
			if caller.Covers(ctx, path) {
				if err := caller.Grade(ctx, path, before, after); err != nil {
					return err
				}
			}
			if workspace.Covers(ctx, path) {
				return workspace.Grade(ctx, path, before, after)
			}
			return nil
		},
	}
}

// Each rule grants access to a subtree. Two overlapping grants intersect
// at the narrower subtree, with only the access both granted.
func intersectSpliceGrants(left, right []filesystem.Rule) []filesystem.Rule {
	out := make([]filesystem.Rule, 0, len(left))
	for _, a := range left {
		for _, b := range right {
			path := a.Path
			switch {
			case filesystem.Under(a.Path, b.Path):
			case filesystem.Under(b.Path, a.Path):
				path = b.Path
			default:
				continue
			}
			out = append(out, filesystem.Rule{Path: path, Read: a.Read && b.Read, Write: a.Write && b.Write})
		}
	}
	return out
}

var _ spells.Driver = (*spliceTool)(nil)
