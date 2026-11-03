package bindings

import (
	"context"
	"fmt"
	"sync"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// skillCatalog is built on first use: hashing the embedded skill sources is work a
// session that never asks for a skill should not pay.
var skillCatalog = sync.OnceValue(func() *agent.Catalog {
	return agent.Default(types.KnowledgeSchemaVersion)
})

func init() { //nolint:gochecknoinits // std cannot import internal/agent, which imports std
	std.SkillCatalog = magusSkills
}

func magusSkills(ctx context.Context, opts types.SkillsOptions) ([]types.Skill, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return nil, types.DiagnosticErrorf(types.MagusfileOnlyMember,
			"magus\\Workspace.skills: no workspace on the context; it reads the workspace's skill directories, so run it from a magusfile target or a `magus buzz` script inside a workspace")
	}
	wired, err := workspace.WiredHarnesses(ws)
	if err != nil {
		return nil, fmt.Errorf("magus\\Workspace.skills: %w", err)
	}
	ctx = agent.ContextWithWiredHarnesses(ctx, wired)
	return skillCatalog().Offered(ctx, ws.Root(), wired, agent.SkillQuery{Name: opts.Name, Form: opts.Form})
}
