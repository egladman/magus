package bindings

import (
	"context"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp/bindings/ffi"
	bindgen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

// registerStopRule binds magus\guard.stop onto guardMap:
//
//	magus\guard.stop(fun(req: StopRequest) > GuardVerdict {
//	    if (req.role == "root" and !req.stopHookActive) { return magus\guard.deny("two rows wait"); }
//	    return magus\guard.allow();
//	});
//
// The guard calls it on every Stop and SubagentStop its built-in stop check lets through.
func registerStopRule(ctx context.Context, sess *buzz.Session, obs buzz.DirectObserver, guardMap vm.Value) {
	registerFunctionRule(ctx, sess, obs, guardMap, functionRule{
		member:    "stop",
		signature: "fun(req: StopRequest) > GuardVerdict",
		judges:    "every agent stop",
		set: func(reg *workspace.WorkspaceRegistry, sess *buzz.Session, fn vm.Value) {
			reg.SetStopRule(buzzStopRule(sess, fn))
		},
	})
}

// buzzStopRule adapts the registered Buzz function to the guard's rule type.
func buzzStopRule(sess *buzz.Session, rule vm.Value) workspace.StopRule {
	return func(ctx context.Context, req types.StopRequest, facts hint.Gate) (types.GuardVerdict, error) {
		return callFunctionRule(ctx, sess, rule, "stop", bindgen.ObjectStopRequest(req, ffi.SessionEnums(ctx, sess, "magus")), facts)
	}
}
