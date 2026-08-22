package bindings

import (
	"context"

	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
)

// buildLedgerNS assembles magus\ledger for a magusfile or `magus buzz` script:
// list/put/register/clear over the delegation ledger (see internal/ledger,
// types.DelegationUnit).
//
// Hand-bound, like cache/ci/secret/workspace above, because a Namespace's methods are
// Extern by construction (see std.Namespace) - there is no Impl for codegen to reflect a
// trampoline from. Unlike those, ledger needs no session-scoped state (a provider
// selection, a resolver), so it takes no captured ctx: each closure calls std.MagusListLedger
// et al with the CALL-TIME ctx the VM supplies, the same one every generated Impl
// trampoline uses to find the workspace on the context.
//
// Every failure goes back through bindinggen.HostError, which is what a generated
// trampoline does and what makes a caught value a MAP rather than a str: the VM turns a
// bare error into StrValue(err.Error()) and only a StructuredError into an indexable map
// (see vm.caughtValue). Every method here is Raises, so BZZ1006 forces callers to catch
// one - handing them a differently-typed value than every other magus\* method does is a
// difference they would only find at run time.
func buildLedgerNS(obs buzz.DirectObserver) vm.Value {
	ns := vm.NewMap()
	ns.MapSet("list", directVal(obs, "magus.ledger.list", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		report, err := std.MagusListLedger(ctx)
		if err != nil {
			return vm.Null, bindinggen.HostError(err)
		}
		return bindinggen.AnyMapVal(report.BuzzObject()), nil
	}))
	ns.MapSet("put", directVal(obs, "magus.ledger.put", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		id := bindinggen.Str(args, 0)
		opts := bindinggen.AnyMap(args, 1)
		unit, err := std.MagusPutLedger(ctx, id, opts)
		if err != nil {
			return vm.Null, bindinggen.HostError(err)
		}
		return bindinggen.AnyMapVal(unit.BuzzObject()), nil
	}))
	// register answers with a two-key map rather than the row alone, which is the shape
	// list and put use. The advice sentence is DERIVED from the row and not a field on it
	// (see ledger.RegisterAdvice), so folding it in beside the row's own keys would put a
	// rendering where a caller reads facts; "unit" and "advice" keep the two apart.
	ns.MapSet("register", directVal(obs, "magus.ledger.register", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		unit, advice, err := std.MagusRegisterLedger(ctx, bindinggen.Str(args, 0), bindinggen.Str(args, 1))
		if err != nil {
			return vm.Null, bindinggen.HostError(err)
		}
		return bindinggen.AnyMapVal(map[string]any{"unit": unit.BuzzObject(), "advice": advice}), nil
	}))
	ns.MapSet("clear", directVal(obs, "magus.ledger.clear", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		n, err := std.MagusClearLedger(ctx)
		if err != nil {
			return vm.Null, bindinggen.HostError(err)
		}
		return bindinggen.IntVal(n), nil
	}))
	return ns
}
