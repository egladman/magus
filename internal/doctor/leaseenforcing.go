package doctor

import (
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/types"
)

// checkLeaseEnforcing answers a question none of the guard checks beside it do:
// not whether the guard is wired, but whether the lease THIS checkout is bound
// to is actually judged. An unknown id, a terminal row and a live row with no
// registered base all grade a write differently from a normal live lease, and
// every one of them renders exactly like a guarded session in the verdict
// itself (see leases.md#what-the-guard-enforces-under-a-lease). A typo'd
// BAGGAGE export or an orchestrator that reused an id after its row went
// terminal are both invisible without this.
//
// Read through ledger's existing doors only (ActingLease, Store.List): this
// check records nothing and refuses nothing, matching the ledger package's own
// rule that only the guard turns these rows into a verdict.
func (r *runner) checkLeaseEnforcing() types.DoctorCheck {
	return checkLeaseEnforcing(r.cacheDir(), r.ws.Root())
}

func checkLeaseEnforcing(cacheDir, root string) types.DoctorCheck {
	const name = "lease-enforcing"

	id := ledger.ActingLease(cacheDir)
	if id == "" {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Message: "no lease bound; the guard advises only"}
	}

	rows, err := ledger.NewStore(ledger.Location{CacheDir: cacheDir, Root: root}).List()
	if err != nil {
		return types.DoctorCheck{Name: name, Status: types.DoctorFail, Message: fmt.Sprintf("could not read the lease ledger: %v", err)}
	}
	i := slices.IndexFunc(rows, func(l types.Lease) bool { return l.ID == id })
	if i < 0 {
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorFail,
			Message: fmt.Sprintf("this checkout binds lease %q, and no row declares it", id),
			Details: []string{
				"a typo'd or stale id grades every write here as an unattributed edit: advisory, never denied",
				"declare the row under this id, or rebind: magus session lease <id>",
			},
		}
	}
	row := rows[i]

	switch row.State {
	case types.StatePass, types.StateFail, types.StateNoReturn:
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorAdvice,
			Message: fmt.Sprintf("lease %q is bound here and terminal (%s), so its rules are inert", id, row.State),
			Details: []string{"every write here grades as an unattributed edit until this checkout binds a live lease"},
		}
	}

	if row.Registered == 0 {
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorAdvice,
			Message: fmt.Sprintf("lease %q is bound here and live, but has no registered base, so every write is denied until it does", id),
			Details: []string{"record it: magus vcs checkpoint -o name, then register what it prints on this lease"},
		}
	}

	return types.DoctorCheck{Name: name, Status: types.DoctorOK, Message: fmt.Sprintf("lease %q is bound here, live, and enforcing", id)}
}
