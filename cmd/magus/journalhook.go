package main

import (
	"log/slog"
	"strings"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
)

// withSessionJournal adds this invocation's session-fact handler to the capture
// fan-out, returning handlers unchanged when the store cannot be resolved.
//
// verb is the subcommand ("run", "affected") the recorded command line begins with;
// args is the rest of it.
//
// It is shared rather than written per command on purpose: `magus sessions` is a view
// of the repository, so a target result must land in the store in the same shape no
// matter which command produced it. Two copies of the wiring would drift, and the
// drift would read as a gap in the history rather than as a bug.
//
// It also covers the DAEMON, without a second wiring site: the daemon executes an
// adopted run by calling runTarget/affected itself (main.go's dispatchAdopted), so a
// forwarded run reaches this function in the daemon process. What the daemon gets wrong
// is the UNIT, not the facts - see below.
func withSessionJournal(handlers []slog.Handler, root, verb string, args []string) []slog.Handler {
	// The unit is read ONCE, here, and every fact this invocation writes carries that copy.
	// Reading it per fact would let a mid-run environment change split one session's facts
	// across two units, which is a history no producer could have meant.
	//
	// On an ADOPTED run this reads the DAEMON's environment, not the client's: proc
	// forwards argv, cwd and root over the socket and no environment at all, so a run
	// launched with MAGUS_DELEGATION_UNIT set records no unit once the daemon adopts it. Fixing it
	// means carrying the unit on the proc request and off proc's context here, which is a
	// protocol change rather than a wiring one.
	h := sessions.NewFactHandler(root, sessions.SessionStart{
		Workspace: root,
		Command:   strings.Join(append([]string{verb}, args...), " "),
		Version:   version,
		Unit:      trail.UnitFromEnv(),
	})
	if h == nil {
		return handlers
	}
	return append(handlers, h)
}
