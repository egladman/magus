package agent

import (
	"fmt"

	"github.com/egladman/magus/internal/trail"
)

// ImproveDefinition documents the boundary of a self-improvement report. The
// activity trail can establish repeated friction, but a pre-tool hook cannot
// observe whether a requested replacement eventually executed or succeeded.
const ImproveDefinition = "Candidates are recurring pre-tool guard observations. A followed replacement means only that a later magus run was requested in the same host session; pre-tool hooks cannot prove execution or success. With --apply --host, Magus safely maintains only its own PreToolUse entries in that workspace's selected harness; it preserves other settings and never writes memory, skills, instructions, or guard rules."

// ImproveOptions provides the workspace context and explicit authority for an
// improvement review. CacheDir is passed in by the caller because cache policy
// belongs to the workspace loader, not to an agent-host integration.
type ImproveOptions struct {
	Root       string
	CacheDir   string
	Session    string
	Limit      int
	IncludeAll bool
	Apply      bool
	Host       string
	DryRun     bool
	// ActingLease is captured by the trusted CLI ingress. It is deliberately
	// data rather than an environment lookup so a nested command cannot make a
	// bound caller appear unbound by changing its environment.
	ActingLease string
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

// Improve turns durable hook evidence into a small review queue. Apply is the
// deliberate exception to an otherwise read-only operation: it refreshes only
// Magus-owned adapter entries in the selected local harness.
func Improve(opts ImproveOptions) (ImproveReport, error) {
	if opts.Limit < 1 {
		return ImproveReport{}, fmt.Errorf("--limit must be positive (got %d)", opts.Limit)
	}
	if opts.CacheDir == "" {
		return ImproveReport{}, fmt.Errorf("activity store is required")
	}
	if !opts.Apply && opts.Host != "" {
		return ImproveReport{}, fmt.Errorf("--host is only meaningful with --apply")
	}
	if opts.Apply {
		if opts.Root == "" {
			return ImproveReport{}, fmt.Errorf("workspace root is required with --apply")
		}
	}

	feedback, err := trail.RecentGuardFeedback(opts.CacheDir, opts.Session, opts.Limit)
	if err != nil {
		return ImproveReport{}, fmt.Errorf("read activity trail: %w", err)
	}
	report := ImproveReport{Definition: ImproveDefinition, Candidates: []ImprovementCandidate{}}
	for _, item := range feedback {
		if !opts.IncludeAll && !item.NeedsReview() {
			continue
		}
		report.Candidates = append(report.Candidates, improvementCandidateFor(item))
	}
	if opts.Root != "" {
		hosts, err := KnownHarnesses(opts.Root)
		if err != nil {
			return ImproveReport{}, fmt.Errorf("load harness descriptors: %w", err)
		}
		report.HarnessCoverage = make([]HarnessVerification, 0, len(hosts))
		for _, host := range hosts {
			coverage, err := VerifyHarness(opts.Root, host)
			if err != nil {
				return ImproveReport{}, fmt.Errorf("verify %s harness: %w", host, err)
			}
			report.HarnessCoverage = append(report.HarnessCoverage, coverage)
		}
	}
	if opts.Apply {
		update, err := ApplyHarness(HarnessApplyOptions{
			Root:        opts.Root,
			Host:        opts.Host,
			DryRun:      opts.DryRun,
			ActingLease: opts.ActingLease,
		})
		if err != nil {
			return ImproveReport{}, fmt.Errorf("update %s harness: %w", opts.Host, err)
		}
		report.HarnessUpdates = []HarnessUpdate{update}
	}
	return report, nil
}

func improvementCandidateFor(feedback trail.GuardFeedback) ImprovementCandidate {
	candidate := ImprovementCandidate{
		GuardFeedback: feedback,
		Destination:   "discard",
		Confidence:    "low",
		Next:          "inspect the evidence; a recurring event is a review candidate, not a rule",
	}
	if feedback.Rule == "raw-tool" {
		candidate.Destination = "harness descriptor, magus-local-development, or upstream-bug"
		candidate.Confidence = "high"
		candidate.Next = "keep the guard; improve the host adapter when its delivery is the friction (`magus agent improve --apply --host <host>`), otherwise decide whether a local skill or upstream change is warranted"
	}
	return candidate
}
