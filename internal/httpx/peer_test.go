//go:build linux || darwin

package httpx

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

var needMCP = types.Need{Surface: types.SurfaceMCP, Level: types.LevelWrite}

// serveSocket serves h on a real unix socket, with each connection's peer read by
// PeerConnContext, until the test ends, and returns a client that dials it.
func serveSocket(t *testing.T, h http.Handler) *http.Client {
	t.Helper()
	// t.TempDir's path can exceed the unix socket length limit on macOS.
	dir, err := os.MkdirTemp("", "sock")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ConnContext: PeerConnContext}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		require.NoError(t, srv.Close())
		assert.ErrorIs(t, <-done, http.ErrServerClosed)
	})
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		},
	}}
}

func post(t *testing.T, c *http.Client) (int, []byte) {
	t.Helper()
	resp, err := c.Post("http://localhost/mcp", "application/json", nil) //nolint:noctx // test client carries a timeout
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

type echoed struct {
	Credential types.Credential `json:"credential"`
	EntryPoint types.EntryPoint `json:"entry_point"`
}

// credentialEcho answers with the credential and entry point the guard left on the context.
var credentialEcho = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	err := json.NewEncoder(w).Encode(echoed{
		Credential: trail.CredentialFromContext(r.Context()),
		EntryPoint: trail.EntryPointFromContext(r.Context()),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
})

func TestPeerGuardAdmitsTheServersOwnUID(t *testing.T) {
	client := serveSocket(t, PeerGuard(rpcerr.FormatJSON, os.Getuid(), types.CredentialSocketPeer, credentialEcho))

	status, body := post(t, client)
	require.Equal(t, http.StatusOK, status, "%s", body)
	var got echoed
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, echoed{Credential: types.CredentialSocketPeer, EntryPoint: types.EntryPointRPC}, got)
}

// The kernel reports this test's own uid for the connection, so a guard expecting any other
// uid is exactly the server's view of a peer running as someone else.
func TestPeerGuardRefusesAnotherUID(t *testing.T) {
	var reached atomic.Bool
	client := serveSocket(t, PeerGuard(rpcerr.FormatJSON, os.Getuid()+1, types.CredentialSocketPeer,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached.Store(true) })))

	status, body := post(t, client)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Contains(t, string(body), string(types.SocketPeerNotOwner))
	assert.False(t, reached.Load())
}

func TestPeerGuardRefusesAConnectionWithNoPeer(t *testing.T) {
	rec := httptest.NewRecorder()
	PeerGuard(rpcerr.FormatJSON, os.Getuid(), types.CredentialSocketPeer, credentialEcho).
		ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	assert.Equal(t, http.StatusForbidden, rec.Code, "a request that did not arrive on a unix socket has no peer to admit")
	assert.Contains(t, rec.Body.String(), string(types.SocketPeerNotOwner))
}

func TestPeerConnContextNamesTheKernelsPeer(t *testing.T) {
	a, b, err := unixPair()
	require.NoError(t, err)
	defer a.Close()
	defer b.Close()
	p, ok := PeerConnContext(t.Context(), a).Value(peerKey{}).(peer)
	require.True(t, ok)
	assert.Equal(t, peer{uid: os.Getuid()}, p)

	pipe, other := net.Pipe()
	defer pipe.Close()
	defer other.Close()
	p, ok = PeerConnContext(t.Context(), pipe).Value(peerKey{}).(peer)
	require.True(t, ok)
	assert.Error(t, p.err, "a connection that is not a unix socket has no peer credentials")
}

// unixPair is a connected pair of unix sockets.
func unixPair() (*net.UnixConn, *net.UnixConn, error) {
	dir, err := os.MkdirTemp("", "pair")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "p.sock"), Net: "unix"})
	if err != nil {
		return nil, nil, err
	}
	defer ln.Close()
	accepted := make(chan *net.UnixConn, 1)
	go func() {
		c, _ := ln.AcceptUnix()
		accepted <- c
	}()
	dialed, err := net.DialUnix("unix", nil, ln.Addr().(*net.UnixAddr))
	if err != nil {
		return nil, nil, err
	}
	a := <-accepted
	if a == nil {
		_ = dialed.Close()
		return nil, nil, errors.New("accept failed")
	}
	return a, dialed, nil
}

func TestGrantGuardHoldsTheCredentialOnTheContextToThePathsNeed(t *testing.T) {
	needs := map[string]types.Need{
		"/mcp":    needMCP,
		"/status": {Surface: types.SurfaceConsole, Level: types.LevelRead},
	}
	g, err := GrantGuard(rpcerr.FormatJSON, needs, credentialEcho)
	require.NoError(t, err)

	serve := func(path string, cred types.Credential) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, nil)
		g.ServeHTTP(rec, req.WithContext(trail.ContextWithCredential(req.Context(), cred)))
		return rec
	}
	viewer := types.Credential{Class: types.ClassStored, Grant: types.GrantViewer}

	assert.Equal(t, http.StatusOK, serve("/mcp", types.CredentialSocketPeer).Code)
	assert.Equal(t, http.StatusOK, serve("/status", viewer).Code)
	rec := serve("/mcp", viewer)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), string(types.GrantInsufficient))
	assert.Equal(t, http.StatusForbidden, serve("/unnamed", viewer).Code, "an unnamed path takes the strictest need")
	assert.Equal(t, http.StatusForbidden, serve("/status", types.Credential{}).Code, "no credential holds nothing")

	_, err = GrantGuard(rpcerr.FormatJSON, nil, credentialEcho)
	assert.Error(t, err)
	_, err = GrantGuard(rpcerr.FormatJSON, map[string]types.Need{"/x": {Surface: types.SurfaceMCP}}, credentialEcho)
	assert.Error(t, err)
}
