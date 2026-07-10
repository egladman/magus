// Package httpx owns the loopback-only HTTP server core and the DNS-rebind
// guard shared by magus's daemon-facing HTTP surfaces. The server binds
// 127.0.0.1 exclusively - serving to a network interface is never allowed, so
// the bind host is not configurable; only the port is taken from the caller's
// address.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// Server is the loopback-bound HTTP core: a 127.0.0.1 listener, a mux, and an
// *http.Server with a header-read timeout. It generalizes the inline server
// hand-rolled by callers that need a single loopback port with a few mounted
// routes and graceful, ctx-driven shutdown.
type Server struct {
	ln  net.Listener
	srv *http.Server
	mux *http.ServeMux
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
	mux := http.NewServeMux()
	return &Server{
		ln:  ln,
		mux: mux,
		srv: &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second},
	}, nil
}

// Handle mounts h at pattern on the server's mux.
func (s *Server) Handle(pattern string, h http.Handler) {
	s.mux.Handle(pattern, h)
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

// Addr is the loopback address actually bound - the real port even when the
// caller requested port 0.
func (s *Server) Addr() netip.AddrPort {
	return netip.AddrPortFrom(
		netip.AddrFrom4([4]byte{127, 0, 0, 1}),
		uint16(s.ln.Addr().(*net.TCPAddr).Port),
	)
}
