package cache

import (
	"strings"

	"github.com/egladman/magus/libs/safetyscissors/budget"
	"github.com/egladman/magus/types"
)

// ExitCodeSlotDeadlock is what a deadlocked-slot refusal asks for: 78, EX_CONFIG. The
// same invocation will do the same thing on the next machine and the next run, so it is a
// configuration answer rather than a timing one, and a wrapper retrying on EX_TEMPFAIL
// must not retry this.
//
// It picks the number ExitCodeMachineDeclaration picks, for the reason that constant
// gives about the workspace lock: two independent decisions that agree, not one shared
// setting, because coupling them would move both the day either changes.
const ExitCodeSlotDeadlock = 78

// NewLimiter returns magus's limiter of capacity n (n <= 0 is unlimited): a
// [budget.Limiter] whose refusal of a wait no slot can end is MGS3013, exiting
// [ExitCodeSlotDeadlock]. opts follow that refusal, so one of them may replace it.
func NewLimiter(n int, opts ...budget.LimiterOption) *budget.Limiter {
	return budget.NewLimiter(n, append([]budget.LimiterOption{budget.WithRefusal(slotDeadlockError)}, opts...)...)
}

// slotDeadlockError builds the MGS3013 error from what the limiter saw. It carries its
// exit status the way a machine refusal does and for the same reason: a step the server
// runs for an adopted client crosses a socket that erases the Go type, and the server
// reads the code off the error.
func slotDeadlockError(d budget.SlotDeadlock) error {
	return types.ExitError{Code: ExitCodeSlotDeadlock, Err: types.DiagnosticErrorf(types.BuildSlotsDeadlocked,
		"refusing to keep waiting for a build slot: all %d of this run's slots are held by steps that are"+
			" themselves waiting, so no slot can free and nothing queued can start. Holding: %s. Queued: %s."+
			" The usual cause is a target reading what a target beside it writes with no ctx.needs between"+
			" them (see %s); order the reader after the writer. Raising concurrency widens the window rather"+
			" than closing it.",
		d.Capacity, strings.Join(d.Holding, "; "), strings.Join(d.Queued, "; "),
		types.CodeURL(types.UnorderedSameStepWrite))}
}
