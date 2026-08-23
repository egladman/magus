package main

import (
	"context"
	"log/slog"
	"strings"

	"github.com/egladman/magus/internal/proc"
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
// forwarded run reaches this function in the daemon process. ctx is what tells the two
// cases apart - see below.
func withSessionJournal(ctx context.Context, handlers []slog.Handler, root, verb string, args []string) []slog.Handler {
	// The delegation is read ONCE, here, and every fact this invocation writes carries that copy.
	// Reading it per fact would let a mid-run environment change split one session's facts
	// across two delegations, which is a history no producer could have meant.
	//
	// On an ADOPTED run the environment belongs to the DAEMON, not to whoever asked for the
	// run, so the environment channel alone leaves every forwarded run unattributed. proc
	// carries the client's delegation on the request and lands it on ctx, and that wins here:
	// it is the claim of the process that asked, which is the same precedence trail documents
	// between a delegation marker and MAGUS_DELEGATION. A plain CLI run carries none on ctx and
	// falls through to its own environment.
	delegation := proc.DelegationFromContext(ctx)
	if delegation == "" {
		delegation = trail.DelegationFromEnv()
	}
	h := sessions.NewFactHandler(root, sessions.SessionStart{
		Workspace:  root,
		Command:    strings.Join(append([]string{verb}, args...), " "),
		Version:    version,
		Delegation: delegation,
	})
	if h == nil {
		return handlers
	}
	return append(handlers, h)
}
