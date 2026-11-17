package secrets

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EndpointSuite struct{ suite.Suite }

func TestEndpoint(t *testing.T) { suite.Run(t, new(EndpointSuite)) }

// upstream is a TLS test server standing in for the real API, plus a resolver wired
// to reach it. The grant's host is the server's own host:port, so the forwarder makes
// a genuine verified TLS connection to it: the production code path, not a relaxed
// one. Only the trust anchor is test-supplied.
func upstream(t *testing.T, h http.HandlerFunc) (*httptest.Server, *Resolver, Grant) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)

	res := NewResolver()
	res.endpointTransport = srv.Client().Transport

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return srv, res, Grant{
		Ref: "UPSTREAM_KEY", Host: u.Host, Header: "Authorization", Prefix: "Bearer ",
	}
}

// scope is the test's own endpoint scope, standing in for a run's id.
func scope(t *testing.T) string { return "inv-" + t.Name() }

// TestAttachesCredential is the Tier 2 happy path: the child speaks plain HTTP
// to loopback and the upstream receives an authenticated HTTPS request. The child
// never holds the credential.
func (s *EndpointSuite) TestAttachesCredential() {
	t := s.T()
	var gotAuth, gotPath string
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		_, _ = io.WriteString(w, "ok")
	})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(base, "http://127.0.0.1:"), "endpoint must be loopback plaintext, got %q", base)

	resp, err := http.Get(base + "/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", string(body))
	assert.Equal(t, "Bearer sk-upstream-value", gotAuth)
	assert.Equal(t, "/v1/models", gotPath, "the token prefix must be stripped before forwarding")
}

// TestRejectsMissingToken is the property that makes a loopback bind
// defensible. Without the token any local process could reach the port and spend the
// credential; the token rides in the URL only the child was given.
//
// Delete the prefix check in originHandler and this fails.
func (s *EndpointSuite) TestRejectsMissingToken() {
	t := s.T()
	var reached atomic.Bool
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) { reached.Store(true) })
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)

	// Same port, no token: what another process on the machine can reach unaided.
	u, err := url.Parse(base)
	require.NoError(t, err)
	resp, err := http.Get(fmt.Sprintf("http://%s/v1/models", u.Host))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.False(t, reached.Load(), "an untokened local request reached the upstream")
}

// TestWrongTokenRejected: guessing the port is not enough, and a wrong token is
// refused the same way a missing one is.
func (s *EndpointSuite) TestWrongTokenRejected() {
	t := s.T()
	var reached atomic.Bool
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) { reached.Store(true) })
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)
	u, err := url.Parse(base)
	require.NoError(t, err)

	resp, err := http.Get(fmt.Sprintf("http://%s/00000000000000000000000000000000/v1/models", u.Host))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.False(t, reached.Load())
}

// Opening an endpoint must resolve NOTHING, or merely declaring one would pop an unlock
// prompt. The provider is invoked on the first request through it.
//
// The credential is deliberately absent from the environment until after OpenEndpoint
// returns: if it resolved eagerly it would fail here rather than succeed.
func (s *EndpointSuite) TestIsLazy() {
	t := s.T()
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err, "OpenEndpoint resolved the credential instead of deferring it")
	assert.Zero(t, res.resolvedCount(), "a provider was invoked before any request was made")

	t.Setenv("UPSTREAM_KEY", "sk-late-value")
	resp, err := http.Get(base + "/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 1, res.resolvedCount(), "the first request through the endpoint did not resolve the credential")
}

// One socket per distinct grant within a scope: three callers naming the same grant
// must not bind three listeners.
func (s *EndpointSuite) TestIsKeptPerGrant() {
	t := s.T()
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})

	first, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)
	second, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)
	assert.Equal(t, first, second, "the same grant bound two endpoints")

	other := g
	other.Header = "X-Api-Key"
	third, err := res.OpenEndpoint(t.Context(), scope(t), other)
	require.NoError(t, err)
	assert.NotEqual(t, first, third, "two different grants shared one endpoint")
}

// Resolvers derived with WithProvider share one endpoint map, and a forwarder resolves
// through the Resolver that opened it, so one provider's endpoint must not answer for
// another's.
func (s *EndpointSuite) TestProvidersGetTheirOwnEndpoint() {
	t := s.T()
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})

	builtin, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)
	vault, err := res.WithProvider("vault").OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)
	assert.NotEqual(t, builtin, vault, "two providers shared one endpoint")
}

// The listener belongs to its context, not to the machine: nothing survives it to be
// stopped later.
func (s *EndpointSuite) TestClosesWithRun() {
	t := s.T()
	ctx, cancel := context.WithCancel(context.Background())
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(ctx, scope(t), g)
	require.NoError(t, err)
	resp, err := http.Get(base + "/ping")
	require.NoError(t, err)
	_ = resp.Body.Close()

	cancel()
	assert.Eventually(t, func() bool {
		c := &http.Client{Timeout: 200 * time.Millisecond}
		r, gerr := c.Get(base + "/ping")
		if gerr != nil {
			return true
		}
		_ = r.Body.Close()
		return false
	}, 3*time.Second, 25*time.Millisecond, "the forwarder outlived the run's context")
}

// The base URL carries the token that authorizes an authenticated request, so it must be
// masked exactly as a credential is, or anything recorded hands a local process the
// ability to spend the upstream key.
//
// Delete the Register call in startForwarder and this fails.
func (s *EndpointSuite) TestURLIsRedactable() {
	t := s.T()
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)

	line := "OPENAI_BASE_URL=" + base
	assert.NotContains(t, res.RedactString(line), base, "the endpoint token survived redaction")
	assert.Contains(t, res.RedactString(line), mask)
}

// TestRejectsInvalidGrant: the same validation grant() applies, because an
// origin that binds for a malformed grant is a socket nobody can use failing far from
// its cause.
func (s *EndpointSuite) TestRejectsInvalidGrant() {
	t := s.T()
	res := NewResolver()
	_, err := res.OpenEndpoint(t.Context(), scope(t), Grant{Host: "api.example.com", Header: "Authorization"})
	require.ErrorContains(t, err, "ref is required")
}

// TestUpstreamFailureIsNotSilent: a provider that cannot resolve produces a 502
// naming the problem, not a request forwarded without credentials. Sending an
// unauthenticated request would surface as a confusing 401 from the far end.
func (s *EndpointSuite) TestUpstreamFailureIsNotSilent() {
	t := s.T()
	var reached atomic.Bool
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) { reached.Store(true) })
	// UPSTREAM_KEY deliberately unset: the built-in provider errors.

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)
	resp, err := http.Get(base + "/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.False(t, reached.Load(), "an unresolvable credential still produced an upstream request")
}

// TestPreservesMethodAndBody: the forwarder is a proxy, not a GET-shaped
// shortcut. A tool POSTing through it must arrive intact.
func (s *EndpointSuite) TestPreservesMethodAndBody() {
	t := s.T()
	var method, body, auth string
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		method, auth = r.Method, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)
	resp, err := http.Post(base+"/v1/chat", "application/json", strings.NewReader(`{"q":1}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.MethodPost, method)
	assert.JSONEq(t, `{"q":1}`, body)
	assert.Equal(t, "Bearer sk-upstream-value", auth)
}

// TestRootPath: a tool that hits the base URL with no trailing path must not
// forward an empty path, which is not a legal request-URI.
func (s *EndpointSuite) TestRootPath() {
	t := s.T()
	var gotPath string
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) { gotPath = r.URL.Path })
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)
	resp, err := http.Get(base)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "/", gotPath)
}

// TestRebindsAfterContextEnds is the kept-endpoint liveness fix. The Resolver outlives
// a single scope, so a cached entry whose context has ended
// used to hand back a URL on a RELEASED port, which under a local-attacker model
// means another process can bind it and receive the child's whole request stream.
//
// Delete the f.ctx.Err() check in OpenEndpoint and this fails.
func (s *EndpointSuite) TestRebindsAfterContextEnds() {
	t := s.T()
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	first, cancel := context.WithCancel(context.Background())
	base1, err := res.OpenEndpoint(first, scope(t), g)
	require.NoError(t, err)
	cancel()

	// The forwarder is torn down asynchronously; wait for it before asking again.
	assert.Eventually(t, func() bool {
		c := &http.Client{Timeout: 200 * time.Millisecond}
		r, gerr := c.Get(base1 + "/ping")
		if gerr != nil {
			return true
		}
		_ = r.Body.Close()
		return false
	}, 3*time.Second, 25*time.Millisecond)

	base2, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)
	assert.NotEqual(t, base1, base2, "a dead endpoint was served from the kept endpoints")

	resp, err := http.Get(base2 + "/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the rebound endpoint does not work")
}

// TestTokenMustBeAWholeSegment: the check was strings.HasPrefix, so
// "/<tok>extra" passed and then trimmed to a path with no leading slash, producing a
// malformed request line upstream.
func (s *EndpointSuite) TestTokenMustBeAWholeSegment() {
	t := s.T()
	var reached atomic.Bool
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) { reached.Store(true) })
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)

	resp, err := http.Get(base + "extra/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.False(t, reached.Load(), "a token prefix match reached the upstream")
}

// TestPreservesEncodedPath: trimming the DECODED path turned an encoded
// separator into a real one, so "/v1/a%2Fb" reached the upstream as "/v1/a/b": the
// proxy silently addressing a different resource than the child asked for.
func (s *EndpointSuite) TestPreservesEncodedPath() {
	t := s.T()
	var gotRequestURI string
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotRequestURI = r.RequestURI
	})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)
	resp, err := http.Get(base + "/v1/a%2Fb")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "/v1/a%2Fb", gotRequestURI)
}

// TestIgnoresConnectionHeader: ReverseProxy removes every header NAMED in
// Connection, and it does so AFTER Rewrite, so a caller holding the token could send
// "Connection: X-Api-Key" and have the proxy strip the credential straight back off,
// producing the silently-unauthenticated request this design refuses to make.
func (s *EndpointSuite) TestIgnoresConnectionHeader() {
	t := s.T()
	var gotAuth string
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
	})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	require.NoError(t, err)
	req.Header.Set("Connection", "Authorization")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "Bearer sk-upstream-value", gotAuth,
		"a client-controlled Connection header stripped the credential")
}

// TestOverwritesCallerSetHeader is the case that decides whether Tier 2 works
// at all with the clients it exists for. An earlier version refused a request that
// already set the granted header, on symmetry with ApplyGrants, which made the endpoint
// unusable with openai-python, the Anthropic SDK, and essentially every API library,
// since they all set their auth header unconditionally from a key they insist on being
// given and offer no way to suppress it.
//
// The documented pattern is a placeholder key: the child's value is garbage by
// construction, so overwriting it is correct rather than presumptuous.
func (s *EndpointSuite) TestOverwritesCallerSetHeader() {
	t := s.T()
	var gotAuth string
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
	})
	t.Setenv("UPSTREAM_KEY", "sk-the-real-key")

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	require.NoError(t, err)
	// What every SDK sends: a placeholder the user set to stop it refusing to start.
	req.Header.Set("Authorization", "Bearer sk-placeholder-do-not-use")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "a client that sets its own auth header was refused")
	assert.Equal(t, "Bearer sk-the-real-key", gotAuth,
		"the child's placeholder reached the upstream instead of the granted credential")
}

// TestStreamsWithoutBuffering: an SSE completion is the marquee reason to point
// an agent at an endpoint. Wrapping the proxy in http.TimeoutHandler broke this (Go's
// timeoutWriter implements no Flusher, so ReverseProxy cannot flush and buffers the whole
// response), and no test caught it because every other test drives a complete response.
func (s *EndpointSuite) TestStreamsWithoutBuffering() {
	t := s.T()
	release := make(chan struct{})
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !assert.True(t, ok) {
			return
		}
		_, _ = io.WriteString(w, "data: first\n\n")
		fl.Flush()
		<-release // hold the response open, as a streaming completion does
		_, _ = io.WriteString(w, "data: last\n\n")
	})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)
	resp, err := http.Get(base + "/v1/chat")
	require.NoError(t, err)
	defer resp.Body.Close()
	defer close(release)

	// The first chunk must arrive BEFORE the upstream finishes. Buffering makes this
	// block until the test times out.
	buf := make([]byte, len("data: first\n\n"))
	done := make(chan error, 1)
	go func() {
		_, rerr := io.ReadFull(resp.Body, buf)
		done <- rerr
	}()
	select {
	case rerr := <-done:
		require.NoError(t, rerr)
		assert.Equal(t, "data: first\n\n", string(buf))
	case <-time.After(3 * time.Second):
		t.Fatal("the first chunk never arrived: the forwarder is buffering, so streaming is broken")
	}
}

// TestTokenAloneIsRedactable: registering only the composite base URL left the
// token exposed in the ordinary access-log shape, "GET /<tok>/v1/models", because
// redaction is literal substring matching.
func (s *EndpointSuite) TestTokenAloneIsRedactable() {
	t := s.T()
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)
	u, err := url.Parse(base)
	require.NoError(t, err)
	tok := strings.TrimPrefix(u.Path, "/")
	require.NotEmpty(t, tok)

	line := fmt.Sprintf("GET /%s/v1/models HTTP/1.1", tok)
	assert.NotContains(t, res.RedactString(line), tok, "the endpoint token survived redaction")
}

// TestRejectsNonLoopbackHost closes DNS rebinding categorically rather than
// relying on the token alone.
func (s *EndpointSuite) TestRejectsNonLoopbackHost() {
	t := s.T()
	var reached atomic.Bool
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) { reached.Store(true) })
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)
	u, err := url.Parse(base)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	require.NoError(t, err)
	req.Host = "evil.example.com:" + u.Port()
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.False(t, reached.Load())
}

// resolvedCount is how many references have been resolved through a provider: the
// observable for laziness, since a resolver can hold something maskable (an endpoint's
// URL) while having invoked no provider at all.
func (r *Resolver) resolvedCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.resolved)
}

// endpointCount reports how many forwarders the resolver holds.
func (r *Resolver) endpointCount() int {
	r.endpointMu.Lock()
	defer r.endpointMu.Unlock()
	return len(r.endpoints)
}

// An endpoint with no scope would have nothing to confine its token to, so a caller
// outside any run gets an error rather than a forwarder that lives as long as the process.
func (s *EndpointSuite) TestRefusesAnEmptyScope() {
	t := s.T()
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := res.OpenEndpoint(ctx, "", g)
	require.ErrorIs(t, err, ErrNoScope)
	var ee *EndpointError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, g.Ref, ee.Ref)
	assert.Zero(t, res.endpointCount())
}

func (s *EndpointSuite) TestLoopbackHostAcceptsEveryLoopbackSpelling() {
	for _, h := range []string{"127.0.0.1:8080", "127.0.0.1", "localhost:8080", "localhost", "[::1]:8080", "[::1]"} {
		s.True(loopbackHost(h), "host %q", h)
	}
	for _, h := range []string{"evil.example.com:8080", "evil.example.com", "[2001:db8::1]:8080", "10.0.0.1"} {
		s.False(loopbackHost(h), "host %q", h)
	}
}

func (s *EndpointSuite) TestNilResolverOpensNothing() {
	var r *Resolver
	_, err := r.OpenEndpoint(s.T().Context(), "inv", Grant{Ref: "R", Host: "h", Header: "A"})
	var nr *NoResolverError
	s.Require().ErrorAs(err, &nr)
	s.Equal("secrets: no resolver on this context", err.Error())
}

// TestDoesNotForwardTokenUpstream: Referer and Origin routinely carry the full
// loopback URL a child was given, token included, and the upstream is the one party
// this design assumes is hostile.
func (s *EndpointSuite) TestDoesNotForwardTokenUpstream() {
	t := s.T()
	var referer, origin string
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		referer, origin = r.Header.Get("Referer"), r.Header.Get("Origin")
	})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	require.NoError(t, err)
	req.Header.Set("Referer", base+"/previous")
	req.Header.Set("Origin", base)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Empty(t, referer, "the endpoint token was forwarded upstream in Referer")
	assert.Empty(t, origin, "the endpoint token was forwarded upstream in Origin")
}

// TestResolveFailureDoesNotEchoProviderError: a provider error can embed a
// spell subprocess's stderr, and on that path the value was never recorded, so nothing
// downstream would mask a credential it happened to quote. The detail goes to slog
// (which is redacted); the socket gets a fixed string.
func (s *EndpointSuite) TestResolveFailureDoesNotEchoProviderError() {
	t := s.T()
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})
	// UPSTREAM_KEY unset: the built-in provider errors, naming the variable.

	base, err := res.OpenEndpoint(t.Context(), scope(t), g)
	require.NoError(t, err)
	resp, err := http.Get(base + "/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.NotContains(t, string(body), "UPSTREAM_KEY", "the provider's error text reached the socket")
}

// TestReclaimsRedactionOnShutdown bounds the growth that would otherwise run
// for a server's whole life: a fresh token plus a base URL plus seven encoded forms of
// each, per rebind, per run, all scanned linearly by Redact on the output hot path.
//
// A resolved CREDENTIAL is never reclaimed: it stays valid wherever it was sent. An
// endpoint token is only meaningful while its listener is up.
func (s *EndpointSuite) TestReclaimsRedactionOnShutdown() {
	t := s.T()
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	ctx, cancel := context.WithCancel(context.Background())
	base, err := res.OpenEndpoint(ctx, scope(t), g)
	require.NoError(t, err)

	// Exercise it so the credential is resolved and registered too.
	resp, err := http.Get(base + "/v1/models")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.NotContains(t, res.RedactString(base), base, "the endpoint URL was never registered")

	cancel()
	assert.Eventually(t, func() bool {
		return res.RedactString(base) == base
	}, 5*time.Second, 25*time.Millisecond, "the endpoint's redaction strings were never reclaimed")

	assert.Equal(t, "***", res.RedactString("sk-upstream-value"),
		"a resolved credential was reclaimed along with the endpoint; it stays valid and must stay masked")
	assert.Zero(t, res.endpointCount(), "the shut-down forwarder stayed in the endpoint map")
}

// Two concurrent scopes
// wanting the same grant get two sockets and two TOKENS, which is the point: a token
// leaked from one run (into a log, a child's argv, a crash dump) authorizes nothing in
// any other run. One run finishing also cannot close a socket another is still using,
// which is the lifetime bug sharing required a refcount to avoid.
func (s *EndpointSuite) TestScopesGetTheirOwnEndpoint() {
	t := s.T()
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	runA, cancelA := context.WithCancel(context.Background())
	runB, cancelB := context.WithCancel(context.Background())
	defer cancelB()

	baseA, err := res.OpenEndpoint(runA, "inv-a", g)
	require.NoError(t, err)
	baseB, err := res.OpenEndpoint(runB, "inv-b", g)
	require.NoError(t, err)
	assert.NotEqual(t, baseA, baseB, "two runs shared one endpoint, so they shared a token")

	// Within ONE run the endpoint is still kept: three targets naming it bind one
	// socket, not three.
	again, err := res.OpenEndpoint(runA, "inv-a", g)
	require.NoError(t, err)
	assert.Equal(t, baseA, again, "one run bound the same grant twice")

	// Run A finishing must not disturb run B.
	cancelA()
	assert.Never(t, func() bool {
		c := &http.Client{Timeout: 500 * time.Millisecond}
		resp, gerr := c.Get(baseB + "/v1/models")
		if gerr != nil {
			return true
		}
		_ = resp.Body.Close()
		return false
	}, 1500*time.Millisecond, 100*time.Millisecond,
		"one run's endpoint closed when a DIFFERENT run finished")

	// And A's own endpoint is gone.
	assert.Eventually(t, func() bool {
		c := &http.Client{Timeout: 200 * time.Millisecond}
		resp, gerr := c.Get(baseA + "/v1/models")
		if gerr != nil {
			return true
		}
		_ = resp.Body.Close()
		return false
	}, 5*time.Second, 25*time.Millisecond, "an endpoint outlived its run")
}

// The no-CA argument rests on the forwarder making an ordinary verified TLS connection
// upstream itself. Honoring HTTPS_PROXY would delegate
// exactly the interception this design refuses to perform: the destination is disclosed
// to the proxy, and if the proxy's CA is trusted the guarantee is gone.
func (s *EndpointSuite) TestIgnoresProxyEnvironment() {
	t := s.T()
	tr, err := endpointTransport(http.DefaultTransport)
	require.NoError(t, err)
	assert.Nil(t, tr.Proxy, "the forwarder would route a credential through HTTPS_PROXY")
	assert.Equal(t, endpointResponseHeaderTimeout, tr.ResponseHeaderTimeout)
}

// A default transport replaced by one that is not an *http.Transport is refused, not
// used as it is with its proxy and no first-byte timeout.
func (s *EndpointSuite) TestRefusesADefaultTransportItCannotConfigure() {
	_, err := endpointTransport(roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, nil }))
	var refused *TransportError
	s.Require().ErrorAs(err, &refused)
	s.Equal("secrets.roundTripperFunc", refused.Type)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
