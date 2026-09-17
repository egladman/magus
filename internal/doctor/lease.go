package doctor

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

func (r *runner) checkJobTree() types.DoctorCheck {
	return checkJobTree(r.ws.Root(), r.opts.cfg.Jobs, time.Now().Unix())
}

// checkJobTree reports live jobs nobody is left to wait on. It only reports: magus never
// transitions a row, so the finding names the exit command a person runs instead.
func checkJobTree(root string, limits config.Jobs, now int64) types.DoctorCheck {
	const name = "job-tree"

	rows, err := job.NewStore(job.Location{Root: root}).List()
	if err != nil {
		return types.DoctorCheck{Name: name, Status: types.DoctorFail, Evidence: types.EvidenceUnknown,
			Message: fmt.Sprintf("could not read the job store: %v", err)}
	}
	flagged := types.NewJobList(rows).Flag(now, limits.StaleAfter)
	var parts, details []string
	if len(flagged.Orphans) > 0 {
		parts = append(parts, fmt.Sprintf("%d live job(s) whose root job has ended: %s", len(flagged.Orphans), strings.Join(flagged.Orphans, ", ")))
	}
	if len(flagged.Stale) > 0 {
		parts = append(parts, fmt.Sprintf("%d live job(s) not updated within jobs.stale_after (%s): %s",
			len(flagged.Stale), limits.StaleAfter, strings.Join(flagged.Stale, ", ")))
	}
	if len(parts) == 0 {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Message: "every live job has a live root and a recent update"}
	}
	seen := map[string]bool{}
	for _, id := range append(slices.Clone(flagged.Orphans), flagged.Stale...) {
		if !seen[id] {
			seen[id] = true
			details = append(details, "if nobody holds it: "+hint.JobExit.With(id))
		}
	}
	details = append(details, "magus reports these and never ends a row itself")
	return types.DoctorCheck{Name: name, Status: types.DoctorAdvice, Message: strings.Join(parts, "; "), Details: details}
}

func (r *runner) checkBoundLease() types.DoctorCheck {
	return checkBoundLease(r.runCtx(), r.cacheDir(), r.ws.Root(), workspaceHarnesses(r.ws)...)
}

// checkBoundLease grades the lease this checkout is bound to: an unknown id, a
// job that is not live and a live job with no registered base each grade a write
// differently from a normal lease, and all three render exactly like a guarded
// session in the verdict itself (see leases.md#what-the-guard-enforces-under-a-lease).
//
// It reads the job store through Root alone, so a diagnostic never adopts a legacy
// cache-dir store on the way past. The MCP surface is outside what it can see: no leg
// here says anything about whether a magus tool call is judged.
//
// Named for the subject (the bound lease), not "binding": that word rhymes with
// guard-wiring / checkpoint-wiring and suggests a wiring check, which this is not.
func checkBoundLease(ctx context.Context, cacheDir, root string, wired ...string) types.DoctorCheck {
	const name = "bound-lease"

	id := job.ActingLease(cacheDir)
	if id == "" {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Message: "no lease bound; the guard advises only"}
	}
	// Asked through job.LeaseConflict rather than compared here: since the marker WINS,
	// a comparison against the resolved id can never differ from the marker, so a second
	// copy of this rule in this file would be one that silently stopped firing.
	if marker, claimed, conflicted := job.LeaseConflict(cacheDir); conflicted {
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorFail,
			Message: fmt.Sprintf("this checkout's marker binds lease %q while the environment claims %q", marker, claimed),
			Details: []string{
				"the marker is what `" + hint.JobExec.String() + "` wrote here, so magus grades every write under " +
					marker + " and ignores the claim: a record of where the work is beats an assertion a shell can rewrite",
				"unset " + trail.EnvBaggage + ", or take the lease you mean here with `" + hint.JobExec.With(claimed) + "`",
			},
		}
	}

	rows, err := job.NewStore(job.Location{Root: root}).List()
	if err != nil {
		return types.DoctorCheck{
			Name:     name,
			Status:   types.DoctorFail,
			Evidence: types.EvidenceUnknown,
			Message:  fmt.Sprintf("could not read the job store: %v", err),
		}
	}
	i := slices.IndexFunc(rows, func(lease types.Job) bool { return lease.ID == id })
	if i < 0 {
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorFail,
			Message: fmt.Sprintf("lease %q is bound here, and no row declares it", id),
			Details: []string{
				"the guard grades every write here as an unattributed edit: advisory, never denied",
				"declare the row under this id: " + hint.JobFork.With(id, "--criteria", "<criteria>"),
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
			Details: []string{"record one: " + hint.VCSCheckpoint.With("-o", "name") + ", then exec it on this lease"},
		}
	}

	if len(guardHookConfigs(ctx, root, wired...)) == 0 {
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
