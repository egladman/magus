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
// spell as an agent-host harness (a fifth provider-style contract beside cache,
// CI, secret and review):
//
//	import "spells/harness/cursor" as cursor
//	magus\harness.provider(cursor)
//
// The spell exports harness_config / harness_skills / harness_entries; Magus
// invokes them by name on apply/verify. A workspace may use several harnesses.
func buildHarness(ctx context.Context, obs buzz.DirectObserver) vm.Value {
	harness := vm.NewMap()
	harness.MapSet("provider", directVal(obs, "magus.harness.provider", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) == 0 || !args[0].IsMap() {
			return vm.Null, fmt.Errorf(`magus\harness.provider: expected an imported spell handle`)
		}
		nv, ok := args[0].MapGet("name")
		if !ok || !nv.IsStr() {
			return vm.Null, fmt.Errorf(`magus\harness.provider: argument is not a spell handle (no name)`)
		}
		name := strings.TrimSpace(nv.AsString())
		if name == "" {
			return vm.Null, fmt.Errorf(`magus\harness.provider: argument is not a spell handle (no name)`)
		}
		if reg := workspace.WorkspaceRegistryFromContext(ctx); reg != nil {
			reg.AddHarness(name)
		}
		return vm.Null, nil
	}))
	return harness
}
