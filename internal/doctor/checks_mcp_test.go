package doctor

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckTokens(t *testing.T) {
	isolate := func(t *testing.T) string {
		state := t.TempDir()
		t.Setenv("XDG_STATE_HOME", state)
		require.NoError(t, os.MkdirAll(filepath.Join(state, "magus"), 0o700))
		return state
	}

	t.Run("absent operator token and no stored tokens", func(t *testing.T) {
		isolate(t)
		got := (&runner{}).checkTokens()
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "operator token: absent")
		assert.Contains(t, got.Message, "0 stored token(s)")
		assert.Empty(t, got.Details)
	})

	t.Run("present operator token shows its id", func(t *testing.T) {
		isolate(t)
		tok, err := auth.GenerateOperator()
		require.NoError(t, err)
		_, err = auth.SaveNewOperator(tok)
		require.NoError(t, err)
		got := (&runner{}).checkTokens()
		assert.Contains(t, got.Message, "operator token: present (id "+auth.TokenID(tok))
	})

	t.Run("a token expiring within 14 days is advice", func(t *testing.T) {
		isolate(t)
		dir, err := auth.StoreDir()
		require.NoError(t, err)
		store, err := auth.LoadStore(dir)
		require.NoError(t, err)
		for name, g := range map[string]types.Grant{"soon": types.GrantConnector, "later": types.GrantConsole} {
			ttl := 48 * time.Hour
			if name == "later" {
				ttl = 60 * 24 * time.Hour
			}
			_, _, err = store.Mint(types.GrantOperator, auth.MintRequest{Name: name, Grant: g, TTL: ttl})
			require.NoError(t, err)
		}
		got := (&runner{}).checkTokens()
		assert.Equal(t, types.DoctorAdvice, got.Status)
		assert.Contains(t, got.Message, "2 stored token(s)")
		joined := strings.Join(got.Details, "\n")
		assert.Contains(t, joined, `token "soon" (mcp=write) expires in`)
		assert.NotContains(t, joined, "later")
	})

	t.Run("an old operator file and a retired store fail with their codes", func(t *testing.T) {
		state := isolate(t)
		require.NoError(t, os.WriteFile(filepath.Join(state, "magus", "mcp_token"), []byte("b2xkLWZvcm1hdA\n"), 0o600))
		old := filepath.Join(state, "magus", "connectors.d", "obsidian.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(old), 0o700))
		require.NoError(t, os.WriteFile(old, []byte(`{"version":1}`), 0o600))
		got := (&runner{}).checkTokens()
		assert.Equal(t, types.DoctorFail, got.Status)
		joined := strings.Join(got.Details, "\n")
		assert.Contains(t, joined, "MGS9016")
		assert.Contains(t, joined, "MGS9017")
		assert.Contains(t, joined, old)
	})

	// A record no mint could have written is skipped by the store, and doctor fails on it,
	// naming the file, while the good tokens beside it still count.
	t.Run("a planted token record fails naming its file", func(t *testing.T) {
		isolate(t)
		dir, err := auth.StoreDir()
		require.NoError(t, err)
		store, err := auth.LoadStore(dir)
		require.NoError(t, err)
		_, _, err = store.Mint(types.GrantOperator, auth.MintRequest{Name: "good", Grant: types.GrantConsole, TTL: time.Hour})
		require.NoError(t, err)
		planted := filepath.Join(dir, "planted.json")
		body := `{"version":2,"id":"aaaaaaaa","name":"planted","class":"stored","sha256":"` + strings.Repeat("a", 64) +
			`","grant":{"tokens":"write","mcp":"write","console":"write"},"created":"2026-01-01T00:00:00Z","expires":"9999-01-01T00:00:00Z"}`
		require.NoError(t, os.WriteFile(planted, []byte(body), 0o600))
		got := (&runner{}).checkTokens()
		assert.Equal(t, types.DoctorFail, got.Status)
		joined := strings.Join(got.Details, "\n")
		assert.Contains(t, joined, "MGS9019")
		assert.Contains(t, joined, planted)
		assert.Contains(t, got.Message, "1 stored token(s)")
	})

	t.Run("a state dir other accounts can read is advice", func(t *testing.T) {
		state := isolate(t)
		require.NoError(t, os.Chmod(filepath.Join(state, "magus"), 0o755))
		got := (&runner{}).checkTokens()
		assert.Equal(t, types.DoctorAdvice, got.Status)
		assert.Contains(t, strings.Join(got.Details, "\n"), "chmod 700")
	})
}

// TestProbeBridgeReachability pins which lifecycle the skip keys on.
//
// It used to key on the PROC daemon being reachable, and magus spins one of those up for
// ordinary commands, so a plain `magus doctor` adopted one, the skip could never fire, and
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
