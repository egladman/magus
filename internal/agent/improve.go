package agent

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/trail"
)

const (
	improveDefinition = "Candidates are recurring pre-tool guard observations. A followed replacement means only that a later magus run was requested in the same host session; pre-tool hooks cannot prove execution or success. This report never writes memory, skills, instructions, guard rules, or harness config."

	destinationDiscard = "discard"
	destinationHarness = "harness descriptor, magus-local-development, or upstream-bug"
	confidenceLow      = "low"
	confidenceHigh     = "high"
)

// ImproveOptions provides the workspace context for a read-only improvement
// review. CacheDir is passed in by the caller because cache policy belongs to
// the workspace loader, not to an agent-host integration. Apply authority lives
// on the CLI (ApplyHarness), not here.
type ImproveOptions struct {
	Root       string
	CacheDir   string
	Session    string
	Limit      int
	IncludeAll bool
	// WiredHarnesses are magusfile-selected harness spell names
	// (Magus.Harnesses). Unioned into KnownHarnesses so spell-only hosts
	// appear in coverage without a harnesses/*.json file.
	WiredHarnesses []string
}

// ImprovementCandidate is a review proposal, never an instruction to weaken a
// guard. It carries the evidence that made the proposal visible.
type ImprovementCandidate struct {
	trail.GuardFeedback
	Destination string `json:"destination"`
	Confidence  string `json:"confidence"`
	Next        string `json:"next"`
}

// ImproveReport is suitable for either human presentation or structured CLI
// output. An empty Candidates slice deliberately serializes as [] rather than
// null, so consumers can treat it as a queue.
type ImproveReport struct {
	Definition      string                 `json:"definition"`
	Candidates      []ImprovementCandidate `json:"candidates"`
	HarnessCoverage []HarnessVerification  `json:"harness_coverage,omitempty"`
	HarnessUpdates  []HarnessUpdate        `json:"harness_updates,omitempty"`
}

// Improve turns durable hook evidence into a small review queue and reports
// harness coverage. It is read-only: harness mutation is ApplyHarness via the CLI.
func Improve(ctx context.Context, opts ImproveOptions) (ImproveReport, error) {
	if err := ctx.Err(); err != nil {
		return ImproveReport{}, err
	}
	if opts.Limit < 1 {
		return ImproveReport{}, fmt.Errorf("limit must be positive (got %d)", opts.Limit)
	}
	if opts.CacheDir == "" {
		return ImproveReport{}, fmt.Errorf("activity store is required")
	}

	feedback, err := trail.RecentGuardFeedback(opts.CacheDir, opts.Session, opts.Limit)
	if err != nil {
		return ImproveReport{}, fmt.Errorf("read activity trail: %w", err)
	}
	report := ImproveReport{Definition: improveDefinition, Candidates: []ImprovementCandidate{}}
	for _, item := range feedback {
		if !opts.IncludeAll && !item.NeedsReview() {
			continue
		}
		report.Candidates = append(report.Candidates, improvementCandidateFor(item))
	}
	if opts.Root != "" {
		ctx = ContextWithWiredHarnesses(ctx, opts.WiredHarnesses)
		ids, err := KnownHarnesses(ctx, opts.Root, opts.WiredHarnesses...)
		if err != nil {
			return ImproveReport{}, fmt.Errorf("load harness descriptors: %w", err)
		}
		report.HarnessCoverage = make([]HarnessVerification, 0, len(ids))
		for _, id := range ids {
			coverage, err := VerifyHarness(ctx, opts.Root, id)
			if err != nil {
				return ImproveReport{}, fmt.Errorf("verify %s harness: %w", id, err)
			}
			report.HarnessCoverage = append(report.HarnessCoverage, coverage)
		}
	}
	return report, nil
}

func improvementCandidateFor(feedback trail.GuardFeedback) ImprovementCandidate {
	candidate := ImprovementCandidate{
		GuardFeedback: feedback,
		Destination:   destinationDiscard,
		Confidence:    confidenceLow,
		Next:          "inspect the evidence; a recurring event is a review candidate, not a rule",
	}
	if feedback.Rule == "raw-tool" {
		candidate.Destination = destinationHarness
		candidate.Confidence = confidenceHigh
		candidate.Next = "keep the guard; improve the host adapter when its delivery is the friction (`magus agent improve --apply --id <harness-id>`), otherwise decide whether a local skill or upstream change is warranted"
	}
	return candidate
}
