package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/netip"

	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/types"
)

// TokenServer is a running loopback server on an ephemeral 127.0.0.1 port for one browser
// page. Every route admits only a loopback peer, CORS-locked to the page's origin, that
// presents the per-run token the server minted, as a bearer header or a `?token=` query
// param (a browser EventSource cannot set headers).
//
// The guard stack is assembled here once, so a server handing data to a page cannot be
// built with a guard missing.
type TokenServer struct {
	srv    *Server
	token  string
	cancel context.CancelFunc
	done   chan struct{} // closed once the background Serve has fully shut down
}

// StartTokenServer binds the port, mints the token, registers routes (pattern to handler)
// behind the guards, and serves in the background until Close.
func StartTokenServer(origin string, routes map[string]http.Handler) (*TokenServer, error) {
	s, err := NewServer(netip.AddrPort{})
	if err != nil {
		return nil, err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("httpx: mint token: %w", err)
	}
	t := &TokenServer{srv: s, token: hex.EncodeToString(raw), done: make(chan struct{})}
	// The per-run token reads its one page's data and nothing more.
	verify := SingleTokenVerifier(func() (string, error) { return t.token, nil }, types.GrantViewer)
	need := types.Need{Surface: types.SurfaceConsole, Level: types.LevelRead}
	for pattern, h := range routes {
		guarded, err := BearerGuardWithQueryToken(rpcerr.FormatJSON, verify, need, h)
		if err != nil {
			return nil, err
		}
		s.Handle(pattern, RequireLoopbackPeer(CORS(origin)(guarded)))
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	go func() { defer close(t.done); _ = s.Serve(ctx) }()
	return t, nil
}

// Close shuts the server down gracefully and waits for it, so the port is released when it
// returns. Calling it again returns at once.
func (t *TokenServer) Close() {
	t.cancel()
	<-t.done
}

// Addr is the loopback "127.0.0.1:PORT" the server bound.
func (t *TokenServer) Addr() string { return t.srv.Addr().String() }

// Token is the per-run token a page must present.
func (t *TokenServer) Token() string { return t.token }
