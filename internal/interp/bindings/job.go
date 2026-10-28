package bindings

import (
	"bytes"
	"context"
	"fmt"

	"github.com/egladman/magus/internal/interp/bindings/ffi"
	bindgen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// buildJob assembles magus\job for a magusfile or `magus buzz` script:
// list/put/register/exit/wait/clear over the job store (see internal/job,
// types.Job).
//
// Hand-bound, like cache/ci/secret/workspace above, because a Namespace's methods are
// Extern by construction (see std.Namespace): there is no Impl for codegen to reflect a
// trampoline from. The store members need no session-scoped state, so each closure calls
// std.MagusListJob et al with the CALL-TIME ctx the VM supplies, the same one every
// generated Impl trampoline uses to find the workspace on the context. modelTiers and
// modelTier keep functions the magusfile passes, so they take the load's ctx and
// session, as the guard rules do.
//
// Every failure goes back through ffi.Error, which is what a generated
// trampoline does and what makes a caught value a MAP rather than a str: the VM turns a
// bare error into StringValue(err.Error()) and only a StructuredError into an indexable map
// (see vm.caughtValue). Every method here is Raises, so BZZ1006 forces callers to catch
// one; handing them a differently-typed value than every other magus\* method does is a
// difference they would only find at run time.
func buildJob(ctx context.Context, sess *buzz.Session, obs buzz.DirectObserver) vm.Value {
	ns := vm.NewMap()
	ns.MapSet("list", directVal(obs, "magus.job.list", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		report, err := std.MagusListJob(ctx)
		if err != nil {
			return vm.Null, ffi.Error(err)
		}
		return bindgen.ObjectJobList(report, ffi.SessionEnums(ctx, sess, "magus")), nil
	}))
	ns.MapSet("put", directVal(obs, "magus.job.put", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		put, err := job.DecodePut(ffi.AnyMap(args, 1))
		if err != nil {
			return vm.Null, ffi.Error(err)
		}
		row, err := std.MagusPutJob(ctx, ffi.String(args, 0), put)
		if err != nil {
			return vm.Null, ffi.Error(err)
		}
		return bindgen.ObjectJob(row, ffi.SessionEnums(ctx, sess, "magus")), nil
	}))
	// register answers with a two-key map rather than the row alone, which is the shape
	// list and put use. The advice sentence is DERIVED from the row and not a field on it
	// (see job.BaseAdvice), so folding it in beside the row's own keys would put a
	// rendering where a caller reads facts; "job" and "advice" keep the two apart.
	ns.MapSet("register", directVal(obs, "magus.job.register", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		row, advice, err := std.MagusRegisterJob(ctx, ffi.String(args, 0))
		if err != nil {
			return vm.Null, ffi.Error(err)
		}
		out := vm.NewMap()
		out.MapSet("job", bindgen.ObjectJob(row, ffi.SessionEnums(ctx, sess, "magus")))
		out.MapSet("advice", ffi.StringVal(advice))
		return out, nil
	}))
	ns.MapSet("exit", directVal(obs, "magus.job.exit", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		var (
			row types.Job
			err error
		)
		if omittedOptionalMap(args, 1) {
			row, err = std.MagusAbandonJob(ctx, ffi.String(args, 0))
		} else {
			var result types.JobResult
			if result, err = decodeJobResult(ffi.AnyMap(args, 1)); err == nil {
				row, err = std.MagusExitJob(ctx, ffi.String(args, 0), result)
			}
		}
		if err != nil {
			return vm.Null, ffi.Error(err)
		}
		return bindgen.ObjectJob(row, ffi.SessionEnums(ctx, sess, "magus")), nil
	}))
	ns.MapSet("wait", directVal(obs, "magus.job.wait", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		var result *types.JobResult
		if !omittedOptionalMap(args, 1) {
			decoded, err := decodeJobResult(ffi.AnyMap(args, 1))
			if err != nil {
				return vm.Null, ffi.Error(err)
			}
			result = &decoded
		}
		status, err := std.MagusWaitJob(ctx, ffi.String(args, 0), result)
		if err != nil {
			return vm.Null, ffi.Error(err)
		}
		return bindgen.ObjectJobStatus(status, ffi.SessionEnums(ctx, sess, "magus")), nil
	}))
	ns.MapSet("clear", directVal(obs, "magus.job.clear", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		n, err := std.MagusClearJob(ctx)
		if err != nil {
			return vm.Null, ffi.Error(err)
		}
		return ffi.IntVal(n), nil
	}))
	registerModelTiers(ctx, sess, obs, ns)
	registerModelTierMatrix(ctx, sess, obs, ns)
	return ns
}

// omittedOptionalMap is the cross-boundary meaning of an optional map. Buzz
// materializes an omitted optional map as {}, rather than omitting its direct-call
// slot. Both job.exit and job.wait use absence to select evidence already associated
// with the row, and an empty map cannot satisfy their versioned result contract.
func omittedOptionalMap(args []vm.Value, index int) bool {
	return len(args) <= index || args[index].IsNull() || (args[index].IsMap() && len(args[index].MapKeys()) == 0)
}

// decodeJobResult reads a result map through the versioned, strict decoder a JSON result
// uses. Buzz's untyped map is the JSON-shaped contract, not a partial Go struct: an
// unknown field is refused here too, or a result sent through a Buzz target could claim a
// field no verifier reads.
func decodeJobResult(result map[string]any) (types.JobResult, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return types.JobResult{}, fmt.Errorf("magus\\job: encode result: %w", err)
	}
	return job.DecodeResult(bytes.NewReader(raw))
}
