package secret

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/egladman/magus/types"
)

// The subprocess half of a grant. [Resolver.ApplyGrants] covers requests magus
// makes itself, where magus owns the HTTP client and can attach a credential with no
// ceremony. This file covers requests a CHILD makes, where it does not.
//
// The mechanism is deliberately NOT interception. magus does not terminate anyone
// else's TLS, does not generate a CA, and does not ask you to install one. Instead
// magus serves plain HTTP on a loopback socket it owns and the magusfile hands the
// child that URL through the env var the tool already reads. magus then makes an
// ordinary, fully-verified TLS connection upstream itself.
//
// That is a threat-model decision, not a convenience one. A CA in your trust store is
// a universal interception capability, and the adversary it would supposedly defend
// against - a process already executing as you - can read the CA, read magus's
// memory, or hook the TLS library anyway. Installing one to contain a process that is
// already inside the boundary it protects makes the machine weaker, not safer. See
// docs/concepts/secrets.md.
//
// There is no standing service to install or run. magus already launches the child, so
// the listener binds inside the run and closes when the run's context ends.
//
// A child that ignores the endpoint and dials the real API directly is not a bypass
// worth defending against: it arrives with no credential and gets a 401. That
// fail-closed property is why magus needs no egress enforcement here.

// endpointListenAddr is the only interface a forwarder binds. Loopback, always: the
// socket carries plaintext HTTP with a credential attached on the way out, so a
// non-loopback bind would put it on the network.
const endpointListenAddr = "127.0.0.1:0"

// endpointShutdownGrace bounds how long a forwarder waits for in-flight requests when
// its context ends. Short: the run is over, and a child still talking to it is already
// past whatever magus was orchestrating.
const endpointShutdownGrace = 2 * time.Second

// endpointIdleTimeout closes a kept-alive connection nobody is using. Without it any
// local process could hold a connection - and a server goroutine - open for the
// forwarder's whole life just by connecting.
const endpointIdleTimeout = 90 * time.Second

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
//     request. This file's opening argument is that magus makes "an ordinary, fully
//     verified TLS connection upstream itself", and the whole no-CA position rests on
//     it. Honoring an ambient proxy variable would undercut exactly that: a CONNECT
//     tunnel still protects the body, but the destination is disclosed to the proxy, and
//     if the proxy's CA is in the trust store the guarantee is gone entirely - which is
//     the interception this design refuses to perform and should not delegate either.
//
// The cost is stated rather than hidden: a workspace that genuinely requires an egress
// proxy cannot reach the upstream through an endpoint, and should use
// magus\secret.read instead until magus grows an explicit, per-grant proxy setting.
func endpointTransport() http.RoundTripper {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	c := t.Clone()
	c.ResponseHeaderTimeout = endpointResponseHeaderTimeout
	c.Proxy = nil
	return c
}

// forwarder is one running loopback endpoint, belonging to exactly one run.
type forwarder struct {
	baseURL string
	// done closes when the owning context ends. Still needed even though the key now
	// carries the run: a caller OUTSIDE a run (a test, a direct SDK call) has an empty
	// run id, so several unrelated contexts share that one bucket and a cached entry
	// there can be dead. Checking it also makes the shutdown/rebind race benign, since
	// forgetEndpoint runs asynchronously after Shutdown returns.
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
// header or prefix get their own endpoint rather than silently sharing one - matching
// the identity [Resolver.AddGrant] enforces - plus the RUN that opened it.
//
// The run is part of the key so a token cannot outlive the run that was handed it. Two
// concurrent runs wanting the same grant get two sockets and two tokens, which costs a
// file descriptor and buys the property that matters: a token leaked from one run - into
// a log, a child's argv, a crash dump - authorizes nothing in any other run. It also
// removes the refcount that sharing required, and with it a whole class of
// lifetime bug (whose context owns the listener when two runs hold it?).
type endpointKey struct {
	invocation string
	ref        string
	host       string
	header     string
	prefix     string
}

func keyFor(invocation string, g types.SecretGrant) endpointKey {
	return endpointKey{invocation: invocation, ref: g.Ref, host: g.Host, header: g.Header, prefix: g.Prefix}
}

// OpenEndpoint returns the loopback base URL a child should be pointed at so its
// requests to g.Host carry g's credential, starting the forwarder on first use.
//
// Named for what it does. It is not an accessor beside ProviderName/Timeouts: it binds
// a TCP listener, spawns goroutines and mints a token. "Origin" was also wrong twice
// over - types.ProjectOrigin already claims that word on the magusfile surface, and an
// origin is scheme+host+port by definition while this returns a base URL WITH a path.
//
// Memoized per grant, so a magusfile naming the same endpoint in three targets binds
// one socket. The memo is liveness-checked: the Resolver outlives a single run (it is
// per workspace Open), so a cached entry whose context has ended would otherwise hand
// back a dead URL on a RELEASED port - and under a local-attacker model another
// process that then binds that port receives the child's whole request stream.
func (r *Resolver) OpenEndpoint(ctx context.Context, g types.SecretGrant) (string, error) {
	if r == nil {
		return "", errNoResolver
	}
	g, err := g.Normalize()
	if err != nil {
		return "", err
	}
	// An endpoint must belong to a RUN, and the run id is what proves it does. Two
	// things depend on that and neither is optional: the listener shuts down when the
	// run's context ends, and the key scopes the token so a leak from one run
	// authorizes nothing in another.
	//
	// Gated on the run id rather than on ctx.Done() == nil, which was the first attempt
	// and does not work: cmd/magus/main.go builds the root context from
	// watchInterrupts(context.Background()), so preload - where a magusfile's TOP LEVEL
	// is evaluated, and which `magus ls`, `describe` and `graph` all reach - has a
	// perfectly good Done channel and sailed straight through. It would have bound a
	// credential-forwarding listener with a stable token for the whole process life,
	// reachable by any local process that ever saw the URL.
	//
	// This is the same laziness rule grant() follows, enforced structurally: an endpoint
	// belongs inside a target body.
	invocation := InvocationIDFromContext(ctx)
	if invocation == "" {
		return "", fmt.Errorf("secret grant %q: an endpoint belongs to a run, and this call is outside one - open it inside a target body rather than at the magusfile's top level, where it would outlive every run that could use it", g.Ref)
	}
	key := keyFor(invocation, g)

	r.endpointMu.Lock()
	defer r.endpointMu.Unlock()
	if f, ok := r.endpoints[key]; ok {
		if f.alive() {
			return f.baseURL, nil
		}
		// Dead, and forgetEndpoint has not swept it yet. Rebind rather than hand back a
		// URL on a port magus is in the middle of releasing.
		delete(r.endpoints, key)
	}
	f, err := r.startForwarder(ctx, key, g)
	if err != nil {
		return "", err
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
// credential - the forwarder would be a local oracle handing out authenticated access
// to anyone who guessed a port number. The token rides in the URL magus gives the
// child, so a tool that takes a base URL needs no extra wiring to send it.
//
// runCtx, NOT the per-request context, is what the handler resolves against. The
// request context comes from net/http and carries no resolver, no sandbox policy and
// no journal, so reading through it silently ignored magus.yaml's secret timeouts, ran
// a spell-backed provider outside the sandbox, and sent every slog record in the read
// path down the unredacted fast path.
func (r *Resolver) startForwarder(runCtx context.Context, key endpointKey, g types.SecretGrant) (*forwarder, error) {
	tok, err := endpointToken()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", endpointListenAddr)
	if err != nil {
		return nil, fmt.Errorf("secret grant %q: bind loopback endpoint: %w", g.Ref, err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, fmt.Errorf("secret grant %q: unexpected listener address type %T", g.Ref, ln.Addr())
	}

	// The token is registered SEPARATELY from the base URL, not only as part of it.
	// Redaction is literal substring matching, so a child that logs the request path
	// alone - "GET /<tok>/v1/models", the ordinary access-log shape - would print the
	// token in the clear if only the composite URL were registered.
	r.registerRedactable(tok)

	proxy := &httputil.ReverseProxy{
		Rewrite:      r.rewriteFor(g, tok),
		ErrorHandler: endpointErrorHandler(g),
		// Without this, ReverseProxy logs transport errors through the process-global
		// log package straight to stderr, outside NewRedactingHandler and outside the
		// capture tap - the two things that keep a credential out of magus's output.
		ErrorLog: slogErrorLog(runCtx, "secret.endpoint.proxy"),
	}
	proxy.Transport = endpointTransport()
	if r.endpointTransport != nil {
		proxy.Transport = r.endpointTransport
	}

	srv := &http.Server{
		Handler: r.endpointHandler(runCtx, g, tok, proxy),
		// NO TimeoutHandler and NO WriteTimeout, deliberately. Both bound the whole
		// exchange, and both break STREAMING: Go's timeoutWriter implements no Flusher,
		// so ReverseProxy cannot flush through it and buffers the entire response in
		// memory. An SSE completion is the marquee use case for pointing an agent at an
		// endpoint, so a forwarder that cannot stream is a forwarder that does not work.
		//
		// The risky wait is bounded on the OUTBOUND leg instead - see endpointTransport -
		// which caps how long an upstream may stall before answering without capping how
		// long it may take to finish answering.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       endpointIdleTimeout,
		ErrorLog:          slogErrorLog(runCtx, "secret.endpoint.server"),
	}
	base := fmt.Sprintf("http://127.0.0.1:%d/%s", addr.Port, tok)
	// The base URL is registered for redaction like a credential, because for the
	// forwarder that is exactly what it is: presenting the token authorizes an
	// authenticated request, so a URL reaching a log, a cached output or a knowledge
	// shard hands any local process the ability to spend the credential.
	r.registerRedactable(base)

	go func() { _ = srv.Serve(ln) }()

	go func() {
		<-runCtx.Done()
		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(runCtx), endpointShutdownGrace)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		// Reclaim everything this forwarder added. Eagerly, here, rather than lazily on
		// the next lookup: a daemon serving a workspace for days would otherwise
		// accumulate a dead map entry plus sixteen redaction strings per run, and Redact
		// scans that set on every captured write.
		//
		// AFTER Shutdown returns, so in-flight responses are still masked while they
		// drain. Once the listener is down the token authorizes nothing, so continuing
		// to redact it would only shred ordinary output.
		r.forgetEndpoint(key, tok, base)
	}()

	slog.DebugContext(runCtx, "secret.endpoint.opened",
		slog.String("ref", g.Ref), slog.String("host", g.Host), slog.Int("port", addr.Port))
	return &forwarder{baseURL: base, done: runCtx.Done()}, nil
}

// grantValueKey carries the resolved header value from the handler to the rewrite.
//
// The credential MUST be attached inside Rewrite, not on the inbound request.
// ReverseProxy strips hop-by-hop headers from its outbound clone BEFORE Rewrite runs,
// honoring whatever the caller named in Connection - so a local process holding the
// token could send "Connection: Authorization" and have the proxy remove the
// credential magus had just set, sending an unauthenticated request upstream. Setting
// it in Rewrite puts it past that point. Measured: this exact test failed before the
// move.
type grantValueKey struct{}

// rewriteFor builds the ReverseProxy rewrite: retarget the request at the granted
// host over TLS, strip the token segment, attach the credential, and drop the headers
// a caller could use to interfere with any of that.
func (r *Resolver) rewriteFor(g types.SecretGrant, tok string) func(*httputil.ProxyRequest) {
	return func(pr *httputil.ProxyRequest) {
		pr.Out.URL.Scheme = "https"
		pr.Out.URL.Host = g.Host
		// Host as well as URL.Host: the former is what goes on the wire and drives SNI,
		// and leaving it as the loopback address makes a virtual-hosted API answer for
		// the wrong site or reject the request outright.
		pr.Out.Host = g.Host

		// Trim on the ESCAPED path and set both forms. Trimming the decoded Path alone
		// turned an encoded separator into a real one: "/<tok>/v1/a%2Fb" reached the
		// upstream as "/v1/a/b", so the proxy silently addressed a different resource
		// than the child asked for.
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
		// levers over how magus's own upstream request is framed. Losing websocket
		// upgrades through an endpoint is the correct trade.
		pr.Out.Header.Del("Connection")
		pr.Out.Header.Del("Upgrade")
		// Referer and Origin routinely carry the full loopback URL a child was given,
		// token included, and the upstream is the one party in this design that is
		// assumed hostile. The endpoint URL is credential-equivalent - it is registered
		// for redaction as one - so it does not get forwarded.
		pr.Out.Header.Del("Referer")
		pr.Out.Header.Del("Origin")

		// Attached LAST, after every strip - see grantValueKey.
		if v, ok := pr.In.Context().Value(grantValueKey{}).(string); ok {
			pr.Out.Header.Set(g.Header, v)
		}
	}
}

// endpointHandler authorizes the path token, resolves the credential, attaches it, and
// forwards. Resolution happens HERE rather than when the endpoint was opened, so
// declaring one still prompts for nothing until a child actually sends a request.
//
// A header the CHILD already set is OVERWRITTEN, which is the opposite of what
// ApplyGrants does to a magusfile, and the asymmetry is the point rather than an
// oversight. The two have different callers:
//
//   - ApplyGrants' caller is the magusfile author. They control the request, so a
//     collision means they wrote two things that disagree, and erroring tells them.
//   - This caller is a third-party SDK. openai-python, the Anthropic client and
//     essentially every API library set their auth header unconditionally from a key
//     they insist on being given, with no supported way to suppress it. Erroring here
//     made the endpoint unusable with the exact clients it exists for, and refusing to
//     overwrite would send the child's dummy key upstream instead of the real one.
//
// So the documented pattern is: give the SDK any non-empty placeholder so it stops
// refusing to start, point it at this endpoint, and magus replaces the header. The
// child's value is garbage BY CONSTRUCTION, which is what makes overwriting it correct
// rather than presumptuous.
func (r *Resolver) endpointHandler(runCtx context.Context, g types.SecretGrant, tok string, proxy http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !loopbackHost(req.Host) {
			// The listener is loopback-only, so a non-loopback Host means the request
			// arrived through a name that resolves here - the DNS-rebinding shape.
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
			// The DETAIL goes to slog, which is redacted; the socket gets a fixed
			// string. A provider error can embed a spell subprocess's stderr, and on
			// this path the value was never recorded, so nothing downstream would mask
			// a credential it happened to quote.
			slog.ErrorContext(runCtx, "secret.endpoint.resolve_failed",
				slog.String("ref", g.Ref), slog.String("host", g.Host), slog.Any("err", err))
			http.Error(w, "magus secret endpoint: could not resolve the granted credential; see the magus log", http.StatusBadGateway)
			return
		}
		// Reveal where the credential crosses into the outbound header - see rewriteFor.
		proxy.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), grantValueKey{}, g.Prefix+v.Reveal())))
	})
}

// endpointErrorHandler renders an upstream transport failure.
//
// The response body is written straight to the socket and is NOT passed through the
// redaction rail - that covers captured subprocess output, journal text, cache logs
// and slog records, none of which this is. So it deliberately carries only the host
// and the error, and must never be extended to echo a header or a request.
func endpointErrorHandler(g types.SecretGrant) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, "magus secret endpoint: upstream "+g.Host+": "+err.Error(), http.StatusBadGateway)
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
		return "", fmt.Errorf("secret: generate endpoint token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// slogErrorLog adapts net/http's *log.Logger error sink onto slog, so a proxy or
// server error travels the same path every other magus log line does - including the
// redacting handler - instead of being written raw to stderr.
func slogErrorLog(ctx context.Context, msg string) *log.Logger {
	return log.New(slogWriter{ctx: ctx, msg: msg}, "", 0)
}

type slogWriter struct {
	ctx context.Context
	msg string
}

func (w slogWriter) Write(p []byte) (int, error) {
	slog.WarnContext(w.ctx, w.msg, slog.String("detail", strings.TrimRight(string(p), "\n")))
	return len(p), nil
}

// forgetEndpoint drops a shut-down forwarder's map entry and reclaims its redaction
// strings. Idempotent, and safe to call for an entry another OpenEndpoint has already
// replaced: the map delete is guarded on identity, so a rebind that reused this key is
// not evicted by the previous forwarder's shutdown.
//
// Lock order is endpointMu then mu, matching OpenEndpoint. unregisterRedactable takes
// mu itself, so it is called outside the endpointMu critical section only because
// nothing requires them together - not because the order is optional.
func (r *Resolver) forgetEndpoint(key endpointKey, tok, base string) {
	r.endpointMu.Lock()
	if f, ok := r.endpoints[key]; ok && f.baseURL == base {
		delete(r.endpoints, key)
	}
	r.endpointMu.Unlock()

	r.unregisterRedactable(tok)
	r.unregisterRedactable(base)
}
