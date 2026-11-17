package job

import (
	"context"
	"fmt"
	"io"

	"github.com/egladman/magus/libs/safetyscissors/job"
)

// PinOrAdvance is `magus job pin` and `magus job advance`, which verb says, over each of
// ids. It returns the rows the verb changed.
func PinOrAdvance(ctx context.Context, store *Store, verb string, ids []string) ([]job.Job, error) {
	var rows []job.Job
	for _, id := range ids {
		var (
			changed []job.Job
			err     error
		)
		if verb == "pin" {
			changed, err = store.PinBinary(ctx, id)
		} else {
			var row job.Job
			row, err = store.AdvanceAuthority(ctx, id)
			changed = []job.Job{row}
		}
		if err != nil {
			return nil, fmt.Errorf("magus job %s: %w", verb, err)
		}
		rows = append(rows, changed...)
	}
	return rows, nil
}

// PrintPinned writes one line per row PinOrAdvance changed.
func PrintPinned(w io.Writer, verb string, rows []job.Job) {
	for _, row := range rows {
		if verb == "pin" {
			fmt.Fprintf(w, "pinned %s for %s: %s\n", row.Worktree.Binary, row.ID, row.Worktree.BinaryDigest)
			continue
		}
		fmt.Fprintf(w, "advanced %s's authority to %s\n", row.ID, ShortRevision(row.Authority))
	}
}
