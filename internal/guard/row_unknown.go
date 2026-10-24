package guard

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// denyUnknownRowMembers refuses every call under a row carrying members this build does
// not declare: one may widen or narrow the row, and grading as if it were absent is a
// silent default. Only the root can rebuild the hook's binary, yet this denies rather than
// advises (ADR 0013's notice) because an unjudgeable call has no safe answer.
func denyUnknownRowMembers(ctx context.Context, deps Dependencies, actingLease, _ string) denial {
	if actingLease == "" {
		return denial{}
	}
	standing := actingLeaseStanding(ctx, deps, actingLease)
	if !standing.declared {
		return denial{}
	}
	return unknownRowDenial(deps, standing.row)
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
	return denial{
		Say: "magus workspace: job " + row.ID + "'s row carries " + strings.Join(members, ", ") +
			", which " + binary + " (" + version + ", per `magus version`) does not declare, so it cannot judge this call; ask the root to rebuild the hook's ./magus.",
		Why: "A member this build does not know may widen or narrow what the row permits, and judging as if it were absent would be a guess. " +
			"The root rebuilds the ./magus the host hook runs (`" + ward.BootstrapCommand + "`), then restarts the daemon (`./magus server stop`, then `./magus server start`). " +
			"The worker cannot take that remedy, and the hook never switches to the binary the worker's own checkout built.",
	}
}
