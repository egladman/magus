package magus

import (
	"context"
	"errors"
	"fmt"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// GuardRules are the workspace guard rules the root magusfile registers, loaded from that
// magusfile alone. An agent host calls the guard before every tool call, and the rules need
// none of what the full workspace load computes, which costs an order of magnitude more.
type GuardRules struct {
	root     string
	opts     types.VCSOptions
	registry *workspace.WorkspaceRegistry
	sources  []interp.SourceFile
}

// LoadGuardRules evaluates the root magusfile of the workspace at root on its own, and
// returns the guard rules it registers. opts selects the version control the approved side
// is read through. A workspace with no magusfile has no rules and no error; a magusfile
// that does not load is an error, which the caller must not read as "no rules".
func LoadGuardRules(ctx context.Context, root string, opts types.VCSOptions) (*GuardRules, error) {
	rules := &GuardRules{root: root, opts: opts, registry: workspace.NewWorkspaceRegistry()}
	if !interp.Available() {
		return rules, nil
	}
	srcs, err := interp.FindAll(root)
	if errors.Is(err, interp.ErrNoMagusfile) {
		return rules, nil
	}
	if err != nil {
		return nil, err
	}
	log := &interp.SourceLog{}
	lctx := installWorkspaceRegistry(ctx, rules.registry)
	lctx = secret.ContextWithResolver(lctx, secret.New())
	lctx = interp.WithProjectPath(lctx, ".")
	lctx = interp.WithSourceLog(lctx, log)
	for _, src := range srcs {
		if _, err := interp.Parse(lctx, src); err != nil {
			return nil, fmt.Errorf("magusfile: %w", err)
		}
	}
	rules.sources = log.Files()
	return rules, nil
}

// ShellRules returns the magus\guard.shell rules in declaration order.
func (g *GuardRules) ShellRules() []workspace.ShellRule { return g.registry.ShellRules() }

// SpawnRule returns the magus\guard.spawn rule, or nil.
func (g *GuardRules) SpawnRule() workspace.SpawnRule { return g.registry.SpawnRule() }

// CommandRule returns the magus\guard.command rule, or nil.
func (g *GuardRules) CommandRule() workspace.CommandRule { return g.registry.CommandRule() }

// WriteRule returns the magus\guard.write rule, or nil.
func (g *GuardRules) WriteRule() workspace.WriteRule { return g.registry.WriteRule() }

// Policy describes the rules for the trail's lineage, as Magus.GuardPolicy does.
func (g *GuardRules) Policy() GuardPolicy {
	return guardPolicyOf(g.ShellRules(), g.SpawnRule() != nil, g.CommandRule() != nil, g.WriteRule() != nil, g.sources)
}

// ApprovedSpawnRule is Magus.ApprovedSpawnRule for these rules. It costs one VCS status
// when no file the root load read differs from its approved content, and a second load of
// the root magusfile when one does.
func (g *GuardRules) ApprovedSpawnRule(ctx context.Context) (workspace.SpawnRule, error) {
	reg, err := approvedRegistryBeside(ctx, g.root, ".", g.opts, secret.New(), g.sources, true)
	if err != nil || reg == nil {
		return nil, err
	}
	return reg.SpawnRule(), nil
}

// ApprovedCommandRule is ApprovedSpawnRule for the magus\guard.command rule.
func (g *GuardRules) ApprovedCommandRule(ctx context.Context) (workspace.CommandRule, error) {
	reg, err := approvedRegistryBeside(ctx, g.root, ".", g.opts, secret.New(), g.sources, true)
	if err != nil || reg == nil {
		return nil, err
	}
	return reg.CommandRule(), nil
}

// ApprovedWriteRule is ApprovedSpawnRule for the magus\guard.write rule.
func (g *GuardRules) ApprovedWriteRule(ctx context.Context) (workspace.WriteRule, error) {
	reg, err := approvedRegistryBeside(ctx, g.root, ".", g.opts, secret.New(), g.sources, true)
	if err != nil || reg == nil {
		return nil, err
	}
	return reg.WriteRule(), nil
}

// ApprovedContentIDs is Magus.ApprovedContentIDs for these rules' workspace.
func (g *GuardRules) ApprovedContentIDs(ctx context.Context, paths []string) map[string]string {
	return approvedContentIDs(ctx, g.root, g.opts, paths)
}
