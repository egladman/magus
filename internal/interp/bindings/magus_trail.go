package bindings

import (
	"context"

	"github.com/egladman/magus/internal/interp/bindings/ffi"
	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/trail"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
)

// buildTrail assembles magus\trail: a session's guard record and the marks a person gives
// its rows. Hand-bound for the reason buildVCS is: every failure goes through ffi.Error so
// a caught value is the same map every other magus\* raise hands back.
func buildTrail(obs buzz.DirectObserver) vm.Value {
	t := vm.NewMap()
	t.MapSet("read", directVal(obs, "magus.trail.read", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		got, err := std.MagusTrailRead(ctx, ffi.AnyMap(args, 0))
		if err != nil {
			return vm.Null, ffi.Error(err)
		}
		return bindinggen.ObjectFeedbackTrail(got), nil
	}))
	t.MapSet("shape", directVal(obs, "magus.trail.shape", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		return ffi.StrVal(trail.CommandShape(ffi.Str(args, 0))), nil
	}))
	t.MapSet("shapes", directVal(obs, "magus.trail.shapes", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		return ffi.StrSliceVal(trail.CommandShapes(ffi.Str(args, 0))), nil
	}))
	t.MapSet("marks", directVal(obs, "magus.trail.marks", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		marks, err := std.MagusTrailMarks(ctx)
		if err != nil {
			return vm.Null, ffi.Error(err)
		}
		return ffi.ObjectSlice(marks, bindinggen.ObjectFeedbackMark), nil
	}))
	t.MapSet("mark", directVal(obs, "magus.trail.mark", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		m, err := std.MagusTrailMark(ctx, ffi.AnyMap(args, 0))
		if err != nil {
			return vm.Null, ffi.Error(err)
		}
		return bindinggen.ObjectFeedbackMark(m), nil
	}))
	return t
}
