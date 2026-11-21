package bindings

import (
	"context"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/interp/bindings/ffi"
	bindgen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/interp/bindings/gen/decode"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// buildHarness assembles magus\harness for a magusfile. provider() wires an imported
// spell as an agent-host harness (a provider-style contract beside cache, CI, secret and
// review):
//
//	import "ghcr.io/egladman/magus/spells/harness/cursor";
//	magus\harness.provider(cursor);
//
// The spell exports the harness contract's mgs_ functions (internal/spell/contract.go); Magus
// calls them on describe/verify. A workspace may use several harnesses.
//
// provider returns the harness id, the key a magus\modelTiers hosts map is keyed by. A
// call that discards it wires the harness all the same.
func buildHarness(ctx context.Context, obs buzz.DirectObserver) vm.Value {
	harness := vm.NewMap()
	harness.MapSet("provider", directVal(obs, "magus.harness.provider", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsMap() {
			return vm.Null, fmt.Errorf(`magus\harness.provider: expected an imported spell handle`)
		}
		nv, ok := args[0].MapGet("name")
		if !ok || !nv.IsString() {
			return vm.Null, fmt.Errorf(`magus\harness.provider: argument is not a spell handle (no name)`)
		}
		name := strings.TrimSpace(nv.AsString())
		if name == "" {
			return vm.Null, fmt.Errorf(`magus\harness.provider: argument is not a spell handle (no name)`)
		}
		if reg := workspace.WorkspaceRegistryFromContext(ctx); reg != nil {
			reg.AddHarness(name)
		}
		return vm.StringValue(name), nil
	}))
	harness.MapSet("install", directVal(obs, "magus.harness.install", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		opts, err := harnessCommandOptions("install", args, 0)
		if err != nil {
			return vm.Null, err
		}
		out, err := std.MagusHarnessInstall(ctx, opts)
		return bindgen.ObjectExecResult(out), err
	}))
	harness.MapSet("verify", directVal(obs, "magus.harness.verify", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		opts, err := harnessCommandOptions("verify", args, 1)
		if err != nil {
			return vm.Null, err
		}
		out, err := std.MagusHarnessVerify(ctx, ffi.String(args, 0), opts)
		return bindgen.ObjectExecResult(out), err
	}))
	return harness
}

func harnessCommandOptions(member string, args []vm.Value, at int) (types.MagusCommandOptions, error) {
	if !ffi.Present(args, at) {
		return types.MagusCommandOptions{}, nil
	}
	opts, err := decode.DecodeMagusCommandOptions(args[at])
	if err != nil {
		return opts, fmt.Errorf("magus.harness.%s: opts: %w", member, err)
	}
	return opts, nil
}
