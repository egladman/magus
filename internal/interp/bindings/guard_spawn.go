package bindings

import (
	"context"

	"github.com/egladman/magus/internal/hint"
	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

// registerSpawnRule binds magus\guard.spawn onto guardMap:
//
//	magus\guard.spawn(fun(req: SpawnRequest) > GuardVerdict {
//	    if (req.model == "") { return magus\guard.deny("name a model"); }
//	    return magus\guard.allow();
//	});
//
// The guard calls it on every spawn and continuation of a subagent it sees.
func registerSpawnRule(ctx context.Context, sess *buzz.Session, obs buzz.DirectObserver, guardMap vm.Value) {
	registerFunctionRule(ctx, sess, obs, guardMap, functionRule{
		member:    "spawn",
		signature: "fun(req: SpawnRequest) > GuardVerdict",
		judges:    "every agent spawn",
		set: func(reg *workspace.WorkspaceRegistry, sess *buzz.Session, fn vm.Value) {
			reg.SetSpawnRule(buzzSpawnRule(sess, fn))
		},
	})
}

// buzzSpawnRule adapts the registered Buzz function to the guard's rule type.
func buzzSpawnRule(sess *buzz.Session, rule vm.Value) workspace.SpawnRule {
	return func(ctx context.Context, req types.SpawnRequest, facts hint.Gate) (types.GuardVerdict, error) {
		return callFunctionRule(ctx, sess, rule, "spawn", bindinggen.ObjectSpawnRequest(req), facts)
	}
}
