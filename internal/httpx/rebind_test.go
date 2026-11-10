package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/rpcerr"
)

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestDnsRebindGuard(t *testing.T) {
	t.Parallel()

	loopback := AllowedHosts(netip.MustParseAddrPort("127.0.0.1:7391"))

	// serve drives one request with the given Host and Origin (empty = none)
	// through the guard and returns the status code.
	serve := func(host, origin string) int {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Host = host
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rr := httptest.NewRecorder()
		GuardRebind(rpcerr.FormatJSON, loopback, okHandler).ServeHTTP(rr, req)
		return rr.Code
	}

	t.Run("host variations", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, http.StatusOK, serve("127.0.0.1:7391", ""), "loopback IP with port")
		assert.Equal(t, http.StatusOK, serve("127.0.0.1", ""), "loopback IP bare")
		assert.Equal(t, http.StatusOK, serve("localhost", ""), "localhost")
		assert.Equal(t, http.StatusOK, serve("localhost:7391", ""), "localhost with port")
		assert.Equal(t, http.StatusOK, serve("[::1]:7391", ""), "IPv6 loopback")
		assert.Equal(t, http.StatusOK, serve("::ffff:127.0.0.1", ""), "IPv4-mapped loopback")
		assert.Equal(t, http.StatusOK, serve("127.0.0.2", ""), "127.x loopback (whole /8 is loopback)")
		assert.Equal(t, http.StatusForbidden, serve("attacker.com", ""), "external host")
		assert.Equal(t, http.StatusForbidden, serve("evil.com:80", ""), "external host with port")
	})

	t.Run("origin variations", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, http.StatusOK, serve("127.0.0.1:7391", ""), "no origin")
		assert.Equal(t, http.StatusOK, serve("127.0.0.1:7391", "http://127.0.0.1:7391"), "loopback origin")
		assert.Equal(t, http.StatusForbidden, serve("127.0.0.1:7391", "http://evil.com"), "external origin")
		assert.Equal(t, http.StatusForbidden, serve("127.0.0.1:7391", "null"), "null origin")
		assert.Equal(t, http.StatusForbidden, serve("127.0.0.1:7391", "://bad"), "malformed origin")
	})
}

func TestAllowedHosts(t *testing.T) {
	t.Parallel()

	// allowed reports whether host passes the guard built for bind addr.
	allowed := func(addr, host string) bool {
		return isAllowedHost(host, AllowedHosts(netip.MustParseAddrPort(addr)))
	}

	t.Run("default loopback addr", func(t *testing.T) {
		t.Parallel()
		assert.True(t, allowed("127.0.0.1:7391", "127.0.0.1"))
		assert.True(t, allowed("127.0.0.1:7391", "localhost"))
		assert.True(t, allowed("127.0.0.1:7391", "::1"))
		assert.True(t, allowed("127.0.0.1:7391", "::ffff:127.0.0.1")) // IPv4-mapped → Unmap → IsLoopback
		assert.True(t, allowed("127.0.0.1:7391", "127.0.0.2"))        // whole 127.0.0.0/8 is loopback
		assert.False(t, allowed("127.0.0.1:7391", "attacker.com"))
	})

	t.Run("wildcard bind allows only loopback, not the wildcard IP", func(t *testing.T) {
		t.Parallel()
		assert.True(t, allowed("0.0.0.0:7391", "127.0.0.1"))
		assert.False(t, allowed("0.0.0.0:7391", "0.0.0.0"))
	})

	t.Run("concrete non-loopback bind also allows that host", func(t *testing.T) {
		t.Parallel()
		assert.True(t, allowed("192.168.1.5:7391", "192.168.1.5"))
		assert.True(t, allowed("192.168.1.5:7391", "127.0.0.1"))
		assert.False(t, allowed("192.168.1.5:7391", "attacker.com"))
	})
}

type AllowOriginSuite struct {
	suite.Suite
	base AllowedSet
}

func TestAllowOriginSuite(t *testing.T) {
	suite.Run(t, new(AllowOriginSuite))
}

func (s *AllowOriginSuite) SetupTest() {
	s.base = AllowedHosts(netip.MustParseAddrPort("127.0.0.1:7391"))
}

func (s *AllowOriginSuite) serve(allowed AllowedSet, host, origin string) int {
	req := httptest.NewRequest(http.MethodGet, "/api/", nil)
	req.Host = host
	req.Header.Set("Origin", origin)
	rr := httptest.NewRecorder()
	GuardRebind(rpcerr.FormatJSON, allowed, okHandler).ServeHTTP(rr, req)
	return rr.Code
}

func (s *AllowOriginSuite) TestNoOriginIsTrustedByDefault() {
	s.Equal(http.StatusForbidden, s.serve(s.base, "127.0.0.1:7391", "https://eli.gladman.cc"))
}

func (s *AllowOriginSuite) TestAdmitsTheExactOrigin() {
	allowed, err := s.base.AllowOrigin("https://site.example")
	s.Require().NoError(err)
	s.Equal(http.StatusOK, s.serve(allowed, "127.0.0.1:7391", "https://site.example"))
}

func (s *AllowOriginSuite) TestRefusesAnotherSchemeOfTheOrigin() {
	allowed, err := s.base.AllowOrigin("https://site.example")
	s.Require().NoError(err)
	s.Equal(http.StatusForbidden, s.serve(allowed, "127.0.0.1:7391", "http://site.example"))
}

func (s *AllowOriginSuite) TestRefusesAnotherPortOfTheOrigin() {
	allowed, err := s.base.AllowOrigin("https://site.example")
	s.Require().NoError(err)
	s.Equal(http.StatusForbidden, s.serve(allowed, "127.0.0.1:7391", "https://site.example:8443"))
}

func (s *AllowOriginSuite) TestNeverTrustsTheOriginsNameAsAHost() {
	allowed, err := s.base.AllowOrigin("https://site.example")
	s.Require().NoError(err)
	s.Equal(http.StatusForbidden, s.serve(allowed, "site.example", "https://site.example"))
}

func (s *AllowOriginSuite) TestLeavesTheReceiverUnchanged() {
	_, err := s.base.AllowOrigin("https://site.example")
	s.Require().NoError(err)
	s.Equal(http.StatusForbidden, s.serve(s.base, "127.0.0.1:7391", "https://site.example"))
}

func (s *AllowOriginSuite) TestRefusesAPath() {
	_, err := s.base.AllowOrigin("https://site.example/magus/")
	s.Error(err)
}

func (s *AllowOriginSuite) TestRefusesAWildcard() {
	_, err := s.base.AllowOrigin("*")
	s.Error(err)
}

func (s *AllowOriginSuite) TestRefusesABareHost() {
	_, err := s.base.AllowOrigin("site.example")
	s.Error(err)
}

func (s *AllowOriginSuite) TestRefusesUppercase() {
	_, err := s.base.AllowOrigin("https://Site.Example")
	s.Error(err)
}

func TestRebindRefusalOmitsTheBearerChallenge(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Host = "evil.example"
	rr := httptest.NewRecorder()
	GuardRebind(rpcerr.FormatJSON, AllowedHosts(netip.MustParseAddrPort("127.0.0.1:7391")), okHandler).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
	assert.Empty(t, rr.Header().Get("WWW-Authenticate"))
	got := decodeStatus(t, rr.Body.Bytes())
	assert.Equal(t, "PERMISSION_DENIED", got.Error.Status)
	assert.Equal(t, "MGS9007", got.reason())
}

// The refusal names the setting that widens the caller's set, and only that.
func TestRebindRefusalNamesTheCallersSetting(t *testing.T) {
	t.Parallel()
	refuse := func(allowed AllowedSet) string {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "evil.example"
		rr := httptest.NewRecorder()
		GuardRebind(rpcerr.FormatJSON, allowed, okHandler).ServeHTTP(rr, req)
		require.Equal(t, http.StatusForbidden, rr.Code)
		return decodeStatus(t, rr.Body.Bytes()).Error.Message
	}
	server := AllowedHosts(netip.MustParseAddrPort("127.0.0.1:7391"))
	widened, err := server.AllowOrigin("https://console.example")
	require.NoError(t, err)

	assert.Contains(t, refuse(server), "only loopback hosts and the origins in mcp.allowed_origins;", "the server's MCP setting")
	assert.Contains(t, refuse(widened), "the origins in mcp.allowed_origins;", "AllowOrigin keeps the setting")
	assert.Contains(t, refuse(server.WidenedBy("viewer.origin")), "only loopback hosts and the origins in viewer.origin;")
	none := refuse(server.WidenedBy(""))
	assert.Contains(t, none, "only loopback hosts;")
	assert.NotContains(t, none, "origins in")
}
