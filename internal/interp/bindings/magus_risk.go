package bindings

import (
	"context"

	"github.com/egladman/magus/internal/interp/bindings/ffi"
	bindgen "github.com/egladman/magus/internal/interp/bindings/gen"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
)

// buildRisk assembles magus\risk, the tier magus proves for a change. Hand-bound for the
// reason buildJob is: every failure goes through ffi.Error so a caught value is the same
// map every other magus\* raise hands back.
func buildRisk(obs buzz.DirectObserver) vm.Value {
	risk := vm.NewMap()
	risk.MapSet("assess", directVal(obs, "magus.risk.assess", func(ctx context.Context, args []vm.Value) (vm.Value, error) {
		report, err := std.MagusRiskAssess(ctx, ffi.String(args, 0), ffi.AnyMap(args, 1))
		if err != nil {
			return vm.Null, ffi.Error(err)
		}
		return bindgen.ObjectRiskReport(report), nil
	}))
	return risk
}
