package main

import (
	"context"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/internal/proc"
)

// hintWarmGraph tells a reader who just paid to rebuild the knowledge graph that a daemon
// would have kept it warm, and returns err unchanged so it can wrap a dispatch call.
//
// It is scoped to the graph-reading verbs on purpose. The daemon holds the warm graph
// (warmgraph.go), so those commands genuinely rebuild from scratch without one and the
// suggestion is a measurable claim. The same hint on `magus run` would usually be FALSE - a
// single build with no sibling invocations gains nothing from a shared concurrency pool -
// and a hint that is wrong half the time is the kind people learn to scroll past, taking the
// accurate ones with them.
//
// Nothing here gates a capability: every one of these commands works exactly the same with
// no daemon, just slower. That is the whole point of the suggestion, and the reason it is a
// hint rather than a refusal.
func hintWarmGraph(ctx context.Context, err error) error {
	if err != nil {
		return err
	}
	if servedByAnotherDaemon(ctx) {
		return err
	}
	interactive.Emit(os.Stderr, fmt.Sprintf(
		"no daemon is running, so this rebuilt the workspace graph from scratch; %s keeps it warm between commands",
		clihint.ServerStart))
	return err
}

// servedByAnotherDaemon reports whether a daemon OTHER than this process is serving, which
// is the only case where the reader already has the warm graph.
//
// The PID comparison is what makes it correct rather than merely plausible, and both of the
// cheaper checks are wrong. A magus invocation hosts its own proc server on the stable
// socket for the life of the command, so "is the stable socket live" answers yes to every
// caller; and it exports MAGUS_DAEMON_SOCKET into its OWN environment so subprocesses
// inherit adoption, so "is that variable set" answers yes too. Both report a process as
// being served by itself, and the hint would never fire. The question is not whether a
// socket answers - it is whether somebody else is on the other end.
func servedByAnotherDaemon(ctx context.Context) bool {
	// Both candidates are checked, not one in preference to the other. The stable socket is
	// where a long-lived daemon lives; MAGUS_DAEMON_SOCKET is where an adopting PARENT lives,
	// which may be a per-process server rather than the daemon. Either one belonging to
	// another process means the graph came in warm.
	var addrs []string
	if live, ok := proc.LookupStableSocket(ctx); ok {
		addrs = append(addrs, live)
	}
	if env := os.Getenv("MAGUS_DAEMON_SOCKET"); env != "" {
		addrs = append(addrs, env)
	}
	for _, addr := range addrs {
		st, err := proc.QueryStatus(ctx, addr)
		if err != nil || st == nil {
			continue
		}
		if st.ParentPID != 0 && st.ParentPID != os.Getpid() {
			return true
		}
	}
	return false
}
