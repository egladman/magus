package bindings

import (
	"context"
	"fmt"
	"sync"

	"github.com/egladman/magus/internal/agent"
	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

// skillCatalog is built on first use: hashing the embedded skill sources is work a
// session that never asks for a skill should not pay.
var skillCatalog = sync.OnceValue(func() *agent.Catalog {
	return agent.Default(types.KnowledgeSchemaVersion)
})

// buildSkills binds magus\skills. Hand-bound because the catalog lives in
// internal/agent, which imports std, so std cannot hold an Impl for it.
func buildSkills(obs buzz.DirectObserver) vm.Value {
	return directVal(obs, "magus.skills", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		out, err := magusSkills(ctx, bindinggen.AnyMap(args, 0))
		if err != nil {
			return vm.Null, bindinggen.HostError(err)
		}
		return bindinggen.ObjectSlice(out, bindinggen.ObjectSkill), nil
	})
}

func magusSkills(ctx context.Context, opts map[string]any) ([]types.Skill, error) {
	var q agent.SkillQuery
	for k, v := range opts {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("magus.skills: %s must be a string, got %T", k, v)
		}
		switch k {
		case "name":
			q.Name = s
		case "form":
			form, err := agent.ParseForm(s)
			if err != nil {
				return nil, fmt.Errorf("magus.skills: %w", err)
			}
			q.Form = form
		default:
			return nil, fmt.Errorf("magus.skills: unknown option %q (want name, form)", k)
		}
	}
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return nil, types.DiagnosticErrorf(types.MagusfileOnlyMember,
			"magus\\skills: no workspace on the context; it reads the workspace's skill directories, so run it from a magusfile target or a `magus buzz` script inside a workspace")
	}
	// Structural, as doctor reads it: the workspace value is the root *magus.Magus,
	// which bindings cannot import.
	var wired []string
	if h, ok := ws.(interface{ Harnesses() []string }); ok {
		wired = h.Harnesses()
	}
	ctx = agent.ContextWithWiredHarnesses(ctx, wired)
	return skillCatalog().Offered(ctx, ws.Root(), wired, q)
}
