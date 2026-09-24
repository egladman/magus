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

// registerWriteRule binds magus\guard.write onto guardMap:
//
//	magus\guard.write(fun(req: WriteRequest) > GuardVerdict {
//	    if (req.path.endsWith("/go.sum")) { return magus\guard.deny("run the go-mod-tidy target"); }
//	    return magus\guard.allow();
//	});
//
// The guard calls it on every file an agent writes through its host's edit tools, after
// its built-in rules.
func registerWriteRule(ctx context.Context, sess *buzz.Session, obs buzz.DirectObserver, guardMap vm.Value) {
	registerFunctionRule(ctx, sess, obs, guardMap, functionRule{
		member:    "write",
		signature: "fun(req: WriteRequest) > GuardVerdict",
		judges:    "every agent file write",
		set: func(reg *workspace.WorkspaceRegistry, sess *buzz.Session, fn vm.Value) {
			reg.SetWriteRule(buzzWriteRule(sess, fn))
		},
	})
}

// buzzWriteRule adapts the registered Buzz function to the guard's rule type.
func buzzWriteRule(sess *buzz.Session, rule vm.Value) workspace.WriteRule {
	return func(ctx context.Context, req types.WriteRequest, facts hint.Gate) (types.GuardVerdict, error) {
		return callFunctionRule(ctx, sess, rule, "write", bindinggen.ObjectWriteRequest(req), facts)
	}
}
