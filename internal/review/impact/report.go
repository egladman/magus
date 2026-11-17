package impact

import (
	"fmt"

	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/internal/trail"
)

// Report is what a disposer should know before landing: the consequences a file list
// structurally cannot show.
//
// Every field distinguishes "nothing" from "nobody measured", the same refusal diff.File
// makes with its pointers. An absent cost is a workspace with no run history, and rendering it
// as zero would tell a reader the build is free.
type Report struct {
	Reach     *reachSummary          `json:"reach,omitempty"     yaml:"reach,omitempty"`
	Ownership []projectOwner         `json:"ownership,omitempty" yaml:"ownership,omitempty"`
	Cost      *costEstimate          `json:"cost,omitempty"      yaml:"cost,omitempty"`
	Advisors  []review.AdviceSection `json:"advisors,omitempty"  yaml:"advisors,omitempty"`
	// AdvisorNotes names each advisor that could not run. Surfaced rather than swallowed, so
	// an empty advisor list reads as "they all passed" only when it is one.
	AdvisorNotes []string `json:"advisor_notes,omitempty" yaml:"advisor_notes,omitempty"`
	// AdvisorBase qualifies everything the advisors said. nil when the backend cannot date a
	// revision, which is a different fact from a base that is merely old.
	AdvisorBase *advisorBase       `json:"advisor_base,omitempty" yaml:"advisor_base,omitempty"`
	Anchors     []review.AnchorHit `json:"anchors,omitempty"      yaml:"anchors,omitempty"`
	LeftBehind  *leftBehind        `json:"left_behind,omitempty"  yaml:"left_behind,omitempty"`
	// Evidence is what this changeset's authors asked magus before writing it. It is the
	// reasoning half of the record diff.Touch carries: that one names the files an agent
	// had READ, this one the questions it put to the graph.
	Evidence []trail.Consultation `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	// EvidenceGap says which silence an empty Evidence is, so a consumer of the document can
	// tell an unresearched change from a record kept in another checkout.
	EvidenceGap trail.ConsultGap `json:"evidence_gap,omitempty" yaml:"evidence_gap,omitempty"`
	Review      *reviewSummary   `json:"review,omitempty"       yaml:"review,omitempty"`
}

// Lines renders the report, one claim per line, in the same count-then-list shape
// the file list above it uses.
//
// Lines rather than prints, so every empty form is testable without a terminal, and the empty
// forms are the half that matters. Each one says what was not measured and what would measure
// it, because a silent section reads as a clean bill of health.
func Lines(p Report) []string {
	out := []string{"IMPACT - the blast radius of landing this", ""}
	sections := [][]string{
		reachLines(p.Reach),
		ownershipLines(p.Ownership),
		costLines(p.Cost),
		advisorLines(p.Advisors, p.AdvisorNotes, p.AdvisorBase),
		anchorLines(p.Anchors),
		leftBehindLines(p.LeftBehind),
		evidenceLines(p.Evidence, p.EvidenceGap),
		reviewLines(p.Review),
	}
	for _, s := range sections {
		// A section may render nothing: REVIEW says nothing about a small change nobody
		// has disturbed. Skipping it here rather than emitting its blank separator is what
		// keeps that silence from reading as a section that broke.
		if len(s) == 0 {
			continue
		}
		if len(out) > 2 {
			out = append(out, "")
		}
		out = append(out, s...)
	}
	return out
}

// capList bounds one section's list; moreLine reports what it left off. They
// are separate because the remainder is a line in the section's own indentation, not an entry
// in the list it describes.
func capList[T any](xs []T) []T {
	if len(xs) <= listCap {
		return xs
	}
	return xs[:listCap]
}

func moreLine(n int) []string {
	if n <= listCap {
		return nil
	}
	return []string{fmt.Sprintf("      and %d more", n-listCap)}
}

// listCap bounds each section's list. A reach of sixty projects is a real answer and
// an unreadable one; the remainder is reported rather than truncated in silence.
const listCap = 10
