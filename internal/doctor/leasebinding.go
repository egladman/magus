package doctor

import (
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/types"
)

func (r *runner) checkLeaseBinding() types.DoctorCheck {
	return checkLeaseBinding(r.cacheDir(), r.ws.Root())
}

// checkLeaseBinding grades the lease this checkout is bound to: an unknown id, a
// row that is not live and a live row with no registered base each grade a write
// differently from a normal lease, and all three render exactly like a guarded
// session in the verdict itself (see leases.md#what-the-guard-enforces-under-a-lease).
func checkLeaseBinding(cacheDir, root string) types.DoctorCheck {
	const name = "lease-binding"

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
