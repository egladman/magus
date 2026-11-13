package guard

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

const (
	advisoryReleaseRequested hint.MarkerKind = "release-requested"
	advisoryLeaseNarrowed    hint.MarkerKind = "lease-narrowed"
)

// adviseLeaseNarrowed tells row's holder which of its whole-file holds a fork
// was granted part of, and what the row holds there now. Like
// adviseReleaseRequested it reads only the row.
func adviseLeaseNarrowed(row types.Job) advice {
	if !job.Editing(row) {
		return advice{}
	}
	var lines []string
	for _, rel := range row.Releases {
		if !rel.Narrowed || rel.TakenBy == "" {
			continue
		}
		var held []string
		for _, entry := range row.Paths.Write {
			if p, decl := types.SplitClaim(entry); decl != "" && p == rel.Path {
				held = append(held, entry)
			}
		}
		now := "no declaration yet"
		if len(held) > 0 {
			now = strings.Join(held, ", ")
		}
		lines = append(lines, fmt.Sprintf("%s: fork %s was granted declarations you had not touched; you hold it by %s", rel.Path, rel.TakenBy, now))
	}
	if len(lines) == 0 {
		return advice{}
	}
	return advice{
		Say: fmt.Sprintf("magus workspace: lease %s now holds part of a file by declaration.\n%s\nA write to an unclaimed declaration claims it.",
			row.ID, strings.Join(lines, "\n")),
		Why: "A fork claiming only declarations this job had not touched is granted them, and the job keeps every declaration its diff changed. " +
			"A write into another job's claim is denied: that work waits on the fork, by `--depends-on` or a fold.",
	}
}

// adviseReleaseRequested tells row's holder about the refused forks asking it
// to release write paths, or nothing when there are none. It reads only the
// row: this runs on every leased call, and a store fold costs seconds.
func adviseReleaseRequested(row types.Job) advice {
	if !job.Editing(row) || len(row.ReleaseRequests) == 0 {
		return advice{}
	}
	lines := make([]string, 0, len(row.ReleaseRequests))
	for _, req := range row.ReleaseRequests {
		by := "a fork"
		if req.By != "" {
			by = "a fork under lease " + req.By
		}
		lines = append(lines, fmt.Sprintf("%s was refused for %s (request %s): %s", by, strings.Join(req.Wanted, ", "), req.ID, req.Message))
	}
	return advice{
		Say: fmt.Sprintf("magus workspace: lease %s is asked to release write paths it has not used.\n%s\nThe root narrows the row with `%s`.",
			row.ID, strings.Join(lines, "\n"), hint.JobApply.With("-f", "<record>")),
		Why: "A fork that needs a file this job holds is refused until the job narrows to what it edits, exits, or the fork waits on it. " +
			"The request names the paths the job's diff has not touched, so releasing them costs the job nothing.",
	}
}
