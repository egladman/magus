package job

import (
	"fmt"
	"slices"

	"github.com/egladman/magus/libs/safetyscissors/job"
)

// EntryAdvice is what an entry is answered with: what the entrant may now do, and once.
func EntryAdvice(row job.Job, rel string) string {
	return fmt.Sprintf("entered %s on %s (%d of %d entries): your next write to it passes once its holder has been idle for a minute, and is recorded on the job",
		rel, row.ID, len(row.Entries), job.MaxJobEntries)
}

// Entries is the entries recorded on job id in rows, nil when rows holds no such job.
func Entries(rows []job.Job, id string) []job.Entry {
	if i := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == id }); i >= 0 {
		return rows[i].Entries
	}
	return nil
}
