package guard

import (
	"context"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// gradeClaimOutside grades a write to rel outside lease me's write paths: when
// [job.DecideAutoClaim] allows, it records the claim as `magus job apply` does and passes
// the write with a line naming it; otherwise the lease-claim refusal when that line would
// apply, or no opinion. stop names what kept magus from claiming rel. rows are every job,
// and dead the live ones holding rel whose holder [deadHolder] judged dead.
func gradeClaimOutside(ctx context.Context, deps Dependencies, at location, checkout string, me types.Job, rows []types.Job, dead []job.DeadHolder, rel string) (g writeGrade, stop string) {
	// Observing the claim facts diffs the whole checkout against the checkpoint. An
	// integration row's diff is every merged branch, which took the write judgment past
	// its deadline on each write, and no edge from a merge's diff proves a claim.
	if me.Integrating() {
		return writeGrade{}, fmt.Sprintf("%s integrates %d branches, so a diff since its checkpoint proves no claim", me.ID, len(me.Integrates))
	}
	var projects []*types.Project
	if repo, err := deps.inspect(ctx, checkout); err == nil && repo != nil {
		projects = repo.All()
	}
	facts := job.ObserveClaim(ctx, checkout, me, []string{rel}, projects).WithDeadHolders(dead)
	d := job.DecideAutoClaim(me, rel, facts, rows, projects, deps.fileFanIn)
	switch {
	case d.Stop == "":
		store := job.NewStore(at.jobs()).As(job.Actor{Lease: me.ID})
		if _, err := store.Claim(ctx, me.ID, []types.JobClaimRequest{d.Request}, facts, config.Jobs{}, false); err != nil {
			return writeGrade{}, "the job store refused the claim: " + err.Error()
		}
		return claimedAdvice(me, d), ""
	case d.Appliable:
		return withStop(gradeProvedClaim(ctx, deps, checkout, me, rows, rel), d.Stop), d.Stop
	}
	return writeGrade{}, d.Stop
}

// claimedAdvice is the one line a write passes with once magus claimed its path for me.
func claimedAdvice(me types.Job, d job.AutoClaim) writeGrade {
	return writeGrade{Decision: decisionAdvise,
		Context: fmt.Sprintf("magus workspace: claimed %s for lease %s by %s: %s.", d.Request.Path, me.ID, d.Request.Because, d.Proof),
		Why: fmt.Sprintf("The claim is recorded on the job as `%s` records one, and `%s` lists the path among its write paths.",
			hint.JobApply, hint.DescribeJob.With(me.ID))}
}

// withStop adds to a refusal's verdict line why magus did not claim the path itself.
func withStop(g writeGrade, stop string) writeGrade {
	if g.Decision != decisionDeny {
		return g
	}
	say, why, _ := strings.Cut(g.Reason, "\n")
	g.Reason = denial{Say: fmt.Sprintf("%s magus did not claim it on its own: %s.", say, stop), Why: why}.full()
	return g
}

// fileFanIn is the fan-in [job.DecideAutoClaim] reads, from the index the last graph build
// wrote, current or not: a file's fan-in moves slowly, and a file the index never saw stops the claim.
func (d Dependencies) fileFanIn(file string, names []string) (int, error) {
	users := map[string]bool{}
	recorded := false
	for _, name := range names {
		if len(d.symbolIDs(name)) != 1 {
			continue
		}
		sites, _ := d.symbolSites(name)
		for _, s := range sites {
			if s.File == file {
				recorded = true
			} else {
				users[s.File] = true
			}
		}
	}
	if len(names) > 0 && !recorded {
		return 0, fmt.Errorf("the symbol index holds no record of %s, so its fan-in is unmeasured, and `%s` builds one", file, hint.GraphBuild)
	}
	return len(users), nil
}
