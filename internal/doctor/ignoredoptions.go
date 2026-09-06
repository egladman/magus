package doctor

import (
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// checkIgnoredOptions fails when a project declares a magus.project key this binary does
// not recognize.
//
// Load tolerates such a key so a magusfile from the future cannot deadlock the binary
// that would build its successor, and the tolerance is deliberate. What it costs is that
// the policy the key declared is not in force, and several keys are opt-OUTS whose
// absence is the permissive answer: an ignored `review_required` silently shrinks the
// paths magus reports as unreviewed, and an ignored `timeout` leaves the target
// unbounded.
//
// ci.InheritOff already declines to skip CI when anything was dropped, which covers the
// one decision that could skip a gate. This covers the rest, by saying so where a person
// looks. A warning at load is seen by whoever ran that command; a doctor check is the
// thing CI and a new contributor both run.
//
// A failure, not advice: the workspace asked for something this magus cannot do.
func (r *runner) checkIgnoredOptions(projects []*types.Project) types.DoctorCheck {
	const name = "magusfile-options-understood"

	var details []string
	for _, p := range projects {
		label := types.ProjectDisplayName(p.Path, p.Name, p.Dir)
		for _, key := range p.IgnoredOptions {
			details = append(details, fmt.Sprintf("%s declares %q, which this magus dropped", label, key))
		}
	}
	if len(details) == 0 {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK,
			Message: "every magus.project key in this workspace is one this magus understands"}
	}
	slices.Sort(details)
	details = slices.Compact(details)
	return types.DoctorCheck{
		Name:   name,
		Status: types.DoctorFail,
		Message: fmt.Sprintf("%d magusfile option(s) were dropped because this magus does not recognize them, "+
			"so the policy each declares is not in force; %s", len(details), hint.IgnoredKeyAdvice()),
		Details: details,
	}
}
