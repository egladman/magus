package guard

import (
	"context"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// denyRuleLeaseClaim is lease-write for a path a proof covers; its catalog row is
// ruleDocLeaseClaim in catalog.go, where every row is a top-level var.
const denyRuleLeaseClaim denyRuleName = "lease-claim"

// gradeProvedClaim is the lease-claim refusal of rel for lease me, whose checkout is
// checkout, or no opinion when no proof covers it or the apply line runs past its budget.
// rows are every job, which the proof reads for a path another live job holds.
func gradeProvedClaim(ctx context.Context, deps Dependencies, checkout string, me types.Job, rows []types.Job, rel string) writeGrade {
	var projects []*types.Project
	if repo, err := deps.inspect(ctx, checkout); err == nil && repo != nil {
		projects = repo.All()
	}
	facts := job.ObserveClaim(ctx, checkout, me, []string{rel}, projects)
	req, proof, ok := job.ProposeClaim(me, rel, facts, rows)
	if !ok {
		return writeGrade{}
	}
	return denyLeaseClaim(me, rel, req, proof)
}

func denyLeaseClaim(me types.Job, rel string, req types.JobClaimRequest, proof string) writeGrade {
	line, ok := job.ClaimCommand(me.ID, []types.JobClaimRequest{req})
	if !ok {
		return writeGrade{}
	}
	return writeGrade{Decision: decisionDeny, Rule: string(denyRuleLeaseClaim), Reason: denial{
		Say: fmt.Sprintf("magus workspace: %s is outside the write paths of lease %s, and magus proves it in scope (%s); claim the job with the command served next, then retry this write.",
			rel, me.ID, req.Because),
		Why: proof + ". The store proves it again when the record applies, and refuses it if another live job holds the path or a deny path covers it.",
	}.full(), Next: []types.NextStep{{ID: hint.DenyRemedyPrefix + string(denyRuleLeaseClaim), Run: line, Argv: strings.Fields(hint.JobApply.With("-f", "-")), Why: "the proof is the record this applies; the store proves it again under its lock."}}}
}
