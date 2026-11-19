package impact

import (
	"fmt"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/text/plural"
)

// evidenceLines names the questions this changeset's authors put to magus, so a reviewer
// can audit the reasoning behind it and not just its result.
//
// One line per distinct subject, carrying no answer text. The section sits beside a diff someone
// is in the middle of reading, and a transcript of what each answer said would cost more
// attention than the whole report repays. A reader who wants an answer back re-asks the question.
func evidenceLines(hits []recorder.Consultation, gap recorder.ConsultGap) []string {
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

// evidenceGapLines says which silence an empty section is. [recorder.ConsultGap] carries why
// the four have to read differently; this is the sentence each one earns.
func evidenceGapLines(gap recorder.ConsultGap) []string {
	switch gap {
	case recorder.ConsultGapNoQuestions:
		return []string{"EVIDENCE: the authors of this changeset ran no magus read verb"}
	case recorder.ConsultGapUnleased:
		return []string{
			"EVIDENCE: the agent activity recorded here carries no lease, so no question joins this change",
			"      Set BAGGAGE magus.lease on the agent and its questions join its writes.",
		}
	case recorder.ConsultGapUnmatched:
		return []string{
			"EVIDENCE: no lease recorded here wrote these files, so their questions sit elsewhere",
			"      The work was done in another checkout, or it has aged out of the trail.",
		}
	case recorder.ConsultGapNone, recorder.ConsultGapUnobserved:
	}
	return []string{
		"EVIDENCE: no agent activity recorded in this checkout, so what the authors consulted is unknown",
		fmt.Sprintf("      This record is per checkout, and wiring it is `%s`.", hint.AgentInstall.String()),
	}
}

// evidenceNoteLines names each trail record the section skipped.
func evidenceNoteLines(notes []string) []string {
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		out = append(out, "      skipped: "+n)
	}
	return out
}

// errorNotes splits err, joined or not, into one note per error.
func errorNotes(err error) []string {
	if err == nil {
		return nil
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return []string{err.Error()}
	}
	var out []string
	for _, e := range joined.Unwrap() {
		out = append(out, errorNotes(e)...)
	}
	return out
}
