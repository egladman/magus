package doctor

import (
	"context"
	"fmt"

	"github.com/egladman/magus/types"
)

const checkConformanceCeilingName = "conformance-ceiling"

// checkConformanceCeiling reports the configured anchors the code now measures tighter than.
// An anchor raised past the code moves no subject, so `magus diff` is silent about it and the
// slack shows here. Per-file counts need no upkeep: a change is held to its base revision's.
// Advice, never a failure.
func (r *runner) checkConformanceCeiling(ctx context.Context, _ []*types.Project) types.Check {
	analyzer, ok := r.ws.(types.HistoryMetrics)
	if !ok {
		return conformanceUnmeasured("this workspace exposes no conformance lens")
	}
	out, err := analyzer.Conformance(ctx)
	if err != nil {
		// A misconfiguration is a failure rather than a quiet pass.
		return types.Check{
			Name:     checkConformanceCeilingName,
			Status:   types.CheckFail,
			Evidence: types.EvidenceUnknown,
			Message:  "conformance anchors could not be measured",
			Details:  []string{err.Error()},
		}
	}
	if out.Answer.Verdict == types.VerdictUnknown {
		why := "no symbol index to measure"
		if out.Answer.Reason != "" {
			why = string(out.Answer.Reason)
		}
		return conformanceUnmeasured(why)
	}
	return r.conformanceProposals(out)
}

// conformanceUnmeasured is the check when no anchor could be measured, saying why.
func conformanceUnmeasured(why string) types.Check {
	return types.Check{
		Name:     checkConformanceCeilingName,
		Status:   types.CheckOK,
		Evidence: types.EvidenceUnknown,
		Message:  "conformance anchors were not measured: " + why,
		Details:  []string{"build the symbol index with `magus graph build`, then run doctor again"},
	}
}

// conformanceProposals is the check over out's rows: advice naming each anchor that can tighten.
func (r *runner) conformanceProposals(out types.Conformance) types.Check {
	checks := r.opts.cfg.Knowledge.Conformance.Checks
	var details []string
	for _, row := range out.Rows {
		if row.Propose > 0 {
			details = append(details, conformanceProposal(row, checks[row.Key.Metric][row.Key.Population].Percentile))
		}
	}
	if len(details) == 0 {
		return types.Check{
			Name:     checkConformanceCeilingName,
			Status:   types.CheckOK,
			Evidence: types.EvidenceMeasured,
			Message:  "no conformance anchor is looser than the code measures",
		}
	}
	return types.Check{
		Name:     checkConformanceCeilingName,
		Status:   types.CheckAdvice,
		Evidence: types.EvidenceMeasured,
		Message:  fmt.Sprintf("%d conformance anchor(s) can tighten", len(details)),
		Details:  details,
	}
}

// conformanceProposal names the magus.yaml key a row's proposal lowers.
func conformanceProposal(row types.ConformanceRow, percentile float64) string {
	field := fmt.Sprintf("knowledge.conformance.checks.%s.%s.anchor", row.Key.Metric, row.Key.Population)
	if row.Key.Scope != "" {
		field = fmt.Sprintf("knowledge.conformance.checks.%s.%s.scopes.%s", row.Key.Metric, row.Key.Population, row.Key.Scope)
	}
	return fmt.Sprintf("%s: %d, the code measures %d at p%g", field, row.Value, row.Propose, percentile)
}
