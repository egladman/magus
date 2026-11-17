package secrets

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// The subprocess half of a grant: requests a CHILD makes, where the caller does not own
// the HTTP client.
//
// The mechanism is deliberately NOT interception. Nothing here terminates anyone else's
// TLS, generates a CA, or asks for one to be installed. The forwarder serves plain HTTP on
// a loopback socket it owns, the caller hands the child that URL through whatever setting
// the tool already reads, and the forwarder makes an ordinary, fully verified TLS
// connection upstream itself.
//
// That is a threat-model decision, not a convenience one. A CA in the trust store is a
// universal interception capability, and the adversary it would supposedly defend against
// (a process already executing as the user) can read the CA, read the caller's memory, or
// hook the TLS library anyway.
//
// There is no standing service: the listener binds inside the caller's scope and closes
// when the context it was opened with ends.
//
// A child that ignores the endpoint and dials the real API directly arrives with no
// credential and gets a 401. That fail-closed property is why no egress enforcement is
// needed here.

// endpointListenAddr is the only interface a forwarder binds. Loopback, always: the
// socket carries plaintext HTTP with a credential attached on the way out, so a
// non-loopback bind would put it on the network.
const endpointListenAddr = "127.0.0.1:0"

// endpointShutdownGrace bounds how long a forwarder waits for in-flight requests when
// its context ends. Short: the run is over, and a child still talking to it is already
// past whatever its scope was doing.
const endpointShutdownGrace = 2 * time.Second

// endpointIdleTimeout closes a kept-alive connection nobody is using. Without it any
// local process could hold a connection (and a server goroutine) open for the
// forwarder's whole life just by connecting.
const endpointIdleTimeout = 90 * time.Second

// endpointReadHeaderTimeout bounds how long a local client may take to send its request
// headers, so a connection that never finishes a request cannot hold a server goroutine.
const endpointReadHeaderTimeout = 10 * time.Second

// endpointTokenLen is the byte length of an endpoint's path token. 16 bytes is 128 bits of
// crypto/rand, which is the margin that lets the loopback bind be defensible at all.
const endpointTokenLen = 16

// endpointResponseHeaderTimeout bounds how long an upstream may stall before it starts
// answering. Deliberately NOT an overall request timeout: a streamed completion can take
// minutes to finish legitimately, and capping total duration is what breaks it. Waiting
// forever for the first byte is the part with no honest use.
const endpointResponseHeaderTimeout = 60 * time.Second

// endpointTransport is the outbound half of a forwarder.
//
// Two departures from http.DefaultTransport, both load-bearing:
//
//   - ResponseHeaderTimeout, because the default waits forever for the first byte, so a
//     stalled upstream would pin a goroutine and a connection indefinitely.
//   - Proxy is nil, so HTTPS_PROXY / ALL_PROXY cannot reroute a credential-bearing
//     request. The whole no-CA position rests on the forwarder making an ordinary,
//     fully verified TLS connection upstream itself. Honoring an ambient proxy variable would undercut exactly that: a CONNECT
//     tunnel still protects the body, but the destination is disclosed to the proxy, and
//     if the proxy's CA is in the trust store the guarantee is gone entirely, which is
//     the interception this design refuses to perform and should not delegate either.
//
// The cost: a caller that genuinely requires an egress proxy cannot reach the upstream
// through an endpoint, and should read the secret instead.
//
// base is the transport it departs from, http.DefaultTransport outside tests. One that
// is not an *http.Transport is a [*TransportError]: neither departure can be made on
// it, and using it as it is would route a credential through whatever proxy it honors,
// with no first-byte timeout.
func endpointTransport(base http.RoundTripper) (*http.Transport, error) {
	t, ok := base.(*http.Transport)
	if !ok {
		return nil, &TransportError{Type: fmt.Sprintf("%T", base)}
	}
	c := t.Clone()
	c.ResponseHeaderTimeout = endpointResponseHeaderTimeout
	c.Proxy = nil
	return c, nil
}

// TransportError is a forwarder refused because http.DefaultTransport is not an
// *http.Transport, so its proxy and first-byte timeout cannot be set. Type is the type
// the process replaced it with.
type TransportError struct {
	Type string
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("secrets: http.DefaultTransport is a %s, not an *http.Transport, so the endpoint cannot turn off its proxy or bound its first byte", e.Type)
}

// forwarder is one running loopback endpoint, belonging to exactly one scope.
type forwarder struct {
	baseURL string
	// done closes when the owning context ends. Several contexts can share one scope, so
	// a cached entry can be dead; checking it also makes the shutdown/rebind race benign,
	// since forgetEndpoint runs asynchronously after Shutdown returns.
	done <-chan struct{}
}

// alive reports whether f's owning context is still running.
func (f *forwarder) alive() bool {
	select {
	case <-f.done:
		return false
	default:
		return true
	}
}

// endpointKey identifies a forwarder: the WHOLE grant, so two grants differing only by
// header or prefix get their own endpoint rather than silently sharing one, plus the
// SCOPE that opened it and the provider that resolves its credential, since Resolvers
// derived with [Resolver.WithProvider] share one endpoint map.
//
// The scope is part of the key so a token cannot outlive the scope that was handed it.
// Two concurrent scopes wanting the same grant get two sockets and two tokens, which costs
// a file descriptor and buys the property that matters: a token leaked from one (into a
// log, a child's argv, a crash dump) authorizes nothing in the other. It also needs no
// refcount, and so has no question of whose context owns a shared listener.
type endpointKey struct {
	scope    string
	provider string
	ref      string
	host     string
	header   string
	prefix   string
}

func (r *Resolver) endpointKey(scope string, g Grant) endpointKey {
	return endpointKey{scope: scope, provider: r.provider, ref: g.Ref, host: g.Host, header: g.Header, prefix: g.Prefix}
}

// ErrNoScope is why [Resolver.OpenEndpoint] refuses an empty scope. It arrives wrapped
// in an *[EndpointError].
var ErrNoScope = errors.New("no scope, so nothing would close the endpoint or confine its token")

// EndpointError is why [Resolver.OpenEndpoint] opened no endpoint for the grant of Ref.
type EndpointError struct {
	Ref string
	Err error
}

func (e *EndpointError) Error() string {
	return fmt.Sprintf("secrets: endpoint for %q: %v", e.Ref, e.Err)
}

func (e *EndpointError) Unwrap() error { return e.Err }

// OpenEndpoint returns the loopback base URL a child should be pointed at so its requests
// to g.Host carry g's credential, starting the forwarder on first use. The credential is
// resolved on each request, never here, so opening one prompts for nothing.
//
// scope names the unit of work the endpoint belongs to, such as a run's id: it confines
// the endpoint's token to that scope, and an empty one is refused with [ErrNoScope]. The
// listener closes when ctx ends, so ctx must end with the scope. Within one scope the
// endpoint is memoized per grant, and liveness-checked, because a cached entry whose
// context has ended would hand back a dead URL on a RELEASED port that another local
// process could then bind.
//
// It binds a TCP listener, starts goroutines and mints a token; it is not an accessor.
// g is normalized first, so a malformed grant is a *[GrantError]; any other failure is
// an *[EndpointError], or a *[NoResolverError] for a nil Resolver.
func (r *Resolver) OpenEndpoint(ctx context.Context, scope string, g Grant) (string, error) {
	if r == nil {
		return "", &NoResolverError{}
	}
	g, err := g.Normalize()
	if err != nil {
		return "", err
	}
	if scope == "" {
		return "", &EndpointError{Ref: g.Ref, Err: ErrNoScope}
	}
	key := r.endpointKey(scope, g)

	r.endpointMu.Lock()
	defer r.endpointMu.Unlock()
	if f, ok := r.endpoints[key]; ok {
		if f.alive() {
			return f.baseURL, nil
		}
		// Dead, and forgetEndpoint has not swept it yet. Rebind rather than hand back a
		// URL on a port that is being released.
		delete(r.endpoints, key)
	}
	f, err := r.startForwarder(ctx, key, g)
	if err != nil {
		return "", &EndpointError{Ref: g.Ref, Err: err}
	}
	if r.endpoints == nil {
		r.endpoints = make(map[endpointKey]*forwarder)
	}
	r.endpoints[key] = f
	return f.baseURL, nil
}

// startForwarder binds a loopback listener and serves g's destination behind it.
//
// The base URL carries a RANDOM PATH TOKEN, and a request that does not present it is
// refused. Without one, every process on the machine can reach the port and spend the
// credential: the forwarder would be a local oracle handing out authenticated access
// to anyone who guessed a port number. The token rides in the URL the caller gives the
// child, so a tool that takes a base URL needs no extra wiring to send it.
//
// runCtx, NOT the per-request context, is what the handler resolves against. The
// request context comes from net/http and carries none of the values the caller put on
// runCtx, so a provider reading through it would lose them, and every log record in the
// read path would miss the redacting handler's resolver.
func (r *Resolver) startForwarder(runCtx context.Context, key endpointKey, g Grant) (*forwarder, error) {
	transport := r.endpointTransport
	if transport == nil {
		t, err := endpointTransport(http.DefaultTransport)
		if err != nil {
			return nil, err
		}
		transport = t
	}
	tok, err := endpointToken()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", endpointListenAddr)
	if err != nil {
		return nil, fmt.Errorf("bind loopback endpoint: %w", err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, fmt.Errorf("unexpected listener address type %T", ln.Addr())
	}

	// The token is registered SEPARATELY from the base URL, not only as part of it.
	// Redaction is literal substring matching, so a child that logs the request path
	// alone ("GET /<tok>/v1/models", the ordinary access-log shape) would print the
	// token in the clear if only the composite URL were registered.
	r.Register(New(tok))

	proxy := &httputil.ReverseProxy{
		Rewrite:      r.rewrite(g, tok),
		ErrorHandler: endpointErrorHandler(g),
		// Without this, ReverseProxy logs transport errors through the process-global
		// log package straight to stderr, past any redacting handler.
		ErrorLog: r.errorLog(runCtx, "secret.endpoint.proxy"),
	}
	proxy.Transport = transport

	srv := &http.Server{
		Handler: r.endpointHandler(runCtx, g, tok, proxy),
		// NO TimeoutHandler and NO WriteTimeout, deliberately. Both bound the whole
		// exchange, and both break STREAMING: Go's timeoutWriter implements no Flusher,
		// so ReverseProxy cannot flush through it and buffers the entire response in
		// memory. An SSE completion is the marquee use case for pointing an agent at an
		// endpoint, so a forwarder that cannot stream is a forwarder that does not work.
		//
		// The risky wait is bounded on the OUTBOUND leg instead (see endpointTransport),
		// which caps how long an upstream may stall before answering without capping how
		// long it may take to finish answering.
		ReadHeaderTimeout: endpointReadHeaderTimeout,
		IdleTimeout:       endpointIdleTimeout,
		ErrorLog:          r.errorLog(runCtx, "secret.endpoint.server"),
	}
	base := fmt.Sprintf("http://127.0.0.1:%d/%s", addr.Port, tok)
	// The base URL is registered for redaction like a credential, because for the
	// forwarder that is exactly what it is: presenting the token authorizes an
	// authenticated request, so a URL reaching anything recorded hands any local process
	// the ability to spend the credential.
	r.Register(New(base))

	go func() { _ = srv.Serve(ln) }()

	go func() {
		<-runCtx.Done()
		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(runCtx), endpointShutdownGrace)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		// Reclaim everything this forwarder added. Eagerly, here, rather than lazily on
		// the next lookup: a long-lived resolver would otherwise accumulate a dead map
		// entry plus sixteen redaction strings per scope, and Redact scans that set on
		// every call.
		//
		// AFTER Shutdown returns, so in-flight responses are still masked while they
		// drain. Once the listener is down the token authorizes nothing, so continuing
		// to redact it would only shred ordinary output.
		r.forgetEndpoint(key, tok, base)
	}()

	r.log.DebugContext(runCtx, "secret.endpoint.opened",
		slog.String("ref", g.Ref), slog.String("host", g.Host), slog.Int("port", addr.Port))
	return &forwarder{baseURL: base, done: runCtx.Done()}, nil
}

// grantValueKey carries the resolved header value from the handler to the rewrite.
//
// The credential MUST be attached inside Rewrite, not on the inbound request.
// ReverseProxy strips hop-by-hop headers from its outbound clone BEFORE Rewrite runs,
// honoring whatever the caller named in Connection, so a local process holding the
// token could send "Connection: Authorization" and have the proxy remove the
// credential just set, sending an unauthenticated request upstream. Setting it in
// Rewrite puts it past that point.
type grantValueKey struct{}

// rewrite builds the ReverseProxy rewrite: retarget the request at the granted
// host over TLS, strip the token segment, attach the credential, and drop the headers
// a caller could use to interfere with any of that.
func (r *Resolver) rewrite(g Grant, tok string) func(*httputil.ProxyRequest) {
	return func(pr *httputil.ProxyRequest) {
		pr.Out.URL.Scheme = "https"
		pr.Out.URL.Host = g.Host
		// Host as well as URL.Host: the former is what goes on the wire and drives SNI,
		// and leaving it as the loopback address makes a virtual-hosted API answer for
		// the wrong site or reject the request outright.
		pr.Out.Host = g.Host

		// Trim on the ESCAPED path and set both forms. Trimming the decoded Path alone
		// would turn an encoded separator into a real one: "/<tok>/v1/a%2Fb" would reach
		// the upstream as "/v1/a/b", a different resource than the child asked for.
		escaped := strings.TrimPrefix(pr.In.URL.EscapedPath(), "/"+tok)
		if escaped == "" {
			escaped = "/"
		}
		pr.Out.URL.RawPath = ""
		if unescaped, err := url.PathUnescape(escaped); err == nil {
			pr.Out.URL.Path = unescaped
			if unescaped != escaped {
				pr.Out.URL.RawPath = escaped
			}
		} else {
			pr.Out.URL.Path = escaped
		}

		// Dropped rather than forwarded: an endpoint is a credential forwarder, and
		// honoring a caller's connection-control headers only gives a local process
		// levers over how the forwarder's own upstream request is framed. Losing websocket
		// upgrades through an endpoint is the correct trade.
		pr.Out.Header.Del("Connection")
		pr.Out.Header.Del("Upgrade")
		// Referer and Origin routinely carry the full loopback URL a child was given,
		// token included, and the upstream is the one party in this design that is
		// assumed hostile. The endpoint URL is credential-equivalent (it is registered
		// for redaction as one), so it does not get forwarded.
		pr.Out.Header.Del("Referer")
		pr.Out.Header.Del("Origin")

		// Attached LAST, after every strip; see grantValueKey.
		if v, ok := pr.In.Context().Value(grantValueKey{}).(string); ok {
			pr.Out.Header.Set(g.Header, v)
		}
	}
}

// endpointHandler authorizes the path token, resolves the credential, attaches it, and
// forwards. Resolution happens HERE rather than when the endpoint was opened, so
// declaring one still prompts for nothing until a child actually sends a request.
//
// A header the CHILD already set is OVERWRITTEN. The caller here is a third-party SDK:
// nearly every API library sets its auth header unconditionally from a key it insists on
// being given, with no supported way to suppress it. Erroring would make the endpoint
// unusable with the exact clients it exists for, and refusing to overwrite would send the
// child's dummy key upstream instead of the real one.
//
// So the pattern is: give the SDK any non-empty placeholder so it stops refusing to start,
// point it at this endpoint, and the forwarder replaces the header. The child's value is
// garbage BY CONSTRUCTION, which is what makes overwriting it correct.
func (r *Resolver) endpointHandler(runCtx context.Context, g Grant, tok string, proxy http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !loopbackHost(req.Host) {
			// The listener is loopback-only, so a non-loopback Host means the request
			// arrived through a name that resolves here: the DNS-rebinding shape.
			// Closed categorically rather than relied on the token alone.
			http.NotFound(w, req)
			return
		}
		if !hasTokenSegment(req.URL.EscapedPath(), tok) {
			// Deliberately terse and 404, not 403: an unauthorized caller learns only
			// that nothing is served there, not that it found a granted credential one
			// correct guess away.
			http.NotFound(w, req)
			return
		}
		// runCtx, not req.Context(): see startForwarder.
		v, err := r.Read(runCtx, g.Ref)
		if err != nil {
			// The DETAIL goes to the logger; the socket gets a fixed string. A provider
			// error can embed a subprocess's stderr, and on this path the value was never
			// recorded, so nothing downstream would mask a credential it happened to quote.
			r.log.ErrorContext(runCtx, "secret.endpoint.resolve_failed",
				slog.String("ref", g.Ref), slog.String("host", g.Host), slog.Any("err", err))
			http.Error(w, "secret endpoint: could not resolve the granted credential; see the log", http.StatusBadGateway)
			return
		}
		// Reveal where the credential crosses into the outbound header; see rewrite.
		proxy.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), grantValueKey{}, g.Prefix+v.Reveal())))
	})
}

// endpointErrorHandler renders an upstream transport failure.
//
// The response body is written straight to the socket and is NOT redacted. So it deliberately carries only the host
// and the error, and must never be extended to echo a header or a request.
func endpointErrorHandler(g Grant) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, fmt.Sprintf("secret endpoint: upstream %s: %s", g.Host, err.Error()), http.StatusBadGateway)
	}
}

// hasTokenSegment reports whether escapedPath's FIRST path segment is exactly tok.
//
// Segment-scoped, not a prefix test: strings.HasPrefix accepted "/<tok>extra", which
// then trimmed to a path with no leading slash and produced a malformed request line
// upstream. Constant-time, because this is the only check standing between any local
// process and an authenticated request.
func hasTokenSegment(escapedPath, tok string) bool {
	seg := strings.TrimPrefix(escapedPath, "/")
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	return subtle.ConstantTimeCompare([]byte(seg), []byte(tok)) == 1
}

// loopbackHost reports whether a Host header names this machine's loopback interface.
func loopbackHost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

// endpointToken mints the per-endpoint path token. crypto/rand because this is the
// only thing standing between any local process and an authenticated request.
func endpointToken() (string, error) {
	b := make([]byte, endpointTokenLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// errorLog adapts net/http's *log.Logger error sink onto the resolver's logger, so a
// proxy or server error reaches the same handler (and its redaction) instead of being
// written raw to stderr. Records carry runCtx, the forwarder's own lifetime, the same
// context its handler resolves against.
func (r *Resolver) errorLog(runCtx context.Context, msg string) *log.Logger {
	return log.New(writerFunc(func(p []byte) (int, error) {
		r.log.WarnContext(runCtx, msg, slog.String("detail", strings.TrimRight(string(p), "\n")))
		return len(p), nil
	}), "", 0)
}

// writerFunc adapts a function to io.Writer.
type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// forgetEndpoint drops a shut-down forwarder's map entry and reclaims its redaction
// strings. Idempotent, and safe to call for an entry another OpenEndpoint has already
// replaced: the map delete is guarded on identity, so a rebind that reused this key is
// not evicted by the previous forwarder's shutdown.
//
// Lock order is endpointMu then mu, matching OpenEndpoint. unregisterRedactable takes
// mu itself, so it is called outside the endpointMu critical section only because
// nothing requires them together, not because the order is optional.
func (r *Resolver) forgetEndpoint(key endpointKey, tok, base string) {
	r.endpointMu.Lock()
	if f, ok := r.endpoints[key]; ok && f.baseURL == base {
		delete(r.endpoints, key)
	}
	r.endpointMu.Unlock()

	r.unregisterRedactable(New(tok))
	r.unregisterRedactable(New(base))
}
