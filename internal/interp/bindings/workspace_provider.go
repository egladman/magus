package bindings

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
)

// This file lives in the bindings layer (not the magus library) for the reason
// remote_cache.go does: running a provider means running a Buzz spell, and the
// magus library must not link the VM to open a workspace. It registers the runner
// with internal/workspace at init, so the library reaches it through a hook.
func init() {
	workspace.RegisterProviderRunner(runWorkspaceProvider)
}

// buildWorkspaceNS assembles magus.workspace for a magusfile. Today it exposes
// provider(), which wires an imported spell as a workspace provider - a spell that
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
func buildWorkspaceNS(ctx context.Context, obs buzz.DirectObserver) vm.Value {
	ns := vm.NewMap()
	ns.MapSet("provider", directVal(obs, "magus.workspace.provider", func(_ context.Context, args []vm.Value) (vm.Value, error) {
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
	return ns
}

// providerDeadline bounds one list_projects invocation. A provider shells out to a
// foreign tool ON THE LOAD PATH of every magus command, so an nx or gradle daemon
// that wedges would hang `magus <anything>`, `magus doctor` included. The ceiling is
// high because a cold project-graph build on a large repo takes tens of seconds; it
// turns a hang into an error and says nothing about how fast a provider should be.
// (The CI-provider contract does the same with a much smaller budget, since its ops
// only format log lines.)
const providerDeadline = 2 * time.Minute

// runWorkspaceProvider invokes spellName's list_projects contract and decodes what
// it returns. It runs at WORKSPACE scope (Dir is the root), unlike an ordinary op,
// which runs in a project directory - a provider is answering what the projects
// ARE, so there is no project to run it in yet. root also travels in Params, which
// is what reaches the contract's cb callback; Dir only reaches it as target.projectPath.
func runWorkspaceProvider(ctx context.Context, spellName, root string) ([]spells.ProvidedProject, error) {
	drv, ok := project.DefaultSpellRegistry().Lookup(spellName)
	if !ok {
		return nil, fmt.Errorf("spell %q is not registered; import it before wiring it as a workspace provider", spellName)
	}
	ctx, cancel := context.WithTimeout(ctx, providerDeadline)
	defer cancel()
	started := time.Now()
	resp, err := drv.Invoke(ctx, spells.InvokeRequest{
		Target: spells.ListProjectsContract,
		Dir:    root,
		Params: map[string]any{"root": root},
	})
	if err != nil {
		// Report how long it actually ran rather than this function's ceiling: the
		// caller may have carried a shorter deadline (a daemon request budget), and
		// naming 2m for a run that was cut off at 10s sends the reader to the wrong knob.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("spell %q: %s was still running after %s and was cancelled: %w",
				spellName, spells.ListProjectsContract, time.Since(started).Round(time.Second), err)
		}
		return nil, err
	}
	// nil Data covers two cases the invoker cannot tell apart: the spell exports no
	// function of that name (the no-op an optional cache-backend op relies on), or it
	// exported one that returned null. A provider was wired to supply projects, so
	// neither case is benign, and the message names both.
	if resp.Data == nil {
		return nil, fmt.Errorf("spell %q is wired as a workspace provider but its %s(target, cb) returned nothing; it must be exported and must return a list of Project (an empty list if there are none)", spellName, spells.ListProjectsContract)
	}
	items, ok := resp.Data.([]any)
	if !ok {
		return nil, fmt.Errorf("spell %q: %s returned %T, want a list of Project", spellName, spells.ListProjectsContract, resp.Data)
	}
	out := make([]spells.ProvidedProject, 0, len(items))
	for i, item := range items {
		pp, err := decodeProvidedProject(spellName, i, item)
		if err != nil {
			return nil, err
		}
		out = append(out, pp)
	}
	return out, nil
}

// Field keys of the Buzz `object Project` (spells.ProvidedProject's mirror). They
// are named here because the record crosses as a plain map: a rename on the Go
// struct must be mirrored here, and TestDecodeCoversEveryProvidedProjectField walks
// the struct with reflect to make sure it is.
const (
	fieldPath      = "path"
	fieldName      = "name"
	fieldSpells    = "spells"
	fieldDependsOn = "depends_on"
	fieldSources   = "sources"
	fieldOutputs   = "outputs"
	fieldExclusive = "exclusive"
)

// decodeProvidedProject turns one returned Project record into the typed wire form.
// It stops there: turning those facts into ProjectOption values is the workspace
// package's job (projectOptions), which keeps this side a pure Buzz-value decode and
// keeps the decoded record serializable, so the provider's answer can be cached.
//
// It decodes explicitly rather than through a JSON round-trip: this is the least
// trustworthy input in the load path (a spell that shelled out to another tool), and
// a field of the wrong type should name itself rather than silently zero.
func decodeProvidedProject(spellName string, index int, item any) (spells.ProvidedProject, error) {
	where := fmt.Sprintf("spell %q: %s[%d]", spellName, spells.ListProjectsContract, index)
	m, ok := item.(map[string]any)
	if !ok {
		return spells.ProvidedProject{}, fmt.Errorf("%s is %T, want a Project", where, item)
	}

	var pp spells.ProvidedProject
	var err error
	if pp.Path, err = strField(m, fieldPath, where); err != nil {
		return spells.ProvidedProject{}, err
	}
	if pp.Name, err = strField(m, fieldName, where); err != nil {
		return spells.ProvidedProject{}, err
	}
	for _, field := range []struct {
		key string
		dst *[]string
	}{
		{fieldSpells, &pp.Spells},
		{fieldDependsOn, &pp.DependsOn},
		{fieldSources, &pp.Sources},
		{fieldOutputs, &pp.Outputs},
	} {
		vals, err := strListField(m, field.key, where)
		if err != nil {
			return spells.ProvidedProject{}, err
		}
		*field.dst = vals
	}
	// nil reads as absent here exactly as it does in strField and strListField: one
	// record must not have two null policies.
	if v, present := m[fieldExclusive]; present && v != nil {
		b, ok := v.(bool)
		if !ok {
			return spells.ProvidedProject{}, fmt.Errorf("%s: field %q is %T, want bool", where, fieldExclusive, v)
		}
		pp.Exclusive = b
	}
	return pp, nil
}

// strField reads an optional string field. A Project record marshals every declared
// field (an object carries its defaults), so an absent key and an empty string mean
// the same thing and both read as "".
func strField(m map[string]any, key, where string) (string, error) {
	v, present := m[key]
	if !present || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: field %q is %T, want str", where, key, v)
	}
	return s, nil
}

// strListField reads an optional [str] field, rejecting a non-string element rather
// than dropping it: a dropped dependency or source glob is a silently wrong cache
// key, which is the failure this whole decoder exists to prevent.
func strListField(m map[string]any, key, where string) ([]string, error) {
	v, present := m[key]
	if !present || v == nil {
		return nil, nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: field %q is %T, want [str]", where, key, v)
	}
	if len(items) == 0 {
		// nil, not an empty slice: a Buzz object always carries the field, so "declared
		// nothing" and "did not declare" must decode to the same record - otherwise two
		// spellings of the same answer produce different cache entries.
		return nil, nil
	}
	out := make([]string, 0, len(items))
	for i, it := range items {
		s, ok := it.(string)
		if !ok {
			return nil, fmt.Errorf("%s: field %q[%d] is %T, want str", where, key, i, it)
		}
		out = append(out, s)
	}
	return out, nil
}
