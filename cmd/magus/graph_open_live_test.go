//go:build mcp

package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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

// TestLiveBridgeReachable exercises the build-tag-dispatched gate that decides
// whether the zero-arg `magus graph open` default may switch to live mode.
// On the mcp build it delegates to probeLiveBridge against the configured MCP
// address; the non-mcp stub (graph_open_live_stub.go) always returns false.
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
