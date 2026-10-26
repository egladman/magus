package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/types"
)

// checkConformanceRatchet reports the committed conformance thresholds the code now measures
// tighter than, and the known entries that no longer violate, each with the ratchet line a
// person would edit.
//
// The lens already holds both facts; what it cannot say is where they live in the record.
// A ratchet value raised past what the code measures moves no subject, so `magus diff`
// stays silent about it, and the slack it leaves shows up here as a proposal on that row.
//
// It only reports. The value to commit comes from a person reading the row, and a looser
// one is never proposed. Advice, never a failure.
func (r *runner) checkConformanceRatchet(_ []*types.Project) types.Check {
	const name = "conformance-ratchet"

	unmeasured := func(why string) types.Check {
		return types.Check{
			Name:     name,
			Status:   types.CheckOK,
			Evidence: types.EvidenceUnknown,
			Message:  "conformance thresholds were not measured: " + why,
			Details:  []string{"build the symbol index with `magus graph build`, then run doctor again"},
		}
	}

	analyzer, ok := r.ws.(types.InsightAnalyzer)
	if !ok {
		return unmeasured("this workspace exposes no conformance lens")
	}
	out, err := analyzer.Conformance(r.runContext())
	if err != nil {
		// The lens errors on a policy it cannot honor, a ratchet that fails Validate among
		// them, and a misconfiguration is a failure rather than a quiet pass.
		return types.Check{
			Name:     name,
			Status:   types.CheckFail,
			Evidence: types.EvidenceUnknown,
			Message:  "conformance thresholds could not be measured",
			Details:  []string{err.Error()},
		}
	}
	if out.Answer.Verdict == types.VerdictUnknown {
		why := "no symbol index to measure"
		if out.Answer.Reason != "" {
			why = string(out.Answer.Reason)
		}
		return unmeasured(why)
	}

	conformance := r.opts.cfg.Knowledge.Conformance
	ratchet, readError := r.readConformanceRatchet(conformance.Ratchet)

	var details []string
	var tighter int
	for _, row := range out.Rows {
		if row.Propose <= 0 {
			continue
		}
		tighter++
		percentile := conformance.Checks[row.Key.Metric][row.Key.Population].Percentile
		details = append(details, conformanceProposal(row, percentile, conformance.Ratchet, ratchet))
	}
	for _, k := range out.Stale {
		details = append(details, fmt.Sprintf("%s: known %s entry no longer violates; magus never edits the record, so delete by hand or with a workspace target: %s",
			conformanceRatchetAt(conformance.Ratchet, k.Line), k.Metric, strings.Join(k.Items, ", ")))
	}
	if readError != nil && len(details) > 0 {
		details = append(details, "ratchet lines unavailable: "+readError.Error())
	}

	if len(details) == 0 {
		return types.Check{
			Name:     name,
			Status:   types.CheckOK,
			Evidence: types.EvidenceMeasured,
			Message:  "no conformance threshold is looser than the code measures, and no known entry is stale",
		}
	}
	return types.Check{
		Name:     name,
		Status:   types.CheckAdvice,
		Evidence: types.EvidenceMeasured,
		Message: fmt.Sprintf("%d conformance threshold(s) can tighten and %d known entry(ies) no longer violate",
			tighter, len(out.Stale)),
		Details: details,
	}
}

// readConformanceRatchet reads the committed record at the workspace-relative path, or returns
// the zero record when none is configured. Lines only name a place, so a record that cannot
// be read leaves the details without them and says so, rather than hiding the finding.
func (r *runner) readConformanceRatchet(path string) (types.ConformanceRatchet, error) {
	if path == "" {
		return types.ConformanceRatchet{}, nil
	}
	data, err := os.ReadFile(filepath.Join(r.root, path))
	if err != nil {
		return types.ConformanceRatchet{}, err
	}
	return types.ReadConformanceRatchet(path, data)
}

// conformanceProposal is one row's line: what to commit, where, and what is enforced now.
func conformanceProposal(row types.ConformanceRow, percentile float64, path string, ratchet types.ConformanceRatchet) string {
	key := conformanceRowName(row.Key)
	commit := fmt.Sprintf("commit %d (p%g, n=%d)", row.Propose, percentile, row.N)
	if row.Source != types.ConformanceFromRatchet {
		return fmt.Sprintf("%s: %s, now %d from the configured anchor; add a threshold line to %s",
			key, commit, row.Value, conformanceRatchetName(path))
	}
	// A scoped key falls back to its unscoped one, the order ConformanceOptions.Effective reads.
	var line int
	for _, k := range []types.ConformanceKey{row.Key, {Metric: row.Key.Metric, Population: row.Key.Population}} {
		for _, t := range ratchet.Thresholds {
			if t.ConformanceKey == k {
				line = t.Line
			}
		}
		if line != 0 {
			break
		}
	}
	return fmt.Sprintf("%s: %s at %s, now %d", key, commit, conformanceRatchetAt(path, line), row.Value)
}

// conformanceRowName renders a key the way types does for its own errors; that helper is
// unexported there.
func conformanceRowName(k types.ConformanceKey) string {
	name := fmt.Sprintf("%s %s", string(k.Metric), string(k.Population))
	if k.Scope != "" {
		name += " " + k.Scope
	}
	return name
}

func conformanceRatchetName(path string) string {
	if path == "" {
		return "the conformance ratchet record"
	}
	return path
}

// conformanceRatchetAt is path:line, or the bare path when the line is unknown.
func conformanceRatchetAt(path string, line int) string {
	if line == 0 {
		return conformanceRatchetName(path)
	}
	return fmt.Sprintf("%s:%d", path, line)
}
