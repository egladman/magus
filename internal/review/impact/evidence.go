package impact

import (
	"fmt"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/text/plural"
)

// evidenceLines names the questions this changeset's authors put to magus, so a reviewer
// can audit the reasoning behind it and not just its result.
//
// One line per distinct subject, carrying no answer text. The section sits beside a diff someone
// is in the middle of reading, and a transcript of what each answer said would cost more
// attention than the whole report repays. A reader who wants an answer back re-asks the question.
func evidenceLines(hits []trail.Consultation, gap trail.ConsultGap) []string {
	if len(hits) == 0 {
		return evidenceGapLines(gap)
	}
	out := []string{fmt.Sprintf("EVIDENCE: %d question%s the authors asked magus while writing this",
		len(hits), plural.Word(len(hits), "", "s"))}
	for _, h := range capList(hits) {
		// A trailing space on a subjectless verb reads as a truncated line.
		line := "      " + h.Verb
		if h.Subject != "" {
			line += " " + h.Subject
		}
		if h.Count > 1 {
			line += fmt.Sprintf(" (x%d)", h.Count)
		}
		out = append(out, line)
	}
	return append(out, moreLine(len(hits))...)
}

// evidenceGapLines says which silence an empty section is. [trail.ConsultGap] carries why
// the four have to read differently; this is the sentence each one earns.
func evidenceGapLines(gap trail.ConsultGap) []string {
	switch gap {
	case trail.ConsultGapNoQuestions:
		return []string{"EVIDENCE: the authors of this changeset ran no magus read verb"}
	case trail.ConsultGapUnleased:
		return []string{
			"EVIDENCE: the agent activity recorded here carries no lease, so no question joins this change",
			"      Set BAGGAGE magus.lease on the agent and its questions join its writes.",
		}
	case trail.ConsultGapUnmatched:
		return []string{
			"EVIDENCE: no lease recorded here wrote these files, so their questions sit elsewhere",
			"      The work was done in another checkout, or it has aged out of the trail.",
		}
	default:
		return []string{
			"EVIDENCE: no agent activity recorded in this checkout, so what the authors consulted is unknown",
			fmt.Sprintf("      This record is per checkout, and wiring it is `%s`.", hint.AgentInstall.String()),
		}
	}
}
