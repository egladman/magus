package doctor

import (
	"fmt"
	"os"
	"slices"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

func (r *runner) checkLeaseBinding() types.DoctorCheck {
	home, _ := os.UserHomeDir()
	return checkLeaseBinding(r.cacheDir(), r.ws.Root(), home)
}

// checkLeaseBinding grades the lease this checkout is bound to: an unknown id, a
// row that is not live and a live row with no registered base each grade a write
// differently from a normal lease, and all three render exactly like a guarded
// session in the verdict itself (see leases.md#what-the-guard-enforces-under-a-lease).
//
// It reads the ledger through Root alone, so a diagnostic never adopts a legacy
// cache-dir ledger on the way past. The MCP surface is outside what it can see: no leg
// here says anything about whether a magus tool call is judged.
func checkLeaseBinding(cacheDir, root, home string) types.DoctorCheck {
	const name = "lease-binding"

	id := ledger.ActingLease(cacheDir)
	if id == "" {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Message: "no lease bound; the guard advises only"}
	}
	// A host runs its hooks with its own environment, so the guard resolves the marker
	// while a worker's shell resolves what it exported: the two then grade different rows
	// and every verdict in this checkout is about a lease nobody here is acting under.
	if marker := ledger.LeaseFromMarker(cacheDir); marker != "" && marker != id {
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorFail,
			Message: fmt.Sprintf("this session acts as lease %q while this checkout's marker binds %q", id, marker),
			Details: []string{
				"a host runs its hooks with its own environment, so the guard reads the marker while this session reads " +
					trail.EnvBaggage + ": the two grade different rows",
				"unset " + trail.EnvBaggage + ", or bind this checkout to the lease the session acts as",
			},
		}
	}

	rows, err := ledger.NewStore(ledger.Location{Root: root}).List()
	if err != nil {
		return types.DoctorCheck{
			Name:     name,
			Status:   types.DoctorFail,
			Evidence: types.EvidenceUnknown,
			Message:  fmt.Sprintf("could not read the lease ledger: %v", err),
		}
	}
	i := slices.IndexFunc(rows, func(lease types.Lease) bool { return lease.ID == id })
	if i < 0 {
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorFail,
			Message: fmt.Sprintf("lease %q is bound here, and no row declares it", id),
			Details: []string{
				"the guard grades every write here as an unattributed edit: advisory, never denied",
				"declare the row under this id: " + hint.LedgerRegister.With(id, "--goal", "<goal>"),
			},
		}
	}
	row := rows[i]

	// The guard's own predicate, not a second enumeration of it: a row with no state at
	// all is not live either, and the two answering differently is the failure this
	// check exists to report.
	if !row.State.Live() {
		state := string(row.State)
		if state == "" {
			state = "no state"
		}
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorFail,
			Message: fmt.Sprintf("lease %q is bound here and not live (%s), so its lease-scoped rules are inert", id, state),
			Details: []string{"every write here grades as an unattributed edit until this checkout binds a live lease"},
		}
	}

	if row.Registered == 0 {
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorFail,
			Message: fmt.Sprintf("lease %q is bound here and live, but has no registered base, so the guard denies every write until one is recorded", id),
			Details: []string{"record one: " + hint.VCSCheckpoint.With("-o", "name") + ", then register it on this lease"},
		}
	}

	if len(guardHookConfigs(root, home)) == 0 {
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorAdvice,
			Message: fmt.Sprintf("lease %q is bound here, live and registered, but no host hook config in this checkout invokes the guard", id),
			Details: []string{"the guard-wiring check names what is missing; the MCP surface is not checked here"},
		}
	}

	return types.DoctorCheck{
		Name:    name,
		Status:  types.DoctorOK,
		Message: fmt.Sprintf("lease %q is bound here, live, registered, and a host hook is wired to judge it", id),
	}
}
