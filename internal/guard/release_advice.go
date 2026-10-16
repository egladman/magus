package guard

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
)

const advisoryReleaseRequested hint.MarkerKind = "release-requested"

// adviseReleaseRequested tells the holder of actingLease about the open requests a refused
// fork filed asking it to release write paths, or nothing when there are none. A request is
// the lease's when its id is the one [sessions.RequestID] derives from the lease: the
// request records no holder of its own. It reads the attention queue and never a footprint;
// the unused paths are the ones the request's message already lists.
func adviseReleaseRequested(at location, actingLease string) advice {
	if actingLease == "" || at.workspace == "" {
		return advice{}
	}
	dir, err := sessions.Dir(at.workspace)
	if err != nil {
		return advice{}
	}
	fold, err := sessions.ReadAll(dir)
	if err != nil {
		return advice{}
	}
	var lines []string
	for _, req := range sessions.AttentionQueue(fold) {
		if req.Source != job.ReleaseRequestSource {
			continue
		}
		open := sessions.AttentionOpen{Source: req.Source, Where: req.Where, Message: req.Message}
		if sessions.RequestID(actingLease, open) != req.ID {
			continue
		}
		wanted := make([]string, len(req.Files))
		for i, f := range req.Files {
			wanted[i] = f.Value
		}
		by := "a fork"
		if req.Lease != "" {
			by = "a fork under lease " + req.Lease
		}
		lines = append(lines, fmt.Sprintf("%s was refused for %s (request %s): %s", by, strings.Join(wanted, ", "), req.ID, req.Message))
	}
	if len(lines) == 0 {
		return advice{}
	}
	return advice{
		Say: fmt.Sprintf("magus workspace: lease %s is asked to release write paths it has not used.\n%s\nThe root narrows the row with `%s`.",
			actingLease, strings.Join(lines, "\n"), hint.JobApply.With("-f", "<record>")),
		Why: "A fork that needs a file this job holds is refused until the job narrows to what it edits, exits, or the fork waits on it. " +
			"The request names the paths the job's diff has not touched, so releasing them costs the job nothing.",
	}
}

var ruleDocReleaseRequested = types.RuleDoc{
	Name:    string(advisoryReleaseRequested),
	Catches: "a job holding write paths another fork was refused for, with a release request open",
	Why: "A fork refused for a file a live job holds (MGS3032, MGS3036, MGS3046) files an attention request naming that job and the write paths its diff has not touched. " +
		"The holder is told once a session, on its own calls and never on a deny, which fork wanted what and the `magus job apply -f <record>` that narrows it. " +
		"It reads the attention queue, never a diff, and a session with no lease never sees it. Disposing of the request silences it.",
}
