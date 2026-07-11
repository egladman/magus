package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/render"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenViaBrowserEnv exercises the $BROWSER convention parsing: unset falls through
// (error), a bogus command errors, and a real command (with or without %s) launches.
func TestOpenViaBrowserEnv(t *testing.T) {
	t.Setenv("BROWSER", "")
	assert.Error(t, openViaBrowserEnv("http://x"), "unset BROWSER falls through to the platform opener")

	t.Setenv("BROWSER", "magus-no-such-browser-xyz")
	assert.Error(t, openViaBrowserEnv("http://x"), "a command that cannot start errors")

	// `true` exists on the PATH of every supported unix; it launches (Start succeeds),
	// which is all openViaBrowserEnv needs to consider the browser opened.
	t.Setenv("BROWSER", "true")
	assert.NoError(t, openViaBrowserEnv("http://x"))

	t.Setenv("BROWSER", "true %s")
	assert.NoError(t, openViaBrowserEnv("http://x"), "the URL is substituted for %s")

	// The first launchable entry wins even if an earlier one is missing.
	t.Setenv("BROWSER", "magus-no-such-browser-xyz:true")
	assert.NoError(t, openViaBrowserEnv("http://x"))
}

// TestEncodeFragmentDeterminism confirms that render.EncodeFragmentRaw produces
// byte-for-byte identical output for the same input across two calls. This relies
// on gzip.NewWriter leaving the header ModTime at its zero value by default, so the
// compressed stream is deterministic - a necessary property for stable #data= URL
// fragments in MAGUS.md. The test exercises the shared encoder that both the render
// package (per-project MAGUS.md deep links) and cmd/magus (graph open) use, proving
// browser wire-format parity is preserved when a single implementation is used.
func TestEncodeFragmentDeterminism(t *testing.T) {
	payload := []byte(`{"projects":[{"path":"pkg/foo","engine":"buzz","nodes":[{"name":"build","dependencies":["fmt"]},{"name":"fmt"}]}]}`)

	first, err := render.EncodeFragmentRaw(payload)
	require.NoError(t, err, "first EncodeFragmentRaw")

	second, err := render.EncodeFragmentRaw(payload)
	require.NoError(t, err, "second EncodeFragmentRaw")

	assert.Equal(t, first, second, "EncodeFragmentRaw must be deterministic:\n  first:  %s\n  second: %s", first, second)
}

// TestProbeLiveBridge covers the security-relevant branches of the real HTTP
// probe used by both graphOpenLive (blocker: never emit a token for an
// unreachable bridge) and liveBridgeReachable (the zero-arg auto-switch
// gate): a guarded route (401/403) proves the bridge is up; anything else,
// including connection refused, must be treated as down.
func TestProbeLiveBridge(t *testing.T) {
	t.Run("401 is reachable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/api/v1/graph", r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()
		require.NoError(t, probeLiveBridge(context.Background(), strings.TrimPrefix(srv.URL, "http://")))
	})

	t.Run("403 is reachable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()
		require.NoError(t, probeLiveBridge(context.Background(), strings.TrimPrefix(srv.URL, "http://")))
	})

	t.Run("200 is unexpected and treated as unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		err := probeLiveBridge(context.Background(), strings.TrimPrefix(srv.URL, "http://"))
		require.Error(t, err)
	})

	t.Run("connection refused is unreachable", func(t *testing.T) {
		// Bind and immediately close to get a loopback port nothing listens on.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := ln.Addr().String()
		require.NoError(t, ln.Close())

		err = probeLiveBridge(context.Background(), addr)
		require.Error(t, err)
	})
}

// TestLiveBridgeReachable exercises the gate that decides whether the zero-arg
// `magus graph open` default may switch to live mode. It delegates to
// probeLiveBridge against the configured MCP address.
func TestLiveBridgeReachable(t *testing.T) {
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })

	t.Run("reachable bridge", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()
		globalCfg.MCP.Address = strings.TrimPrefix(srv.URL, "http://")
		require.True(t, liveBridgeReachable(context.Background()))
	})

	t.Run("unreachable bridge", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := ln.Addr().String()
		require.NoError(t, ln.Close())
		globalCfg.MCP.Address = addr
		require.False(t, liveBridgeReachable(context.Background()))
	})
}
