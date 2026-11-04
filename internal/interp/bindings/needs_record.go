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
func recordBuzzNeeds(ctx context.Context, rec *types.StepRecorder, targets map[string]vm.Callable, exports map[string]vm.Value, ext *externalHandles, args []vm.Value) error {
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
			return errors.New("each argument must be a target function")
		}
		if ref, ok := ext.lookup(arg); ok {
			dep, err := externalProject(ctx, ref)
			if err != nil {
				return err
			}
			rec.Record(types.Unit{Project: dep, Target: types.Normalize(ref.Target)})
			return nil
		}
		name, err := resolveTargetFun(targets, exports, arg)
		if err != nil {
			return err
		}
		rec.Record(types.Unit{Target: name})
		return nil
	}
	for _, arg := range args {
		if err := collect(arg); err != nil {
			return fmt.Errorf("ctx.needs: %w", err)
		}
	}
	return nil
}

// externalProject is the workspace path a cross-project handle names, resolved
// as dispatchBuzzExternal resolves it.
func externalProject(ctx context.Context, ref externalTarget) (string, error) {
	src := interp.SourceFromContext(ctx)
	ws := types.WorkspaceFromContext(ctx)
	if src == nil || ws == nil {
		return "", fmt.Errorf("cross-project step %s:%s: no workspace to resolve it against", ref.Project, ref.Target)
	}
	callerRel, err := filepath.Rel(ws.Root(), src.Dir)
	if err != nil {
		return "", fmt.Errorf("cross-project step: %w", err)
	}
	return file.ResolveImport(ref.Project, filepath.ToSlash(callerRel))
}
