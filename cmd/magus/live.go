package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/egladman/magus/internal/handler/viewer"
	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/recorder"
)

// beginLive, when enabled, starts an ephemeral 127.0.0.1 SSE server for the current
// run, launches the browser log viewer on it and prints the link too, so a long run
// (especially `magus affected ci`) can be watched as it streams. It returns the broadcaster to
// fold into the invocation's sinks (so every captured record fans out to the live
// stream), and a stop function to defer, which closes the broadcaster (ending the stream)
// and shuts the server down after a brief grace window. When disabled or if the server
// cannot start it returns (nil, no-op) and the run proceeds normally: --open never blocks
// a run.
//
// The link, the data, and the server are all loopback-local; the fragment carrying the
// connection details is never transmitted, so nothing about the run leaves the machine.
// This is the run-time sibling of `magus query ref --open` (a finished run) and mirrors
// `graph export --open --follow`, but streams over loopback SSE rather than the server bridge.
func beginLive(ctx context.Context, enabled bool) (*recorder.Broadcaster, func()) {
	if !enabled {
		return nil, func() {}
	}
	// The viewer page base defaults to the hosted log viewer; MAGUS_LOG_VIEWER_URL overrides
	// it for a self-hosted mirror (also how a locally-served site tests --open, since the
	// SSE server CORS-locks to this origin).
	base := defaultLogViewerURL
	if v := strings.TrimSpace(os.Getenv("MAGUS_LOG_VIEWER_URL")); v != "" {
		base = v
	}
	origin, err := httpx.ParseOrigin(base)
	if err != nil {
		slog.WarnContext(ctx, "--open could not derive the viewer origin; continuing without it", attr.Notice(""), attr.Component("magus"), attr.Error(err))
		return nil, func() {}
	}
	if err := httpx.CheckHostedPageOrigin(base, globalCfg.MCP.AllowedOrigins); err != nil {
		slog.WarnContext(ctx, "--open will not stream this run to the log viewer; continuing without it", attr.Notice(""), attr.Component("magus"), attr.Error(err),
			attr.Why("Add the origin to mcp.allowed_origins in magus.yaml, or set MAGUS_LOG_VIEWER_URL to a loopback mirror."))
		return nil, func() {}
	}
	bc := recorder.NewBroadcaster()
	ls, err := viewer.StartLive(origin, bc)
	if err != nil {
		slog.WarnContext(ctx, "--open could not start the log stream server; continuing without it", attr.Notice(""), attr.Component("magus"), attr.Error(err))
		return nil, func() {}
	}
	url := ls.ViewerURL(base)
	// Printed as well as opened, always. A browser that cannot launch (ssh, CI, a
	// headless box) is the normal case for a long run, and a failure to open must not
	// cost the reader the link, so the link goes out first and the launch is
	// best-effort on top of it. --open never blocks or fails a run.
	slog.InfoContext(ctx, "watch this run (loopback, stays on your machine):\n  "+url, attr.Notice(""))
	if err := interactive.OpenBrowser(url); err != nil {
		slog.WarnContext(ctx, "--open could not launch a browser; open the link above yourself", attr.Notice(""), attr.Component("magus"), attr.Error(err))
	}
	return bc, func() {
		bc.Close()
		ls.Stop(ctx)
	}
}

// liveSinks lifts an optional broadcaster into the sinks BeginInvocation
// accepts, empty when live streaming is off.
func liveSinks(bc *recorder.Broadcaster) []recorder.Sink {
	if bc == nil {
		return nil
	}
	return []recorder.Sink{bc.Handle}
}
