package bindings

import (
	"context"

	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
)

// buildVCS assembles magus\vcs, magus's own reading of the workspace's version control.
// Hand-bound for the reason buildJob is: every failure goes through HostError so a caught
// value is the same map every other magus\* raise hands back.
func buildVCS(obs buzz.DirectObserver) vm.Value {
	vcs := vm.NewMap()
	vcs.MapSet("checkpoint", directVal(obs, "magus.vcs.checkpoint", func(ctx context.Context, _ []vm.Value) (vm.Value, error) {
		cp, err := std.MagusVCSCheckpoint(ctx)
		if err != nil {
			return vm.Null, bindinggen.HostError(err)
		}
		return bindinggen.ObjectVCSCheckpoint(cp), nil
	}))
	return vcs
}
