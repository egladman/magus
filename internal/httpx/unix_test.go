//go:build linux || darwin

package httpx

import (
	"context"
	"encoding/json"
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

	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

var needMCP = types.Need{Surface: types.SurfaceMCP, Level: types.LevelWrite}

// socketPath is a socket in a fresh short directory: t.TempDir's path can exceed the unix
// socket length limit on macOS.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sock")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "mcp.sock")
}

// serveSocket serves h behind SocketPeerGuard for uid on a real unix socket until the test
// ends, and returns a client that dials it.
func serveSocket(t *testing.T, uid int, cred types.Credential, h http.Handler) (string, *http.Client) {
	t.Helper()
	path := socketPath(t)
	srv, err := NewUnixServer(path)
	require.NoError(t, err)
	g, err := SocketPeerGuard(rpcerr.FormatJSON, uid, cred, needMCP, h)
	require.NoError(t, err)
	srv.Handle("/mcp", g)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		assert.NoError(t, <-done)
	})
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		},
	}}
	return path, client
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

func TestSocketPeerGuardAdmitsTheServersOwnUID(t *testing.T) {
	path, client := serveSocket(t, os.Getuid(), types.CredentialSocketPeer, credentialEcho)

	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "only the owner may connect")

	status, body := post(t, client)
	require.Equal(t, http.StatusOK, status, "%s", body)
	var got echoed
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, echoed{Credential: types.CredentialSocketPeer, EntryPoint: types.EntryPointRPC}, got)
}

// The kernel reports this test's own uid for the connection, so a guard expecting any other
// uid is exactly the server's view of a peer running as someone else.
func TestSocketPeerGuardRefusesAnotherUID(t *testing.T) {
	var reached atomic.Bool
	_, client := serveSocket(t, os.Getuid()+1, types.CredentialSocketPeer, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached.Store(true) }))

	status, body := post(t, client)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Contains(t, string(body), string(types.SocketPeerNotOwner))
	assert.False(t, reached.Load())
}

func TestSocketPeerGuardHoldsTheCredentialToTheNeed(t *testing.T) {
	_, client := serveSocket(t, os.Getuid(), types.Credential{Class: types.ClassSocketPeer, Grant: types.GrantViewer}, credentialEcho)

	status, body := post(t, client)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Contains(t, string(body), string(types.GrantInsufficient))
}

func TestSocketPeerGuardRefusesAConnectionWithNoPeer(t *testing.T) {
	g, err := SocketPeerGuard(rpcerr.FormatJSON, os.Getuid(), types.CredentialSocketPeer, needMCP, credentialEcho)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	assert.Equal(t, http.StatusForbidden, rec.Code, "a request that did not arrive on a unix socket has no peer to admit")
	assert.Contains(t, rec.Body.String(), string(types.SocketPeerNotOwner))
}

func TestSocketPeerGuardRefusesAnInvalidNeed(t *testing.T) {
	_, err := SocketPeerGuard(rpcerr.FormatJSON, os.Getuid(), types.CredentialSocketPeer, types.Need{Surface: types.SurfaceMCP}, credentialEcho)
	assert.Error(t, err)
}

func TestNewUnixServerReclaimsAStaleSocket(t *testing.T) {
	path := socketPath(t)
	dead, err := net.Listen("unix", path)
	require.NoError(t, err)
	// A listener that dies without unlinking leaves the file behind, as a SIGKILL would.
	dead.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, dead.Close())
	require.FileExists(t, path)

	srv, err := NewUnixServer(path)
	require.NoError(t, err)
	assert.Equal(t, path, srv.Path())
	require.NoError(t, srv.Close())
	assert.NoFileExists(t, path, "Close removes the socket file")
}

func TestNewUnixServerRefusesALiveSocket(t *testing.T) {
	path := socketPath(t)
	live, err := net.Listen("unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = live.Close() })
	go func() {
		for {
			c, err := live.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	_, err = NewUnixServer(path)
	assert.ErrorIs(t, err, ErrSocketInUse)
}
