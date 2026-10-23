package guard

import (
	"context"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// denyRuleLeaseWiden is lease-write for a path a proof covers; its catalog row is
// ruleDocLeaseWiden in catalog.go, where every row is a top-level var.
const denyRuleLeaseWiden denyRuleName = "lease-widen"

// gradeProvedWidening is the lease-widen refusal of rel for lease me, whose checkout is
// checkout, or no opinion when no proof covers it or the apply line runs past its budget.
// rows are every job, which the proof reads for a path another live job holds.
func gradeProvedWidening(ctx context.Context, deps Dependencies, checkout string, me types.Job, rows []types.Job, rel string) writeGrade {
	var projects []*types.Project
	if repo, err := deps.inspect(ctx, checkout); err == nil && repo != nil {
		projects = repo.All()
	}
	facts := job.ObserveWidening(ctx, checkout, me, []string{rel}, projects)
	req, proof, ok := job.ProposeWidening(me, rel, facts, rows)
	if !ok {
		return writeGrade{}
	}
	return denyLeaseWiden(me, rel, req, proof)
}

func denyLeaseWiden(me types.Job, rel string, req types.JobWidenRequest, proof string) writeGrade {
	line, ok := job.WideningCommand(me.ID, []types.JobWidenRequest{req})
	if !ok {
		return writeGrade{}
	}
	return writeGrade{Decision: decisionDeny, Rule: string(denyRuleLeaseWiden), Reason: denial{
		Say: fmt.Sprintf("magus workspace: %s is outside the write paths of lease %s, and magus proves it in scope (%s); widen the job with the command served next, then retry this write.",
			rel, me.ID, req.Because),
		Why: proof + ". The store proves it again when the record applies, and refuses it if another live job holds the path or a deny path covers it.",
	}.full(), Next: []types.NextStep{{ID: hint.DenyRemedyPrefix + string(denyRuleLeaseWiden), Run: line, Argv: strings.Fields(hint.JobApply.With("-f", "-")), Why: "the proof is the record this applies; the store proves it again under its lock."}}}
}
