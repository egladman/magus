package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/service/viewer"
)

// beginLive, when enabled, starts an ephemeral 127.0.0.1 SSE server for the current
// run and prints a local log-viewer link up front, so a long run (especially `magus
// affected ci`) can be opened and watched as it streams. It returns the broadcaster to
// fold into the invocation's sinks - so every captured record fans out to the live
// stream - and a stop function to defer, which closes the broadcaster (ending the stream)
// and shuts the server down after a brief grace window. When disabled or if the server
// cannot start it returns (nil, no-op) and the run proceeds normally: --live never blocks
// a run.
//
// The link, the data, and the server are all loopback-local; the fragment carrying the
// connection details is never transmitted, so nothing about the run leaves the machine.
// This is the run-time sibling of `magus query ref --open` (a finished run) and mirrors
// `graph open --live`, but streams over loopback SSE rather than the daemon bridge.
func beginLive(ctx context.Context, enabled bool) (*journal.Broadcaster, func()) {
	if !enabled {
		return nil, func() {}
	}
	// The viewer page base defaults to the hosted log viewer; MAGUS_LOG_VIEWER_URL overrides
	// it for a self-hosted mirror (also how a locally-served site tests --live, since the
	// SSE server CORS-locks to this origin).
	base := defaultLogViewerURL
	if v := strings.TrimSpace(os.Getenv("MAGUS_LOG_VIEWER_URL")); v != "" {
		base = v
	}
	origin, err := httpx.ParseOrigin(base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "magus: --live could not derive the viewer origin (%v); continuing without it.\n", err)
		return nil, func() {}
	}
	bc := journal.NewBroadcaster()
	ls, err := viewer.StartLive(origin, bc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "magus: --live could not start the log stream server (%v); continuing without it.\n", err)
		return nil, func() {}
	}
	fmt.Fprintf(os.Stderr, "watch this run live (loopback, stays on your machine):\n  %s\n", ls.ViewerURL(base))
	return bc, func() {
		bc.Close()
		ls.Stop(ctx)
	}
}

// liveHandlers lifts an optional broadcaster into the variadic capture-handler list
// BeginInvocation accepts (empty when live streaming is off). The broadcaster is itself a
// slog.Handler, so it joins the invocation's capture logger directly.
func liveHandlers(bc *journal.Broadcaster) []slog.Handler {
	if bc == nil {
		return nil
	}
	return []slog.Handler{bc}
}
