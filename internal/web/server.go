package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Config configures a loopback delivery server. The bind host is always 127.0.0.1 - serving
// to the network is never allowed, so it is not configurable; only the port is.
type Config struct {
	// Origin is the exact scheme://host[:port] of the page allowed to read responses; every
	// route is CORS-locked to it. Required (see [Origin]).
	Origin string
	// Port is the TCP port to bind on 127.0.0.1. The zero value binds the first available
	// ephemeral port - the usual choice, since the URL is handed to the browser and the port
	// need not be stable. Set it only to pin a known port.
	Port int
}

// server is the shared loopback HTTP core behind every tool-page delivery shape (the
// one-shot [BlobServer] and the streaming [LiveServer]). It is held by composition (a named
// field), not embedded, so each wrapper's public API stays intentional. It binds 127.0.0.1
// only, wraps its whole mux in [RequireLoopback] (defense in depth over the bind), and mounts
// each route behind a CORS gate via [server.handle] - so a route handler never touches
// loopback or CORS itself.
type server struct {
	ln     net.Listener
	srv    *http.Server
	origin string
	mux    *http.ServeMux
}

// newServer binds a loopback listener on cfg.Port (0 = first available) and prepares the
// loopback-guarded mux.
func newServer(cfg Config) (*server, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Port))
	if err != nil {
		return nil, fmt.Errorf("bind loopback server: %w", err)
	}
	s := &server{ln: ln, origin: cfg.Origin, mux: http.NewServeMux()}
	s.srv = &http.Server{Handler: RequireLoopback(s.mux), ReadHeaderTimeout: 5 * time.Second}
	return s, nil
}

// handle mounts h at pattern behind the CORS gate: every response is locked to the server's
// site origin, and an OPTIONS preflight is answered here with methods. Route handlers get a
// request they can answer directly - CORS and preflight live in this one place.
func (s *server) handle(pattern, methods string, h http.HandlerFunc) {
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.origin)
		w.Header().Set("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", methods)
			// The live viewer's fetch-based SSE sends the per-run token as an Authorization
			// header, a non-simple header that requires a preflight allowance.
			w.Header().Set("Access-Control-Allow-Headers", "Authorization")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h(w, r)
	})
}

// start serves in the background; the listener is already bound, so callers may hand out the
// address immediately.
func (s *server) start() { go func() { _ = s.srv.Serve(s.ln) }() }

// addr is the loopback "127.0.0.1:PORT" actually bound - the real port even when Config.Port
// was 0.
func (s *server) addr() string {
	return fmt.Sprintf("127.0.0.1:%d", s.ln.Addr().(*net.TCPAddr).Port)
}

// stop gracefully shuts the server down.
func (s *server) stop() {
	sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(sctx)
}
