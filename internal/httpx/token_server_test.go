package httpx

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type TokenServerRebindSuite struct {
	suite.Suite
	ts *TokenServer
}

func TestTokenServerRebindSuite(t *testing.T) {
	suite.Run(t, new(TokenServerRebindSuite))
}

func (s *TokenServerRebindSuite) SetupTest() {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	ts, err := StartTokenServer("https://page.example", map[string]http.Handler{"/data": ok})
	s.Require().NoError(err)
	s.ts = ts
	s.T().Cleanup(ts.Close)
}

// call presents the page's own token, so only the rebind check can refuse.
func (s *TokenServerRebindSuite) call(host, origin string) int {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/data", s.ts.Addr()), nil)
	s.Require().NoError(err)
	req.Header.Set("Authorization", "Bearer "+s.ts.Token())
	if host != "" {
		req.Host = host
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	s.Require().NoError(err)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func (s *TokenServerRebindSuite) TestAdmitsThePagesOrigin() {
	s.Equal(http.StatusOK, s.call("", "https://page.example"))
}

func (s *TokenServerRebindSuite) TestAdmitsACallerWithNoOrigin() {
	s.Equal(http.StatusOK, s.call("", ""))
}

func (s *TokenServerRebindSuite) TestRefusesAForgedHost() {
	s.Equal(http.StatusForbidden, s.call("evil.example", ""))
}

func (s *TokenServerRebindSuite) TestRefusesTheTrustedPageNameAsHost() {
	s.Equal(http.StatusForbidden, s.call("page.example", ""))
}

func (s *TokenServerRebindSuite) TestRefusesAForeignOrigin() {
	s.Equal(http.StatusForbidden, s.call("", "https://evil.example"))
}

func (s *TokenServerRebindSuite) TestRefusesThePageOverAnotherScheme() {
	s.Equal(http.StatusForbidden, s.call("", "http://page.example"))
}

func TestStartTokenServerRefusesAnInexactOrigin(t *testing.T) {
	t.Parallel()
	_, err := StartTokenServer("https://page.example/logs/", nil)
	assert.Error(t, err)
}

// Every route of a per-run page server holds its one page's token and nothing else: no token
// and any other token, a server operator token included, are refused, and the page's own is
// admitted by header or by the query an EventSource sends.
func TestTokenServerRoutesAdmitOnlyThePageToken(t *testing.T) {
	t.Parallel()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	ts, err := StartTokenServer("http://127.0.0.1", map[string]http.Handler{"/data": ok, "/events/": ok})
	require.NoError(t, err)
	t.Cleanup(ts.Close)
	client := &http.Client{Timeout: 5 * time.Second}
	call := func(path, header string) int {
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s%s", ts.Addr(), path), nil)
		require.NoError(t, err)
		if header != "" {
			req.Header.Set("Authorization", "Bearer "+header)
		}
		resp, err := client.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	for _, path := range []string{"/data", "/events/x"} {
		assert.Equal(t, http.StatusUnauthorized, call(path, ""), path)
		assert.Equal(t, http.StatusUnauthorized, call(path, "mgo_"+ts.Token()), path)
		assert.Equal(t, http.StatusUnauthorized, call(path, ts.Token()+"x"), path)
		assert.Equal(t, http.StatusOK, call(path, ts.Token()), path)
		assert.Equal(t, http.StatusOK, call(fmt.Sprintf("%s?token=%s", path, ts.Token()), ""), path)
	}
}
