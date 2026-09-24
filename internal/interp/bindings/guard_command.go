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

// registerCommandRule binds magus\guard.command onto guardMap:
//
//	magus\guard.command(fun(req: CommandRequest) > GuardVerdict {
//	    foreach (c in req.commands) {
//	        if (c.program == "curl") { return magus\guard.deny("no network from an agent shell"); }
//	    }
//	    return magus\guard.allow();
//	});
//
// The guard calls it on every shell command an agent is about to run, after its built-in
// rules. It is the function form of magus\guard.shell's declarative rules, for a decision
// a program-and-args match cannot express.
func registerCommandRule(ctx context.Context, sess *buzz.Session, obs buzz.DirectObserver, guardMap vm.Value) {
	registerFunctionRule(ctx, sess, obs, guardMap, functionRule{
		member:    "command",
		signature: "fun(req: CommandRequest) > GuardVerdict",
		judges:    "every agent shell command",
		set: func(reg *workspace.WorkspaceRegistry, sess *buzz.Session, fn vm.Value) {
			reg.SetCommandRule(buzzCommandRule(sess, fn))
		},
	})
}

// buzzCommandRule adapts the registered Buzz function to the guard's rule type.
func buzzCommandRule(sess *buzz.Session, rule vm.Value) workspace.CommandRule {
	return func(ctx context.Context, req types.CommandRequest, facts hint.Gate) (types.GuardVerdict, error) {
		return callFunctionRule(ctx, sess, rule, "command", bindinggen.ObjectCommandRequest(req), facts)
	}
}
