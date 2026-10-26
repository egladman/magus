package bindings

import (
	"context"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
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
// provider returns the harness id, the key a magus\job.modelTiers hosts map is keyed by. A
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
	return harness
}
