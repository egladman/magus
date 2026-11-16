package httpx

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const hostedExplorer = "https://eli.gladman.cc/magus/console/graph/"

type ExplorerOriginSuite struct{ suite.Suite }

func TestExplorerOriginSuite(t *testing.T) {
	suite.Run(t, new(ExplorerOriginSuite))
}

func (s *ExplorerOriginSuite) TestRefusesTheDefaultExplorerWithAnEmptyList() {
	err := CheckHostedPageOrigin(hostedExplorer, nil)
	s.Require().Error(err)
	s.Contains(err.Error(), "mcp.allowed_origins")
}

func (s *ExplorerOriginSuite) TestPassesAnOriginTheListNames() {
	s.NoError(CheckHostedPageOrigin(hostedExplorer, []string{"https://eli.gladman.cc"}))
}

func (s *ExplorerOriginSuite) TestRefusesAnotherSchemeOfAListedOrigin() {
	s.Error(CheckHostedPageOrigin("http://site.example/graph/", []string{"https://site.example"}))
}

func (s *ExplorerOriginSuite) TestPassesLocalhost() {
	s.NoError(CheckHostedPageOrigin("http://localhost:9999/graph/", nil))
}

func (s *ExplorerOriginSuite) TestPassesALoopbackIP() {
	s.NoError(CheckHostedPageOrigin("http://127.0.0.1:9999/graph/", nil))
}

func (s *ExplorerOriginSuite) TestRefusesANameThatOnlyStartsLikeLoopback() {
	s.Error(CheckHostedPageOrigin("http://localhost.evil.example/graph/", nil))
}

func (s *ExplorerOriginSuite) TestRefusesARelativeBase() {
	s.Error(CheckHostedPageOrigin("graph/", nil))
}

// TestProbeLiveBridge covers the security-relevant branches of the real HTTP
// probe: a guarded route (401/403) proves the bridge is up; anything else,
// including connection refused, must be treated as down.
func TestProbeLiveBridge(t *testing.T) {
	status := func(code int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/graph", r.URL.Path)
			w.WriteHeader(code)
		}))
	}

	t.Run("401 is reachable", func(t *testing.T) {
		srv := status(http.StatusUnauthorized)
		defer srv.Close()
		require.NoError(t, ProbeLiveBridge(context.Background(), strings.TrimPrefix(srv.URL, "http://")))
	})

	t.Run("403 is reachable", func(t *testing.T) {
		srv := status(http.StatusForbidden)
		defer srv.Close()
		require.NoError(t, ProbeLiveBridge(context.Background(), strings.TrimPrefix(srv.URL, "http://")))
	})

	t.Run("200 is unexpected and treated as unreachable", func(t *testing.T) {
		srv := status(http.StatusOK)
		defer srv.Close()
		require.Error(t, ProbeLiveBridge(context.Background(), strings.TrimPrefix(srv.URL, "http://")))
	})

	t.Run("connection refused is unreachable", func(t *testing.T) {
		require.Error(t, ProbeLiveBridge(context.Background(), unusedAddr(t)))
	})
}

// unusedAddr binds and immediately closes to get a loopback port nothing listens on.
func unusedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// TestLiveBridgeReachable exercises the gate that decides whether the zero-arg
// `magus graph export --open` default may switch to live mode.
func TestLiveBridgeReachable(t *testing.T) {
	t.Run("reachable bridge", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()
		require.True(t, LiveBridgeReachable(context.Background(), strings.TrimPrefix(srv.URL, "http://")))
	})

	t.Run("unreachable bridge", func(t *testing.T) {
		require.False(t, LiveBridgeReachable(context.Background(), unusedAddr(t)))
	})
}
