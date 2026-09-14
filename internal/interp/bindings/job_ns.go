package bindings

import (
	"context"

	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// buildJobNS assembles magus\job for a magusfile or `magus buzz` script:
// list/put/register/exit/wait/clear over the job store (see internal/job,
// types.Job).
//
// Hand-bound, like cache/ci/secret/workspace above, because a Namespace's methods are
// Extern by construction (see std.Namespace): there is no Impl for codegen to reflect a
// trampoline from. Unlike those, job needs no session-scoped state (a provider
// selection, a resolver), so it takes no captured ctx: each closure calls std.MagusListJob
// et al with the CALL-TIME ctx the VM supplies, the same one every generated Impl
// trampoline uses to find the workspace on the context.
//
// Every failure goes back through bindinggen.HostError, which is what a generated
// trampoline does and what makes a caught value a MAP rather than a str: the VM turns a
// bare error into StrValue(err.Error()) and only a StructuredError into an indexable map
// (see vm.caughtValue). Every method here is Raises, so BZZ1006 forces callers to catch
// one; handing them a differently-typed value than every other magus\* method does is a
// difference they would only find at run time.
func buildJobNS(obs buzz.DirectObserver) vm.Value {
	ns := vm.NewMap()
	ns.MapSet("list", directVal(obs, "magus.job.list", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		report, err := std.MagusListJob(ctx)
		if err != nil {
			return vm.Null, bindinggen.HostError(err)
		}
		return bindinggen.AnyMapVal(report.BuzzObject()), nil
	}))
	ns.MapSet("put", directVal(obs, "magus.job.put", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		id := bindinggen.Str(args, 0)
		opts := bindinggen.AnyMap(args, 1)
		row, err := std.MagusPutJob(ctx, id, opts)
		if err != nil {
			return vm.Null, bindinggen.HostError(err)
		}
		return bindinggen.AnyMapVal(row.BuzzObject()), nil
	}))
	// register answers with a two-key map rather than the row alone, which is the shape
	// list and put use. The advice sentence is DERIVED from the row and not a field on it
	// (see job.BaseAdvice), so folding it in beside the row's own keys would put a
	// rendering where a caller reads facts; "job" and "advice" keep the two apart.
	ns.MapSet("register", directVal(obs, "magus.job.register", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		row, advice, err := std.MagusRegisterJob(ctx, bindinggen.Str(args, 0), bindinggen.Str(args, 1))
		if err != nil {
			return vm.Null, bindinggen.HostError(err)
		}
		return bindinggen.AnyMapVal(map[string]any{"job": row.BuzzObject(), "advice": advice}), nil
	}))
	ns.MapSet("exit", directVal(obs, "magus.job.exit", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		var (
			row types.Job
			err error
		)
		if omittedOptionalMap(args, 1) {
			row, err = std.MagusAbandonJob(ctx, bindinggen.Str(args, 0))
		} else {
			row, err = std.MagusExitJob(ctx, bindinggen.Str(args, 0), bindinggen.AnyMap(args, 1))
		}
		if err != nil {
			return vm.Null, bindinggen.HostError(err)
		}
		return bindinggen.AnyMapVal(row.BuzzObject()), nil
	}))
	ns.MapSet("wait", directVal(obs, "magus.job.wait", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		var result map[string]any
		if !omittedOptionalMap(args, 1) {
			result = bindinggen.AnyMap(args, 1)
		}
		status, err := std.MagusWaitJob(ctx, bindinggen.Str(args, 0), result)
		if err != nil {
			return vm.Null, bindinggen.HostError(err)
		}
		return bindinggen.AnyMapVal(status.BuzzObject()), nil
	}))
	ns.MapSet("clear", directVal(obs, "magus.job.clear", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		n, err := std.MagusClearJob(ctx)
		if err != nil {
			return vm.Null, bindinggen.HostError(err)
		}
		return bindinggen.IntVal(n), nil
	}))
	return ns
}

// omittedOptionalMap is the cross-boundary meaning of an optional map. Buzz
// materializes an omitted optional map as {}, rather than omitting its direct-call
// slot. Both job.exit and job.wait use absence to select evidence already associated
// with the row, and an empty map cannot satisfy their versioned result contract.
func omittedOptionalMap(args []vm.Value, index int) bool {
	return len(args) <= index || args[index].IsNull() || (args[index].IsMap() && len(args[index].MapKeys()) == 0)
}
