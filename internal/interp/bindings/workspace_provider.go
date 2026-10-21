package bindings

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/egladman/magus/internal/interp/bindings/gen/providerops"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// This file lives in the bindings layer (not the magus library) for the reason
// remote_cache.go does: running a provider means running a Buzz spell, and the
// magus library must not link the VM to open a workspace. It registers the runner
// with internal/workspace at init, so the library reaches it through a hook.
func init() { //nolint:gochecknoinits // internal/workspace cannot link the Buzz VM; the runner registers from the layer that can
	workspace.RegisterProviderRunner(runWorkspaceProvider)
}

// buildWorkspace assembles magus.workspace for a magusfile. Today it exposes
// provider(), which wires an imported spell as a workspace provider: a spell that
// supplies the workspace's project set because another tool already owns it:
//
//	import "spells/nx"
//	magus.workspace.provider(nx)
//
// The spell exports the list_projects contract function; magus invokes it once per
// workspace load and folds what it returns into the workspace as ordinary projects.
// This is the same shape as magus.cache.remote and magus.ci.provider: the magusfile
// names a spell, the subsystem invokes contract functions by name, and magus knows
// nothing about the foreign tool.
//
// Wiring is per-workspace and explicit. magus infers no projects from tool markers
// (see docs/concepts/workspace.md), and a provider does not reintroduce that: the
// workspace's own magusfile still decides that another tool owns its project set.
//
// Several providers may be wired; they run in wiring order and the first to report a
// path owns it.
func buildWorkspace(ctx context.Context, obs buzz.DirectObserver) vm.Value {
	workspaceObj := vm.NewMap()
	workspaceObj.MapSet("provider", directVal(obs, "magus.workspace.provider", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsMap() {
			return vm.Null, fmt.Errorf(`magus\workspace.provider: expected an imported spell handle`)
		}
		nv, ok := args[0].MapGet("name")
		if !ok || !nv.IsStr() || nv.AsString() == "" {
			return vm.Null, fmt.Errorf(`magus\workspace.provider: argument is not a spell handle (no name)`)
		}
		if reg := workspace.WorkspaceRegistryFromContext(ctx); reg != nil {
			reg.AddProvider(nv.AsString())
		}
		return vm.Null, nil
	}))
	return workspaceObj
}

// providerDeadline bounds one list_projects invocation. A provider shells out to a
// foreign tool ON THE LOAD PATH of every magus command, so an nx or gradle daemon
// that wedges would hang `magus <anything>`, `magus doctor` included. The ceiling is
// high because a cold project-graph build on a large repo takes tens of seconds; it
// turns a hang into an error and says nothing about how fast a provider should be.
// (The CI-provider contract does the same with a much smaller budget, since its ops
// only format log lines.)
const providerDeadline = 2 * time.Minute

// runWorkspaceProvider calls spellName's list_projects op and returns what it answers.
// It runs at WORKSPACE scope (target.projectPath and req.root are the root), unlike an
// ordinary op, which runs in a project directory: a provider is answering what the
// projects ARE, so there is no project to run it in yet.
func runWorkspaceProvider(ctx context.Context, spellName, root string) ([]spells.ProvidedProject, error) {
	drv, ok := project.DefaultSpellRegistry().Lookup(spellName)
	if !ok {
		return nil, fmt.Errorf("spell %q is not registered, import it before wiring it as a workspace provider", spellName)
	}
	ctx, cancel := context.WithTimeout(ctx, providerDeadline)
	defer cancel()
	started := time.Now()
	op := providerops.ListProjects
	got, exported, err := CallOp(ctx, drv, op, providerTarget(ctx, op.Name, root), types.ProjectQuery{Root: root})
	if err != nil {
		// Report how long it actually ran rather than this function's ceiling: the
		// caller may have carried a shorter deadline (a server request budget), and
		// naming 2m for a run that was cut off at 10s sends the reader to the wrong knob.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("spell %q: %s was still running after %s and was cancelled: %w",
				spellName, op.Name, time.Since(started).Round(time.Second), err)
		}
		return nil, err
	}
	if !exported {
		return nil, fmt.Errorf("spell %q is wired as a workspace provider but does not export %s(target: Target, req: ProjectQuery) > [Project]",
			spellName, op.Name)
	}
	return got, nil
}
