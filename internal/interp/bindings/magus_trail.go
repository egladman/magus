package bindings

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/interp/bindings/ffi"
	bindgen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/trail"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// buildTrail assembles magus\trail: a session's guard record and the marks a person gives
// its rows. Hand-bound for the reason buildVCS is: every failure goes through ffi.Error so
// a caught value is the same map every other magus\* raise hands back.
func buildTrail(ctx context.Context, sess *buzz.Session, obs buzz.DirectObserver) vm.Value {
	t := vm.NewMap()
	t.MapSet("read", directVal(obs, "magus.trail.read", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		opts, err := decodeTrailReadOptions(ffi.AnyMap(args, 0))
		if err != nil {
			return vm.Null, ffi.Error(err)
		}
		got, err := std.MagusTrailRead(ctx, opts)
		if err != nil {
			return vm.Null, ffi.Error(err)
		}
		return bindgen.ObjectTrail(got), nil
	}))
	t.MapSet("shape", directVal(obs, "magus.trail.shape", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		return ffi.StringVal(trail.CommandShape(ffi.String(args, 0))), nil
	}))
	t.MapSet("shapes", directVal(obs, "magus.trail.shapes", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		return ffi.StringSliceVal(trail.CommandShapes(ffi.String(args, 0))), nil
	}))
	t.MapSet("marks", directVal(obs, "magus.trail.marks", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		marks, err := std.MagusTrailMarks(ctx)
		if err != nil {
			return vm.Null, ffi.Error(err)
		}
		return ffi.ObjectSlice(marks, func(m types.TrailMark) vm.Value { return bindgen.ObjectTrailMark(m, ffi.SessionEnums(ctx, sess, "magus")) }), nil
	}))
	t.MapSet("mark", directVal(obs, "magus.trail.mark", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		m, err := std.MagusTrailMark(ctx, ffi.AnyMap(args, 0))
		if err != nil {
			return vm.Null, ffi.Error(err)
		}
		return bindgen.ObjectTrailMark(m, ffi.SessionEnums(ctx, sess, "magus")), nil
	}))
	return t
}

// decodeTrailReadOptions reads magus\trail.read's opts map. An unknown key raises, since a
// misspelled option silently read as its default is a report about the wrong window, and
// so does a value that is not a string; a null value is an absent one.
func decodeTrailReadOptions(in map[string]any) (types.TrailReadOptions, error) {
	var opts types.TrailReadOptions
	fields := map[string]*string{"session": &opts.Session, "since": &opts.Since, "until": &opts.Until}
	for k, v := range in {
		dst, known := fields[k]
		if !known {
			return types.TrailReadOptions{}, fmt.Errorf("trail.read: unknown option %q, options are session, since, until", k)
		}
		if v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok {
			return types.TrailReadOptions{}, fmt.Errorf("trail.read: opts.%s is %T, want a string", k, v)
		}
		*dst = s
	}
	return opts, nil
}
