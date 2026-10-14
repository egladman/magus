package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/types"
)

// checkConformRatchet reports the committed conformance thresholds the code now measures
// tighter than, and the known entries that no longer violate, each with the ratchet line a
// person would edit.
//
// The lens already holds both facts; what it cannot say is where they live in the record.
// A ratchet value raised past what the code measures moves no subject, so `magus diff`
// stays silent about it, and the slack it leaves shows up here as a proposal on that row.
//
// It only reports. The value to commit comes from a person reading the row, and a looser
// one is never proposed. Advice, never a failure.
func (r *runner) checkConformRatchet(_ []*types.Project) types.Check {
	const name = "conform-ratchet"

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
		return unmeasured("this workspace exposes no conform lens")
	}
	out, err := analyzer.Conform(r.runCtx())
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

	conform := r.opts.cfg.Knowledge.Conform
	ratchet, readErr := r.readConformRatchet(conform.Ratchet)

	var details []string
	var tighter int
	for _, row := range out.Rows {
		if row.Propose <= 0 {
			continue
		}
		tighter++
		percentile := conform.Checks[row.Key.Metric][row.Key.Population].Percentile
		details = append(details, conformProposal(row, percentile, conform.Ratchet, ratchet))
	}
	for _, k := range out.Stale {
		details = append(details, fmt.Sprintf("%s: known %s entry no longer violates, delete: %s",
			conformRatchetAt(conform.Ratchet, k.Line), k.Metric, strings.Join(k.Items, ", ")))
	}
	if readErr != nil && len(details) > 0 {
		details = append(details, "ratchet lines unavailable: "+readErr.Error())
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

// readConformRatchet reads the committed record at the workspace-relative path, or returns
// the zero record when none is configured. Lines only name a place, so a record that cannot
// be read leaves the details without them and says so, rather than hiding the finding.
func (r *runner) readConformRatchet(path string) (types.ConformRatchet, error) {
	if path == "" {
		return types.ConformRatchet{}, nil
	}
	data, err := os.ReadFile(filepath.Join(r.root, path))
	if err != nil {
		return types.ConformRatchet{}, err
	}
	rec, err := types.ReadConformRatchet(data)
	if err != nil {
		return types.ConformRatchet{}, fmt.Errorf("%s: %w", path, err)
	}
	return rec, nil
}

// conformProposal is one row's line: what to commit, where, and what is enforced now.
func conformProposal(row types.ConformRow, percentile float64, path string, ratchet types.ConformRatchet) string {
	key := conformRowName(row.Key)
	commit := fmt.Sprintf("commit %d (p%g, n=%d)", row.Propose, percentile, row.N)
	if row.Source != types.ConformFromRatchet {
		return fmt.Sprintf("%s: %s, now %d from the configured anchor; add a thresholds entry to %s",
			key, commit, row.Value, conformRatchetName(path))
	}
	// A scoped key falls back to its unscoped one, the order ConformOptions.Effective reads.
	var line int
	for _, k := range []types.ConformKey{row.Key, {Metric: row.Key.Metric, Population: row.Key.Population}} {
		for _, t := range ratchet.Thresholds {
			if t.ConformKey == k {
				line = t.Line
			}
		}
		if line != 0 {
			break
		}
	}
	return fmt.Sprintf("%s: %s at %s, now %d", key, commit, conformRatchetAt(path, line), row.Value)
}

// conformRowName renders a key the way types does for its own errors; that helper is
// unexported there.
func conformRowName(k types.ConformKey) string {
	name := string(k.Metric) + " " + string(k.Population)
	if k.Scope != "" {
		name += " " + k.Scope
	}
	return name
}

func conformRatchetName(path string) string {
	if path == "" {
		return "the conform ratchet record"
	}
	return path
}

// conformRatchetAt is path:line, or the bare path when the line is unknown.
func conformRatchetAt(path string, line int) string {
	if line == 0 {
		return conformRatchetName(path)
	}
	return fmt.Sprintf("%s:%d", path, line)
}
