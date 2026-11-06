package guard

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// denyUnknownRowMembers refuses every call under a row carrying members this build does
// not declare: one may widen or narrow the row, and grading as if it were absent is a
// silent default. An unjudgeable call has no safe answer (ADR 0013's notice).
// A line that replaces the judge passes, or no way out is left.
func denyUnknownRowMembers(ctx context.Context, deps Dependencies, actingLease, command string) denial {
	if actingLease == "" {
		return denial{}
	}
	standing := actingLeaseStanding(ctx, deps, actingLease)
	if !standing.declared {
		return denial{}
	}
	if replacesJudge(ctx, deps, actingLease, command) {
		return denial{}
	}
	return unknownRowDenial(deps, standing.row)
}

// replacesJudge reports a worker's recovery line that gets a binary able to
// read the row. A pick or an abort gets none, so an interrupted one waits for
// the placement, which needs no clean tree.
func replacesJudge(ctx context.Context, deps Dependencies, lease, command string) bool {
	d := effectiveDialect(deps.ShellDialect)
	if call, ok := soleCall(command, d); ok && vcsRecoveryLine(literalWords(call.Args)) {
		return false
	}
	return recoveryLine(command, d, lease, false, hookLocation(ctx, deps))
}

// unknownRowSpawnDenial refuses a spawn bound to a row this build cannot read.
// Only the root can rebuild, so no worker is handed the row.
func unknownRowSpawnDenial(deps Dependencies, row types.Job) denial {
	if len(row.Unknown) == 0 {
		return denial{}
	}
	members := slices.Sorted(maps.Keys(row.Unknown))
	return denial{
		Say: fmt.Sprintf("magus workspace: %s (%s) cannot read %s on job %s's row; rebuild ./magus with `%s`, restart the daemon, and spawn again.",
			cmp.Or(deps.Binary, "this magus"), cmp.Or(deps.BinaryVersion, "an unknown version"), strings.Join(members, ", "), row.ID, ward.BootstrapCommand),
		Why: "A worker spawned on this row would have every call denied. " +
			"Restart the daemon with `./magus server stop`, then `./magus server start`.",
	}
}

// unknownRowDenial is the refusal for row, or none when this build declares every member
// it carries.
func unknownRowDenial(deps Dependencies, row types.Job) denial {
	if len(row.Unknown) == 0 {
		return denial{}
	}
	members := slices.Sorted(maps.Keys(row.Unknown))
	binary := cmp.Or(deps.Binary, "this magus")
	version := cmp.Or(deps.BinaryVersion, "an unknown version")
	checkout := job.WorkingCheckout(row.CheckoutRoot, row)
	if checkout == "" {
		return denial{
			Say: fmt.Sprintf("magus workspace: %s (%s) cannot read %s on job %s's row and it has no checkout; stop and report an unresolved risk.",
				binary, version, strings.Join(members, ", "), row.ID),
			Why: fmt.Sprintf("A member this build does not know may widen or narrow what the row permits, and judging as if it were absent would be a guess. "+
				"A row with no checkout holds nothing to build, so only the root can rebuild the ./magus the host hook runs (`%s`), then restarts the daemon (`./magus server stop`, then `./magus server start`).",
				ward.BootstrapCommand),
		}
	}
	hook := checkoutHook(checkout)
	if hook == "" {
		return denial{
			Say: fmt.Sprintf("magus workspace: %s (%s) cannot read %s on job %s's row; ask the root to place a ./magus that reads it.",
				binary, version, strings.Join(members, ", "), row.ID),
			Why: fmt.Sprintf("A member this build does not know may widen or narrow what the row permits, and judging as if it were absent would be a guess. "+
				"This checkout has "+job.NoCheckoutHook+", so it cannot build its own ./magus, and a placement and `git merge --ff-only <commit>` are the ways in. "+
				"The root rebuilds the ./magus the host hook runs (`%s`), then restarts the daemon (`./magus server stop`, then `./magus server start`).",
				ward.BootstrapCommand),
		}
	}
	return denial{
		Say: fmt.Sprintf("magus workspace: %s (%s) cannot read %s on job %s's row; build this checkout's ./magus, alone on its line: `%s`.",
			binary, version, strings.Join(members, ", "), row.ID, job.CheckoutBootstrap(hook, checkout)),
		Why: fmt.Sprintf("A member this build does not know may widen or narrow what the row permits, and judging as if it were absent would be a guess. "+
			"The hook hands a call to a checkout's own ./magus when it was built at HEAD, after HEAD, or after the hook's binary. "+
			"The holder of the job whose checkout this is builds one, alone on its line: `%s`. "+
			"A placement and `git merge --ff-only <commit>` still run too. "+
			"The root rebuilds the ./magus the host hook runs (`%s`), then restarts the daemon (`./magus server stop`, then `./magus server start`).",
			job.CheckoutBootstrap(hook, "<checkout>"), ward.BootstrapCommand),
	}
}
