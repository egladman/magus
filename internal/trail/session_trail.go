package trail

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// sessionTrailEvents bounds the trail walk behind the join: recent work is what a reader
// asking about a session is asking about.
const sessionTrailEvents = 5000

// SessionTrailHere folds the trail under cacheDir for the session, reporting false when it
// holds nothing: no trail, or a session that ran elsewhere, reads the same. An error is a
// trail that could not be read.
func SessionTrailHere(ctx context.Context, cacheDir, session string) (SessionTrail, bool, error) {
	tr, err := ForSession(ctx, cacheDir, session, sessionTrailEvents)
	if err != nil {
		return SessionTrail{}, false, err
	}
	return tr, tr.Commands > 0 || len(tr.Spawns) > 0, nil
}

// RenderSessionTrail prints what the guard trail recorded for a session.
func RenderSessionTrail(w io.Writer, tr *SessionTrail) {
	fmt.Fprintf(w, "\nGuard trail in this checkout: %d command(s) observed, %d denied", tr.Commands, tr.Denied)
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
