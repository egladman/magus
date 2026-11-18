package sessions

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// activityCalls bounds the log walk behind the join: recent work is what a
// reader asking about a session is asking about.
const activityCalls = 5000

// ActivityHere folds the activity log under cacheDir for the session and
// reports false when it holds nothing: no log and a session that ran
// elsewhere read the same. An error is a log that could not be read.
func ActivityHere(ctx context.Context, cacheDir, session string) (Activity, bool, error) {
	tr, err := readActivity(ctx, cacheDir, session, activityCalls)
	if err != nil {
		return Activity{}, false, err
	}
	return tr, tr.Commands > 0 || len(tr.Spawns) > 0, nil
}

// RenderActivity prints what the activity log recorded for a session.
func RenderActivity(w io.Writer, tr *Activity) {
	fmt.Fprintf(w, "\nGuard activity in this checkout: %d command(s) observed, %d denied", tr.Commands, tr.Denied)
	if len(tr.Leases) > 0 {
		fmt.Fprintf(w, ", under lease %s", strings.Join(tr.Leases, ", "))
	}
	fmt.Fprintln(w)
	for _, sp := range tr.Spawns {
		fmt.Fprintf(w, "  spawned %s", sp.Child)
		if sp.Lease != "" {
			fmt.Fprintf(w, " (lease %s)", sp.Lease)
		}
		// "none declared" reads as a fact about the spawn; a blank field here
		// would read as a producer that forgot to fill it in.
		model := sp.DeclaredModel
		if model == "" {
			model = "none declared"
		}
		fmt.Fprintf(w, "  model %s", model)
		fmt.Fprintf(w, "  %s\n", sp.At.Format("2006-01-02 15:04:05"))
	}
}
