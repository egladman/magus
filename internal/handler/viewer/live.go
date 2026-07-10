// This file is the live SSE side of the viewer wire contract: an ephemeral loopback server
// that streams one invocation's journal to a local browser tool-page over Server-Sent
// Events, gated by a per-run bearer token, for `run --live`. It is built on the shared
// [httpx.Server]: it binds 127.0.0.1 only, guards each route with [httpx.RequireLoopbackPeer],
// and CORS-locks it to the single site origin serving the page. The static sibling (a JSON
// blob for `graph open --serve`) is httpx.BlobServer. It lives in the same package as the
// viewer wire encoders, so it calls EncodeEvent directly.
package viewer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/journal"
)

// liveGrace is how long the server keeps accepting connections after the run finishes,
// so a browser opened late (or reloaded) still gets the whole log before teardown.
const liveGrace = 3 * time.Second

// LiveServer streams one invocation's events to a browser over SSE, on a loopback
// [httpx.Server]. Start it with [StartLive] before the run, hand the browser
// [LiveServer.ViewerURL], let the run emit into the broadcaster, then [LiveServer.Stop] when
// done.
type LiveServer struct {
	srv    *httpx.Server
	bc     *journal.Broadcaster
	token  string
	cancel context.CancelFunc
	done   chan struct{} // closed once the background Serve has fully shut down
}

// StartLive starts a loopback SSE server on an ephemeral 127.0.0.1 port for bc in the
// background and mints a random bearer token. origin is the page allowed to read the stream.
// The caller owns bc and must add it to the invocation's capture logger so events flow, and
// call [LiveServer.Stop] when the run is finished.
func StartLive(origin string, bc *journal.Broadcaster) (*LiveServer, error) {
	s, err := httpx.NewServer(netip.AddrPort{})
	if err != nil {
		return nil, err
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("mint stream token: %w", err)
	}
	ls := &LiveServer{srv: s, bc: bc, token: hex.EncodeToString(tokenBytes), done: make(chan struct{})}
	// The shared loopback stack: peer must be loopback, CORS-locked to the page origin,
	// and the per-run token enforced by the same guard the daemon uses (accepting the
	// token as a header OR a `?token=` query param, since a browser EventSource cannot
	// set headers).
	tokenFn := func() (string, error) { return ls.token, nil }
	s.Handle("/events", httpx.RequireLoopbackPeer(httpx.CORS(origin)(httpx.BearerGuardWithQueryToken(httpx.SingleTokenVerifier(tokenFn), http.HandlerFunc(ls.streamEvents)))))

	ctx, cancel := context.WithCancel(context.Background())
	ls.cancel = cancel
	go func() { defer close(ls.done); _ = s.Serve(ctx) }()
	return ls, nil
}

// Addr is the loopback "127.0.0.1:PORT" the server bound - the value the viewer connects its
// EventSource to.
func (ls *LiveServer) Addr() string { return ls.srv.Addr().String() }

// Token is the per-run bearer token the viewer must present.
func (ls *LiveServer) Token() string { return ls.token }

// ViewerURL builds the viewer link for this live run: <logsBase>/#live=<addr>&token=<token>,
// where logsBase is the log viewer page URL (e.g. https://.../magus/logs/). BOTH the loopback
// host and the bearer token ride the URL fragment, which the browser never transmits to a
// server - so the connection details are handed to the page locally and nothing leaves the
// machine; the page reads the token and strips it from the URL.
func (ls *LiveServer) ViewerURL(logsBase string) string {
	return strings.TrimRight(logsBase, "/") + "/#live=" + url.QueryEscape(ls.Addr()) + "&token=" + url.QueryEscape(ls.token)
}

// Stop shuts the server down, allowing a brief grace window first so a late or reloading
// browser still receives the full log. Call it once the run has finished and the broadcaster
// is closed.
func (ls *LiveServer) Stop(ctx context.Context) {
	select {
	case <-time.After(liveGrace):
	case <-ctx.Done():
	}
	ls.cancel()
	<-ls.done
}

// streamEvents streams the broadcaster's backlog then its live events as SSE, ending with a
// terminal `done` event when the run finishes. The loopback guard, CORS, and the per-run
// bearer token are all enforced by the shared middleware stack wrapping this handler; by the
// time it runs the request is already authenticated, so it just subscribes.
func (ls *LiveServer) streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")

	backlog, ch, cancel := ls.bc.Subscribe()
	defer cancel()

	for _, ev := range backlog {
		if err := writeEvent(w, ev); err != nil {
			return
		}
	}
	flusher.Flush()

	for {
		select {
		case ev, open := <-ch:
			if !open {
				return
			}
			if err := writeEvent(w, ev); err != nil {
				return
			}
			flusher.Flush()
		case <-ls.bc.Done():
			// Drain any events emitted between the last read and Close, then end.
			for {
				select {
				case ev := <-ch:
					if err := writeEvent(w, ev); err != nil {
						return
					}
				default:
					fmt.Fprint(w, "event: done\ndata: {}\n\n")
					flusher.Flush()
					return
				}
			}
		case <-r.Context().Done():
			return
		}
	}
}

// writeEvent emits one event as an SSE data line (base64 protobuf). It returns a non-nil
// error if the event could not be encoded or written, so the caller ends the stream.
func writeEvent(w http.ResponseWriter, ev journal.Event) error {
	payload, err := EncodeEvent(ev)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}
	return nil
}
