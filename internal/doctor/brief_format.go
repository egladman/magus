package doctor

import (
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// checkBriefFormat reports the brief format the workspace's magus.yaml sets for a job row
// that names none, and where it came from, so a trial of buzz briefs can be read off the
// workspace rather than remembered. It never fails: either format is a choice.
func (r *runner) checkBriefFormat(_ []*types.Project) types.Check {
	const name = "brief-format"

	agents, err := config.LoadAgents(r.root)
	if err != nil {
		return types.Check{
			Name: name, Status: types.CheckFail,
			Message: "magus.yaml's agents section does not load, so no brief format is in force",
			Details: []string{err.Error()},
		}
	}
	format, from := job.ResolveBriefFormat(types.Job{}, agents.BriefFormat)
	return types.Check{
		Name: name, Status: types.CheckOK,
		Message: "a brief to a job row that names no brief_format is " + string(format) + ", " + from,
	}
}
