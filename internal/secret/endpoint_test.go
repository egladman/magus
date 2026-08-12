package secret

import (
	"context"
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

	"github.com/egladman/magus/types"
)

// upstream is a TLS test server standing in for the real API, plus a resolver wired
// to reach it. The grant's host is the server's own host:port, so the forwarder makes
// a genuine verified TLS connection to it - the production code path, not a relaxed
// one. Only the trust anchor is test-supplied.
func upstream(t *testing.T, h http.HandlerFunc) (*httptest.Server, *Resolver, types.SecretGrant) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)

	res := New()
	res.setEndpointTransport(srv.Client().Transport)

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return srv, res, types.SecretGrant{
		Ref: "UPSTREAM_KEY", Host: u.Host, Header: "Authorization", Prefix: "Bearer ",
	}
}

// runCtx is a context shaped like a real run: cancellable, and carrying an invocation
// id. An endpoint requires one, because that id is what scopes the token to a run and
// what proves the listener has something to shut down with.
func runCtx(t *testing.T) context.Context {
	t.Helper()
	return ContextWithInvocationID(t.Context(), "inv-"+t.Name())
}

// TestEndpointAttachesCredential is the Tier 2 happy path: the child speaks plain HTTP
// to loopback and the upstream receives an authenticated HTTPS request. The child
// never holds the credential.
func TestEndpointAttachesCredential(t *testing.T) {
	var gotAuth, gotPath string
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		_, _ = io.WriteString(w, "ok")
	})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(runCtx(t), g)
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

// TestEndpointRejectsMissingToken is the property that makes a loopback bind
// defensible. Without the token any local process could reach the port and spend the
// credential; the token rides in the URL only the child was given.
//
// Delete the prefix check in originHandler and this fails.
func TestEndpointRejectsMissingToken(t *testing.T) {
	var reached atomic.Bool
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) { reached.Store(true) })
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(runCtx(t), g)
	require.NoError(t, err)

	// Same port, no token: what another process on the machine can reach unaided.
	u, err := url.Parse(base)
	require.NoError(t, err)
	resp, err := http.Get("http://" + u.Host + "/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.False(t, reached.Load(), "an untokened local request reached the upstream")
}

// TestEndpointWrongTokenRejected: guessing the port is not enough, and a wrong token is
// refused the same way a missing one is.
func TestEndpointWrongTokenRejected(t *testing.T) {
	var reached atomic.Bool
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) { reached.Store(true) })
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(runCtx(t), g)
	require.NoError(t, err)
	u, err := url.Parse(base)
	require.NoError(t, err)

	resp, err := http.Get("http://" + u.Host + "/00000000000000000000000000000000/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.False(t, reached.Load())
}

// TestEndpointIsLazy pins the interaction policy this feature inherits: declaring an
// origin must resolve NOTHING, or a magusfile top-level would pop an unlock prompt on
// `magus ls`. The provider is invoked on the first request through it.
//
// The credential is deliberately absent from the environment until after Origin
// returns: if Origin resolved eagerly it would fail here rather than succeed.
func TestEndpointIsLazy(t *testing.T) {
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})

	base, err := res.OpenEndpoint(runCtx(t), g)
	require.NoError(t, err, "Origin resolved the credential instead of deferring it")
	assert.Zero(t, res.resolvedCount(), "a provider was invoked before any request was made")

	t.Setenv("UPSTREAM_KEY", "sk-late-value")
	resp, err := http.Get(base + "/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 1, res.resolvedCount(), "the first request through the endpoint did not resolve the credential")
}

// TestEndpointMemoizesPerGrant: one socket per distinct grant. A magusfile naming the
// same origin in three targets must not bind three listeners.
func TestEndpointMemoizesPerGrant(t *testing.T) {
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})

	first, err := res.OpenEndpoint(runCtx(t), g)
	require.NoError(t, err)
	second, err := res.OpenEndpoint(runCtx(t), g)
	require.NoError(t, err)
	assert.Equal(t, first, second, "the same grant bound two endpoints")

	other := g
	other.Header = "X-Api-Key"
	third, err := res.OpenEndpoint(runCtx(t), other)
	require.NoError(t, err)
	assert.NotEqual(t, first, third, "two different grants shared one endpoint")
}

// TestEndpointClosesWithRun: the listener belongs to the run, not to the machine. This
// is what makes the design "no separate service" rather than "a service magus starts
// for you" - nothing survives the run to be stopped later.
func TestEndpointClosesWithRun(t *testing.T) {
	ctx, cancel := context.WithCancel(ContextWithInvocationID(context.Background(), "inv-"+t.Name()))
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(ctx, g)
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

// TestEndpointURLIsRedactable: the base URL carries the token that authorizes an
// authenticated request, so it must be masked out of anything magus persists exactly
// as a credential is. Otherwise a cached log or a knowledge shard hands a local
// process the ability to spend the upstream key.
//
// Delete the registerRedactable call in startForwarder and this fails.
func TestEndpointURLIsRedactable(t *testing.T) {
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})

	base, err := res.OpenEndpoint(runCtx(t), g)
	require.NoError(t, err)

	line := "OPENAI_BASE_URL=" + base
	assert.NotContains(t, res.RedactString(line), base, "the endpoint token survived redaction")
	assert.Contains(t, res.RedactString(line), mask)
}

// TestEndpointRejectsInvalidGrant: the same validation grant() applies, because an
// origin that binds for a malformed grant is a socket nobody can use failing far from
// its cause.
func TestEndpointRejectsInvalidGrant(t *testing.T) {
	res := New()
	_, err := res.OpenEndpoint(runCtx(t), types.SecretGrant{Host: "api.example.com", Header: "Authorization"})
	require.ErrorContains(t, err, "ref is required")
}

// TestEndpointUpstreamFailureIsNotSilent: a provider that cannot resolve produces a 502
// naming the problem, not a request forwarded without credentials. Sending an
// unauthenticated request would surface as a confusing 401 from the far end.
func TestEndpointUpstreamFailureIsNotSilent(t *testing.T) {
	var reached atomic.Bool
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) { reached.Store(true) })
	// UPSTREAM_KEY deliberately unset: the built-in provider errors.

	base, err := res.OpenEndpoint(runCtx(t), g)
	require.NoError(t, err)
	resp, err := http.Get(base + "/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.False(t, reached.Load(), "an unresolvable credential still produced an upstream request")
}

// TestEndpointPreservesMethodAndBody: the forwarder is a proxy, not a GET-shaped
// shortcut. A tool POSTing through it must arrive intact.
func TestEndpointPreservesMethodAndBody(t *testing.T) {
	var method, body, auth string
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		method, auth = r.Method, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(runCtx(t), g)
	require.NoError(t, err)
	resp, err := http.Post(base+"/v1/chat", "application/json", strings.NewReader(`{"q":1}`))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.MethodPost, method)
	assert.JSONEq(t, `{"q":1}`, body)
	assert.Equal(t, "Bearer sk-upstream-value", auth)
}

// TestEndpointRootPath: a tool that hits the base URL with no trailing path must not
// forward an empty path, which is not a legal request-URI.
func TestEndpointRootPath(t *testing.T) {
	var gotPath string
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) { gotPath = r.URL.Path })
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(runCtx(t), g)
	require.NoError(t, err)
	resp, err := http.Get(base)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "/", gotPath)
}

// TestEndpointRebindsAfterContextEnds is the memo-liveness fix. The Resolver outlives
// a single run (it is per workspace Open), so a cached entry whose context has ended
// used to hand back a URL on a RELEASED port - which under a local-attacker model
// means another process can bind it and receive the child's whole request stream.
//
// Delete the f.ctx.Err() check in OpenEndpoint and this fails.
func TestEndpointRebindsAfterContextEnds(t *testing.T) {
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	first, cancel := context.WithCancel(ContextWithInvocationID(context.Background(), "inv-"+t.Name()))
	base1, err := res.OpenEndpoint(first, g)
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

	base2, err := res.OpenEndpoint(ContextWithInvocationID(t.Context(), "inv-"+t.Name()), g)
	require.NoError(t, err)
	assert.NotEqual(t, base1, base2, "a dead endpoint was served from the memo")

	resp, err := http.Get(base2 + "/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the rebound endpoint does not work")
}

// TestEndpointTokenMustBeAWholeSegment: the check was strings.HasPrefix, so
// "/<tok>extra" passed and then trimmed to a path with no leading slash, producing a
// malformed request line upstream.
func TestEndpointTokenMustBeAWholeSegment(t *testing.T) {
	var reached atomic.Bool
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) { reached.Store(true) })
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(runCtx(t), g)
	require.NoError(t, err)

	resp, err := http.Get(base + "extra/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.False(t, reached.Load(), "a token prefix match reached the upstream")
}

// TestEndpointPreservesEncodedPath: trimming the DECODED path turned an encoded
// separator into a real one, so "/v1/a%2Fb" reached the upstream as "/v1/a/b" - the
// proxy silently addressing a different resource than the child asked for.
func TestEndpointPreservesEncodedPath(t *testing.T) {
	var gotRequestURI string
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotRequestURI = r.RequestURI
	})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(runCtx(t), g)
	require.NoError(t, err)
	resp, err := http.Get(base + "/v1/a%2Fb")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "/v1/a%2Fb", gotRequestURI)
}

// TestEndpointIgnoresConnectionHeader: ReverseProxy removes every header NAMED in
// Connection, and it does so AFTER Rewrite - so a caller holding the token could send
// "Connection: X-Api-Key" and have the proxy strip the credential straight back off,
// producing the silently-unauthenticated request this design refuses to make.
func TestEndpointIgnoresConnectionHeader(t *testing.T) {
	var gotAuth string
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
	})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(runCtx(t), g)
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

// TestEndpointOverwritesCallerSetHeader is the case that decides whether Tier 2 works
// at all with the clients it exists for. An earlier version refused a request that
// already set the granted header, on symmetry with ApplyGrants - which made the endpoint
// unusable with openai-python, the Anthropic SDK, and essentially every API library,
// since they all set their auth header unconditionally from a key they insist on being
// given and offer no way to suppress it.
//
// The documented pattern is a placeholder key: the child's value is garbage by
// construction, so overwriting it is correct rather than presumptuous.
func TestEndpointOverwritesCallerSetHeader(t *testing.T) {
	var gotAuth string
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
	})
	t.Setenv("UPSTREAM_KEY", "sk-the-real-key")

	base, err := res.OpenEndpoint(runCtx(t), g)
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

// TestEndpointStreamsWithoutBuffering: an SSE completion is the marquee reason to point
// an agent at an endpoint. Wrapping the proxy in http.TimeoutHandler broke this - Go's
// timeoutWriter implements no Flusher, so ReverseProxy cannot flush and buffers the whole
// response - and no test caught it because every other test drives a complete response.
func TestEndpointStreamsWithoutBuffering(t *testing.T) {
	release := make(chan struct{})
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		require.True(t, ok)
		_, _ = io.WriteString(w, "data: first\n\n")
		fl.Flush()
		<-release // hold the response open, as a streaming completion does
		_, _ = io.WriteString(w, "data: last\n\n")
	})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(runCtx(t), g)
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

// TestEndpointTokenAloneIsRedactable: registering only the composite base URL left the
// token exposed in the ordinary access-log shape, "GET /<tok>/v1/models", because
// redaction is literal substring matching.
func TestEndpointTokenAloneIsRedactable(t *testing.T) {
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})

	base, err := res.OpenEndpoint(runCtx(t), g)
	require.NoError(t, err)
	u, err := url.Parse(base)
	require.NoError(t, err)
	tok := strings.TrimPrefix(u.Path, "/")
	require.NotEmpty(t, tok)

	line := "GET /" + tok + "/v1/models HTTP/1.1"
	assert.NotContains(t, res.RedactString(line), tok, "the endpoint token survived redaction")
}

// TestEndpointRejectsNonLoopbackHost closes DNS rebinding categorically rather than
// relying on the token alone.
func TestEndpointRejectsNonLoopbackHost(t *testing.T) {
	var reached atomic.Bool
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) { reached.Store(true) })
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(runCtx(t), g)
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

// setEndpointTransport points a resolver's forwarders at rt instead of
// http.DefaultTransport.
//
// A test hook rather than an Option, deliberately. The upstream hop is always https
// in production and must stay that way - an endpoint that could be aimed at a plaintext
// upstream would undo the only guarantee this design makes about the leg magus
// controls - so there is no legitimate caller for this outside a test, and an
// exported Option would invite one.
func (r *Resolver) setEndpointTransport(rt http.RoundTripper) {
	r.endpointTransport = rt
}

// TestEndpointRefusedOutsideARun is the lifetime gate, and it replaces a test that
// checked the wrong thing. The first version refused a context with no Done channel,
// which no production path produces: cmd/magus/main.go builds the root context from
// watchInterrupts(context.Background()), so PRELOAD - where a magusfile's top level runs,
// and which `magus ls`, `describe` and `graph` all reach - sailed straight through and
// would have bound a credential forwarder with a stable token for the whole process life.
//
// The run id is the honest gate: an endpoint belongs to a run, and preload has none.
func TestEndpointRefusedOutsideARun(t *testing.T) {
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})

	// Cancellable, like preload's context really is - and still refused.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := res.OpenEndpoint(ctx, g)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "belongs to a run")
}

// TestEndpointDoesNotForwardTokenUpstream: Referer and Origin routinely carry the full
// loopback URL a child was given, token included, and the upstream is the one party
// this design assumes is hostile.
func TestEndpointDoesNotForwardTokenUpstream(t *testing.T) {
	var referer, origin string
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {
		referer, origin = r.Header.Get("Referer"), r.Header.Get("Origin")
	})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	base, err := res.OpenEndpoint(runCtx(t), g)
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

// TestEndpointResolveFailureDoesNotEchoProviderError: a provider error can embed a
// spell subprocess's stderr, and on that path the value was never recorded, so nothing
// downstream would mask a credential it happened to quote. The detail goes to slog
// (which is redacted); the socket gets a fixed string.
func TestEndpointResolveFailureDoesNotEchoProviderError(t *testing.T) {
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})
	// UPSTREAM_KEY unset: the built-in provider errors, naming the variable.

	base, err := res.OpenEndpoint(runCtx(t), g)
	require.NoError(t, err)
	resp, err := http.Get(base + "/v1/models")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.NotContains(t, string(body), "UPSTREAM_KEY", "the provider's error text reached the socket")
}

// TestEndpointReclaimsRedactionOnShutdown bounds the growth that would otherwise run
// for a daemon's whole life: a fresh token plus a base URL plus seven encoded forms of
// each, per rebind, per run, all scanned linearly by Redact on the output hot path.
//
// A resolved CREDENTIAL is never reclaimed - it stays valid wherever it was sent. An
// endpoint token is only meaningful while its listener is up.
func TestEndpointReclaimsRedactionOnShutdown(t *testing.T) {
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	ctx, cancel := context.WithCancel(ContextWithInvocationID(context.Background(), "inv-"+t.Name()))
	base, err := res.OpenEndpoint(ctx, g)
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

// TestEndpointsAreScopedToOneRun replaces an earlier refcount test. Two concurrent runs
// wanting the same grant now get two sockets and two TOKENS, which is the point: a token
// leaked from one run - into a log, a child's argv, a crash dump - authorizes nothing in
// any other run. One run finishing also cannot close a socket another is still using,
// which is the lifetime bug sharing required a refcount to avoid.
func TestEndpointsAreScopedToOneRun(t *testing.T) {
	_, res, g := upstream(t, func(w http.ResponseWriter, r *http.Request) {})
	t.Setenv("UPSTREAM_KEY", "sk-upstream-value")

	runA, cancelA := context.WithCancel(ContextWithInvocationID(context.Background(), "inv-a"))
	runB, cancelB := context.WithCancel(ContextWithInvocationID(context.Background(), "inv-b"))
	defer cancelB()

	baseA, err := res.OpenEndpoint(runA, g)
	require.NoError(t, err)
	baseB, err := res.OpenEndpoint(runB, g)
	require.NoError(t, err)
	assert.NotEqual(t, baseA, baseB, "two runs shared one endpoint, so they shared a token")

	// Within ONE run the endpoint is still memoized: three targets naming it bind one
	// socket, not three.
	again, err := res.OpenEndpoint(runA, g)
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

// TestEndpointIgnoresProxyEnvironment: the no-CA argument rests on magus making an
// ordinary verified TLS connection upstream itself. Honoring HTTPS_PROXY would delegate
// exactly the interception this design refuses to perform - the destination is disclosed
// to the proxy, and if the proxy's CA is trusted the guarantee is gone.
func TestEndpointIgnoresProxyEnvironment(t *testing.T) {
	tr, ok := endpointTransport().(*http.Transport)
	require.True(t, ok)
	assert.Nil(t, tr.Proxy, "the forwarder would route a credential through HTTPS_PROXY")
	assert.Equal(t, endpointResponseHeaderTimeout, tr.ResponseHeaderTimeout)
}
