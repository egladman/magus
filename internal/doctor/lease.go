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

func (r *runner) checkJobTree() types.Check {
	return checkJobTree(r.ws.Root(), r.opts.cfg.Jobs, time.Now().Unix())
}

// checkJobTree reports live jobs nobody is left to wait on, including a job blocked on a
// dependency that ended without passing. It only reports: magus never
// transitions a row, so the finding names the exit command a person runs instead.
func checkJobTree(root string, limits config.Jobs, now int64) types.Check {
	const name = "job-tree"

	rows, err := job.NewStore(job.Location{Root: root}).List()
	if err != nil {
		return types.Check{Name: name, Status: types.CheckFail, Evidence: types.EvidenceUnknown,
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
	// A job blocked on a dependency that ended without passing owns nothing and never will
	// until somebody re-plans it; one waiting on live work is the plan working.
	var stuck []string
	for _, b := range flagged.Blocked {
		if b.State == "" || b.State.Terminal() {
			stuck = append(stuck, b.Job)
			parts = append(parts, b.Job+" is "+b.String())
		}
	}
	if len(parts) == 0 {
		return types.Check{Name: name, Status: types.CheckOK, Message: "every live job has a live root and a recent update"}
	}
	seen := map[string]bool{}
	for _, id := range append(append(slices.Clone(flagged.Orphans), flagged.Stale...), stuck...) {
		if !seen[id] {
			seen[id] = true
			details = append(details, "if nobody holds it: "+hint.JobExit.With(id))
		}
	}
	details = append(details, "magus reports these and never ends a row itself")
	return types.Check{Name: name, Status: types.CheckAdvice, Message: strings.Join(parts, "; "), Details: details}
}

func (r *runner) checkBoundLease() types.Check {
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
func checkBoundLease(ctx context.Context, cacheDir, root string, wired ...string) types.Check {
	const name = "bound-lease"

	claimed := trail.LeaseFromEnv()
	id, from, err := job.ActingLease(cacheDir, claimed)
	if err != nil {
		return types.Check{Name: name, Status: types.CheckFail, Message: err.Error()}
	}
	if id == "" {
		return types.Check{Name: name, Status: types.CheckOK, Message: "no lease bound; the guard advises only"}
	}
	if from == types.LeaseSourceContested {
		return types.Check{
			Name:    name,
			Status:  types.CheckFail,
			Message: fmt.Sprintf("this checkout's marker binds lease %q while the environment claims %q", id, claimed),
			Details: []string{
				"the marker is what `" + hint.JobExec.String() + "` wrote here, so magus grades every write under " +
					id + " and ignores the claim: a record of where the work is beats an assertion a shell can rewrite",
				"unset " + trail.EnvBaggage + ", or take the lease you mean here with `" + hint.JobExec.With(claimed) + "`",
			},
		}
	}

	rows, err := job.NewStore(job.Location{Root: root}).List()
	if err != nil {
		return types.Check{
			Name:     name,
			Status:   types.CheckFail,
			Evidence: types.EvidenceUnknown,
			Message:  fmt.Sprintf("could not read the job store: %v", err),
		}
	}
	i := slices.IndexFunc(rows, func(lease types.Job) bool { return lease.ID == id })
	if i < 0 {
		return types.Check{
			Name:    name,
			Status:  types.CheckFail,
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
		return types.Check{
			Name:    name,
			Status:  types.CheckFail,
			Message: fmt.Sprintf("lease %q is bound here and not live (%s), so its lease-scoped rules are inert", id, state),
			Details: []string{"every write here grades as an unattributed edit until this checkout binds a live lease"},
		}
	}

	if row.Registered == 0 {
		return types.Check{
			Name:    name,
			Status:  types.CheckFail,
			Message: fmt.Sprintf("lease %q is bound here and live, but has no registered base, so the guard denies every write until one is recorded", id),
			Details: []string{"record one: " + hint.VCSCheckpoint.With("-o", "name") + ", then exec it on this lease"},
		}
	}

	if len(guardHookConfigs(ctx, root, wired...)) == 0 {
		return types.Check{
			Name:    name,
			Status:  types.CheckAdvice,
			Message: fmt.Sprintf("lease %q is bound here, live and registered, but no host hook config in this checkout invokes the guard", id),
			Details: []string{"the guard-wiring check names what is missing; the MCP surface is not checked here"},
		}
	}

	return types.Check{
		Name:    name,
		Status:  types.CheckOK,
		Message: fmt.Sprintf("lease %q is bound here, live, registered, and a host hook is wired to judge it", id),
	}
}
