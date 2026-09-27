package bindings

import (
	"context"

	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
)

// buildMemory assembles magus\memory over the per-repository memory store. Hand-bound
// for the reason buildJob is, and like it every failure goes through HostError so a
// caught value is the same map every other magus\* raise hands back.
func buildMemory(obs buzz.DirectObserver) vm.Value {
	memory := vm.NewMap()
	record := func(name string, call func(context.Context, []vm.Value) (map[string]any, error)) {
		memory.MapSet(name, directVal(obs, "magus.memory."+name, func(ctx context.Context, args []vm.Value) (vm.Value, error) {
			rec, err := call(ctx, args)
			if err != nil {
				return vm.Null, bindinggen.HostError(err)
			}
			return bindinggen.AnyMapVal(rec), nil
		}))
	}
	record("list", func(ctx context.Context, _ []vm.Value) (map[string]any, error) {
		return std.MagusListMemory(ctx)
	})
	record("get", func(ctx context.Context, args []vm.Value) (map[string]any, error) {
		return std.MagusGetMemory(ctx, bindinggen.Str(args, 0))
	})
	record("put", func(ctx context.Context, args []vm.Value) (map[string]any, error) {
		return std.MagusPutMemory(ctx, bindinggen.Str(args, 0), bindinggen.AnyMap(args, 1))
	})
	record("verify", func(ctx context.Context, _ []vm.Value) (map[string]any, error) {
		return std.MagusVerifyMemory(ctx)
	})
	memory.MapSet("delete", directVal(obs, "magus.memory.delete", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		if err := std.MagusDeleteMemory(ctx, bindinggen.Str(args, 0)); err != nil {
			return vm.Null, bindinggen.HostError(err)
		}
		return vm.Null, nil
	}))
	return memory
}

// buildVCS assembles magus\vcs, magus's own reading of the workspace's version control.
func buildVCS(obs buzz.DirectObserver) vm.Value {
	vcs := vm.NewMap()
	vcs.MapSet("checkpoint", directVal(obs, "magus.vcs.checkpoint", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		cp, err := std.MagusVCSCheckpoint(ctx)
		if err != nil {
			return vm.Null, bindinggen.HostError(err)
		}
		return bindinggen.ObjectVCSCheckpoint(cp), nil
	}))
	return vcs
}
