// Package httpx owns the HTTP server core and the DNS-rebind guard shared by
// magus's HTTP servers. NewServer binds 127.0.0.1 whatever host
// it is handed; only NewNetworkServer binds another, for a caller that decided to
// serve the network. For a unix socket listener it owns the peer-uid admission
// (PeerConnContext, PeerGuard) instead, since no network interface reaches one.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"time"
)

// Server is the HTTP core: a listener, a mux, and an *http.Server with a
// header-read timeout. It generalizes the inline server hand-rolled by callers
// that need a single port with a few mounted routes and graceful, ctx-driven
// shutdown.
type Server struct {
	ln       net.Listener
	srv      *http.Server
	mux      *http.ServeMux
	patterns []string
}

// NewServer binds a loopback listener on the given address's port (0 = first
// available ephemeral port) and prepares the mux. The address's host is
// ignored: the listener is always forced onto 127.0.0.1 regardless of what
// addr names, so the server can never be exposed on a network interface.
func NewServer(addr netip.AddrPort) (*Server, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", addr.Port()))
	if err != nil {
		return nil, fmt.Errorf("bind loopback server: %w", err)
	}
	return newServer(ln), nil
}

// NewNetworkServer binds addr exactly as given, a non-loopback or unspecified host
// included, so whatever can route to that address reaches every mounted route. The caller
// owns that decision; the server's is mcp.insecure_bind.
func NewNetworkServer(addr netip.AddrPort) (*Server, error) {
	ln, err := net.Listen("tcp", addr.String())
	if err != nil {
		return nil, fmt.Errorf("bind %s: %w", addr, err)
	}
	return newServer(ln), nil
}

func newServer(ln net.Listener) *Server {
	mux := http.NewServeMux()
	return &Server{
		ln:  ln,
		mux: mux,
		srv: &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second},
	}
}

// Handle mounts h at pattern on the server's mux.
func (s *Server) Handle(pattern string, h http.Handler) {
	s.mux.Handle(pattern, h)
	s.patterns = append(s.patterns, pattern)
}

// Patterns returns every pattern mounted with Handle, in mount order. ServeMux cannot
// list its own routes, so this is what lets a test prove no route is left unguarded.
// Not safe to call concurrently with Handle.
func (s *Server) Patterns() []string {
	return slices.Clone(s.patterns)
}

// Serve runs the HTTP server until ctx is cancelled or the server fails. On
// ctx.Done() it performs a graceful Shutdown with a 5s timeout. A clean
// shutdown (http.ErrServerClosed) is swallowed and reported as nil.
func (s *Server) Serve(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() { errCh <- s.srv.Serve(s.ln) }()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.srv.Shutdown(shutCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Addr is the address actually bound: the real port even when the caller requested port 0,
// and 127.0.0.1 for a NewServer listener.
func (s *Server) Addr() netip.AddrPort {
	tcp, ok := s.ln.Addr().(*net.TCPAddr)
	if !ok {
		return netip.AddrPort{}
	}
	return netip.AddrPortFrom(tcp.AddrPort().Addr().Unmap(), tcp.AddrPort().Port())
}

// Close releases the listener of a server that will not Serve.
func (s *Server) Close() error { return s.ln.Close() }
