package doctor

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckMCPTokens(t *testing.T) {
	t.Run("absent cli token and no connectors", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		got := (&runner{}).checkMCPTokens()
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "cli token: absent")
		assert.Contains(t, got.Message, "0 connector token(s)")
		assert.Empty(t, got.Details)
	})

	t.Run("present cli token shows fingerprint", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		tok, err := auth.Generate()
		require.NoError(t, err)
		_, err = auth.SaveNew(tok)
		require.NoError(t, err)

		got := (&runner{}).checkMCPTokens()
		assert.Contains(t, got.Message, "cli token: present (fingerprint "+auth.Fingerprint(tok))
	})

	t.Run("expired and soon connectors are flagged; never is quiet", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		store, err := auth.LoadConnectorStore()
		require.NoError(t, err)
		_, _, err = store.Create("expired", time.Now().Add(-time.Hour), auth.ScopeMCP)
		require.NoError(t, err)
		_, _, err = store.Create("soon", time.Now().Add(48*time.Hour), auth.ScopeMCP)
		require.NoError(t, err)
		_, _, err = store.Create("forever", time.Time{}, auth.ScopeMCP)
		require.NoError(t, err)

		got := (&runner{}).checkMCPTokens()
		assert.Equal(t, types.DoctorOK, got.Status, "credential state is informational, never a failure")
		assert.Contains(t, got.Message, "3 connector token(s)")

		joined := strings.Join(got.Details, "\n")
		assert.Contains(t, joined, `connector "expired" expired`)
		assert.Contains(t, joined, "revoke it: magus config mcp connector revoke expired")
		assert.Contains(t, joined, `connector "soon" expires in`)
		assert.NotContains(t, joined, "forever", "a never-expiring token must not be flagged")
	})
}

// TestProbeBridgeReachability pins which lifecycle the skip keys on.
//
// It used to key on the PROC daemon being reachable, and magus spins one of those up for
// ordinary commands - so a plain `magus doctor` adopted one, the skip could never fire, and
// every machine without a console failed here. The bridge rides on the MCP HTTP server that
// only `magus server start` starts, so that is what "expected" has to mean.
func TestProbeBridgeReachability(t *testing.T) {
	t.Run("console disabled skips", func(t *testing.T) {
		got := probeBridgeReachability(t.Context(), &DaemonInfo{})
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "console.enabled: false")
	})

	t.Run("mcp disabled skips", func(t *testing.T) {
		got := probeBridgeReachability(t.Context(), &DaemonInfo{BridgeEnabled: true})
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "mcp.enabled is false")
	})

	// The regression: an adopted per-process daemon is Reachable and serves no bridge.
	t.Run("a reachable non-persistent daemon still skips", func(t *testing.T) {
		got := probeBridgeReachability(t.Context(), &DaemonInfo{
			BridgeEnabled: true, MCPEnabled: true, Reachable: true, MCPAddr: "127.0.0.1:1",
		})
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Equal(t, types.EvidenceUnknown, got.Evidence)
		assert.Contains(t, got.Message, "no persistent daemon")
	})

	// And the other half, which is what the check is FOR: a daemon that promised a bridge
	// and is not serving one is a failure, not a shrug.
	t.Run("a persistent daemon with no bridge fails", func(t *testing.T) {
		got := probeBridgeReachability(t.Context(), &DaemonInfo{
			BridgeEnabled: true, MCPEnabled: true, Reachable: true, Persistent: true,
			MCPAddr: unreachableAddr(t),
		})
		assert.Equal(t, types.DoctorFail, got.Status)
		assert.Contains(t, got.Message, "bridge endpoint not reachable")
	})

	// 401 proves the guarded route is mounted: auth ran before any handler.
	t.Run("a guarded route answering 401 passes", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()
		got := probeBridgeReachability(t.Context(), &DaemonInfo{
			BridgeEnabled: true, MCPEnabled: true, Reachable: true, Persistent: true,
			MCPAddr: strings.TrimPrefix(srv.URL, "http://"),
		})
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "reachable at")
	})
}

// unreachableAddr returns a host:port nothing is listening on: a listener opened only to
// have the kernel pick a free port, then closed.
func unreachableAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}
