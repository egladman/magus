package doctor

import (
	"fmt"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// checkDeferredSettle reports a settle the git hooks deferred under magus.settle=defer
// that no `magus vcs resolve` has run. The hooks start no magus while deferring, so
// nothing else says the generated output of those operations is stale.
func (r *runner) checkDeferredSettle() types.Check {
	const name = "deferred-settle"
	op, deferred, err := vcs.DeferredSettle(r.runCtx(), r.ws.Root())
	if err != nil {
		return types.Check{Name: name, Status: types.CheckFail, Message: err.Error()}
	}
	if !deferred {
		return types.Check{Name: name, Status: types.CheckOK, Message: "no settle is deferred"}
	}
	return types.Check{
		Name:    name,
		Status:  types.CheckFail,
		Message: fmt.Sprintf("a settle deferred under magus.settle=defer has not run, so output generated from the %d file(s) it changed may be stale", len(op.Changed)),
		Details: []string{
			"settle it with `" + hint.VCSResolve.String() + "`, which regenerates what changed, stages the result and clears the record",
		},
	}
}
