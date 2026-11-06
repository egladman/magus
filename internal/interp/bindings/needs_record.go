package bindings

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

// recordBuzzNeeds answers ctx.needs under a step recorder: it records each
// target args names and runs none of them. A same-project step records an
// empty Project, as ChainStep does; the recorder's owner knows the project.
func recordBuzzNeeds(ctx context.Context, rec *types.StepLog, targets map[string]vm.Callable, exports map[string]vm.Value, ext *externalHandles, args []vm.Value) error {
	return eachNeed(targets, exports, ext, args,
		func(name string) { rec.Record(types.Unit{Target: name}) },
		func(ref externalTarget) error {
			dep, ok, err := externalProjectPath(ctx, ref)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("cross-project step %s:%s: no workspace to resolve it against", ref.Project, ref.Target)
			}
			rec.Record(types.Unit{Project: dep, Target: types.Normalize(ref.Target)})
			return nil
		})
}

// eachNeed resolves ctx.needs arguments in order: a target function to its
// name, a cross-project handle to its ref, and a ctx.glob(...) list to each
// element. The error carries the ctx.needs prefix.
func eachNeed(targets map[string]vm.Callable, exports map[string]vm.Value, ext *externalHandles, args []vm.Value, local func(name string), external func(ref externalTarget) error) error {
	var collect func(arg vm.Value) error
	collect = func(arg vm.Value) error {
		if arg.IsList() {
			for _, el := range arg.ListItems() {
				if err := collect(el); err != nil {
					return err
				}
			}
			return nil
		}
		if !arg.IsFun() {
			return errors.New("each argument must be a target function (an exported target, a project import member, or a ctx.glob(...) result)")
		}
		if ref, ok := ext.lookup(arg); ok {
			return external(ref)
		}
		name, err := resolveTargetFun(targets, exports, arg)
		if err != nil {
			return err
		}
		local(name)
		return nil
	}
	for _, arg := range args {
		if err := collect(arg); err != nil {
			return fmt.Errorf("ctx.needs: %w", err)
		}
	}
	return nil
}

// externalProjectPath is the workspace path a cross-project handle names,
// resolved with file.ResolveImport against the caller's workspace-relative
// directory, the rule describe.go applies to the extracted ref. ok is false
// when ctx carries no workspace or source to resolve against.
func externalProjectPath(ctx context.Context, ref externalTarget) (path string, ok bool, err error) {
	src := interp.SourceFromContext(ctx)
	ws := types.WorkspaceFromContext(ctx)
	if src == nil || ws == nil {
		return "", false, nil
	}
	callerRel, err := filepath.Rel(ws.Root(), src.Dir)
	if err != nil {
		return "", true, fmt.Errorf("cross-project dependency: %w", err)
	}
	path, err = file.ResolveImport(ref.Project, filepath.ToSlash(callerRel))
	return path, true, err
}
