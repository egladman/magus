// Package share implements the daemon side of "share to phone": an on-demand,
// time-boxed LAN listener that serves the console's READ surface to a phone on
// the same network, guarded by a single short-lived read-only token.
//
// It is deliberately a THIRD listener, distinct from the daemon's two standing
// ones (the loopback MCP/console server, bound 127.0.0.1 always). This one binds
// the machine's LAN IPv4 on an ephemeral port, exists only while a share is
// active, and every data route on it requires the exact share token minted for
// that session. There is no loopback guard here - that is the whole point, the
// phone is remote - so the token is the sole gate, and the listener + token are
// created and destroyed together so neither can outlive the other.
//
// The console app is served from the SAME origin as its API on this listener, so
// the phone's browser never issues a cross-origin request and CORS never engages.
package share

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/trail"
)

// DefaultTTL is how long a share stays live before the listener closes and the
// token expires. Short by design: a share is a "glance at my phone" affordance,
// not a standing remote endpoint. Fifteen minutes is long enough to scan and
// look, short enough that a leaked QR is worth little.
const DefaultTTL = 15 * time.Minute

// Iface is the minimal, testable projection of a network interface that
// pickLANIPv4 needs: whether it is up and a loopback, and its addresses. The
// real selector maps net.Interface into this; tests construct it directly so the
// filtering logic is exercised without a live network.
type Iface struct {
	Up       bool
	Loopback bool
	Addrs    []netip.Addr
}

// pickLANIPv4 returns the first usable private LAN IPv4 across ifaces, in
// interface then address order: the interface must be up and not loopback, and
// the address must be an IPv4 in an RFC-1918 private range (10/8, 172.16/12,
// 192.168/16). Link-local (169.254/16), loopback, IPv6, and public addresses
// are all skipped. It reports false when nothing qualifies - the caller turns
// that into a clear "no LAN interface" error rather than sharing on a public or
// nonexistent address.
func pickLANIPv4(ifaces []Iface) (netip.Addr, bool) {
	for _, ifc := range ifaces {
		if !ifc.Up || ifc.Loopback {
			continue
		}
		for _, a := range ifc.Addrs {
			if a.Is4() && a.IsPrivate() {
				return a, true
			}
		}
	}
	return netip.Addr{}, false
}

// SelectLANIPv4 returns the machine's first up, non-loopback, private-range
// IPv4, or an error naming the shortfall. It gathers the real interfaces and
// delegates the choice to pickLANIPv4.
func SelectLANIPv4() (netip.Addr, error) {
	raw, err := net.Interfaces()
	if err != nil {
		return netip.Addr{}, fmt.Errorf("share: list interfaces: %w", err)
	}
	ifaces := make([]Iface, 0, len(raw))
	for _, ri := range raw {
		ifc := Iface{
			Up:       ri.Flags&net.FlagUp != 0,
			Loopback: ri.Flags&net.FlagLoopback != 0,
		}
		addrs, aerr := ri.Addrs()
		if aerr != nil {
			continue // an interface whose addrs we cannot read cannot be chosen
		}
		for _, ra := range addrs {
			ipn, ok := ra.(*net.IPNet)
			if !ok {
				continue
			}
			if a, ok := netip.AddrFromSlice(ipn.IP); ok {
				ifc.Addrs = append(ifc.Addrs, a.Unmap())
			}
		}
		ifaces = append(ifaces, ifc)
	}
	if a, ok := pickLANIPv4(ifaces); ok {
		return a, nil
	}
	return netip.Addr{}, fmt.Errorf("share: no up, non-loopback, private-range IPv4 interface found; connect to a LAN or Wi-Fi network and try again")
}

// Session is the public description of an active share, returned to the console.
type Session struct {
	// URL is the full link (with the token in the fragment) a phone loads.
	URL string
	// ExpiresAt is when the listener closes and the token dies.
	ExpiresAt time.Time
	// Superseded reports whether starting this share revoked a previous active
	// one, so the UI can tell the user the old QR just died.
	Superseded bool
}

// active holds the runtime state of one live share: the closer that tears the
// listener down plus the token record and mint time. Exactly one is live at a time.
// It retains the token record (hash + scope + expiry, never the secret) and the mint
// time so a management surface can list and identify the live share via
// [Manager.Active] without reaching into the URL for the secret.
type active struct {
	cancel  context.CancelFunc
	tok     auth.ShareToken
	created time.Time
}

// TokenInfo is the secret-free description of the active share token, for a management
// surface (the console Settings token list). Fingerprint is the prefix-only
// identifier used to revoke it; it never contains the token bytes.
type TokenInfo struct {
	Fingerprint string
	Scope       string
	Created     time.Time
	Expires     time.Time
}

// Manager owns the at-most-one active share. Start opens a fresh listener
// (superseding any current one); Close tears the current one down. It is safe
// for concurrent use.
type Manager struct {
	parent context.Context
	ttl    time.Duration
	log    *slog.Logger

	// selectAddr picks the bind IP. Production uses SelectLANIPv4; tests swap in
	// a loopback selector so the listener lifecycle can be exercised off a LAN.
	selectAddr func() (netip.Addr, error)

	// trailDir is the activity-trail base (the workspace cache dir). When set, the
	// first authenticated request from each remote device in a session records one
	// "share link opened" event there. Empty disables recording (the trail is never
	// a precondition for serving a share).
	trailDir string

	mu  sync.Mutex
	cur *active
}

// Option configures a Manager at construction. It is the variadic-options seam so a
// caller adds behavior (a trail dir today) without a wider NewManager signature or a
// post-construction setter whose ordering the caller has to get right.
type Option func(*Manager)

// WithTrailDir points the manager at the activity-trail base directory so that the
// first request from each remote device on a live share records a "share link opened"
// event. Empty disables recording (the trail is never a precondition for serving a
// share). It replaces the old SetTrailDir setter, so the wiring is set once at
// construction and there is no "call it before Start" ordering trap.
func WithTrailDir(dir string) Option {
	return func(m *Manager) { m.trailDir = dir }
}

// NewManager returns a Manager whose shares live for ttl (<=0 uses DefaultTTL)
// and whose listeners are torn down when parent is cancelled (daemon shutdown).
func NewManager(parent context.Context, ttl time.Duration, log *slog.Logger, opts ...Option) *Manager {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{parent: parent, ttl: ttl, log: log, selectAddr: SelectLANIPv4}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Start mints a fresh read-only token and opens a new LAN listener serving the
// console from consoleDir at /console/ (unauthenticated static assets) and every
// handler in guarded behind the new token (path -> handler). Any previously
// active share is revoked first, so there is exactly one live token bound 1:1 to
// exactly one live listener: a token from a prior session validates nowhere.
// The listener closes and the token expires together after the manager's ttl (or
// on parent cancellation / Close). consoleDir must contain the built console.
func (m *Manager) Start(consoleDir string, guarded map[string]http.Handler) (Session, error) {
	addr, err := m.selectAddr()
	if err != nil {
		return Session{}, err
	}

	secret, tok, err := auth.MintShareToken(m.ttl)
	if err != nil {
		return Session{}, err
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(addr.String(), "0"))
	if err != nil {
		return Session{}, fmt.Errorf("share: bind LAN listener on %s: %w", addr, err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	// The token rides the fragment (#token=), never the path or query, so it is
	// not sent to the server on the initial document GET and does not land in an
	// access log; the console reads it client-side and replays it as a bearer.
	url := fmt.Sprintf("http://%s:%d/console/#token=%s", addr, port, secret)

	// The verifier is bound to THIS session's token only. A new session builds a
	// new closure over a new token, so an old link cannot authenticate here.
	verify := func(presented string) bool { return tok.Verify(presented, time.Now()) }
	mux := http.NewServeMux()
	// Static console: unauthenticated. The app shell is not a secret; it reads the
	// fragment token and replays it as a bearer on the guarded API routes below. It is
	// the SAME console.StaticHandler the loopback daemon mounts, so a phone reload of a
	// clean /console/<surface>/ path hits the shell SPA fallback (not a 404) and gets
	// the same strict CSP.
	mux.Handle("/console/", console.StaticHandler(consoleDir))
	// A bare "/" load is a convenience redirect into the app.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/console/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
	// sg is this share's per-session guard: the first-device binding plus the first-use
	// trail dedupe, both scoped to a single share. It is built per-Start, so a supersede
	// (which rebuilds Start) begins unbound with an empty seen-set.
	sg := newSessionGuard(m)
	// Every data route requires the session token. Header-only: the console reads
	// live data over fetch()-based SSE and Connect, both of which set an
	// Authorization header, so the token never needs to ride a URL here either. sg.admit
	// runs after BearerGuard, so it only ever sees requests that already carry a valid
	// token; it binds the first device and rejects the token replayed from any other.
	for pattern, h := range guarded {
		mux.Handle(pattern, httpx.BearerGuard(verify, sg.admit(h)))
	}

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	// The TTL and the parent lifetime are one context: whichever fires first
	// (timeout, daemon shutdown, or a Close/supersede cancel) tears the listener
	// down. Closing the listener and expiring the token are therefore the same
	// event - there is never a live listener with a dead token or vice versa.
	ctx, cancel := context.WithTimeout(m.parent, m.ttl)

	// Supersede any current share and publish this one under the lock BEFORE starting
	// Serve and the shutdown watcher. Publishing first closes a race on teardown: if
	// the parent context is already cancelled (daemon shutting down), the watcher below
	// must find m.cur pointing at THIS session so Close/CloseIf can tear it down - a
	// listener published only after the goroutines start could serve on an address no
	// management surface knows to revoke. There is still exactly one live share:
	// superseding cancels the previous one before this replaces it.
	m.mu.Lock()
	superseded := m.cur != nil
	if m.cur != nil {
		m.cur.cancel()
	}
	m.cur = &active{cancel: cancel, tok: tok, created: time.Now().UTC()}
	m.mu.Unlock()

	go func() {
		_ = srv.Serve(ln)
	}()
	go func() {
		<-ctx.Done()
		shutCtx, sc := context.WithTimeout(context.Background(), 5*time.Second)
		defer sc()
		_ = srv.Shutdown(shutCtx)
	}()

	if superseded {
		m.log.Info("[SHARE] superseded previous share", slog.String("addr", fmt.Sprintf("%s:%d", addr, port)))
	}
	m.log.Info("[SHARE] LAN share opened",
		slog.String("addr", fmt.Sprintf("%s:%d", addr, port)),
		slog.Time("expires", tok.Expires),
	)
	return Session{URL: url, ExpiresAt: tok.Expires, Superseded: superseded}, nil
}

// Active returns secret-free metadata for the currently live share token, or
// ok=false when no share is active. A share whose token has already expired (in
// the brief window before its context fires and clears m.cur) reports ok=false, so
// a management surface never lists a dead token as if it were revocable.
func (m *Manager) Active() (TokenInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil || m.cur.tok.Expired(time.Now()) {
		return TokenInfo{}, false
	}
	return TokenInfo{
		Fingerprint: m.cur.tok.SHA256[:8],
		Scope:       m.cur.tok.Scope,
		Created:     m.cur.created,
		Expires:     m.cur.tok.Expires,
	}, true
}

// shareBoundOtherDeviceMsg is the 403 body a device gets when it presents a valid token
// but the share is already bound to a DIFFERENT device (a likely token-replay from a
// LAN sniffer). Plain ASCII, no trailing period, so it reads cleanly as an HTTP body.
const shareBoundOtherDeviceMsg = "share link is bound to another device"

// sessionGuard is the per-Start post-verification guard for one live share. It runs
// only after BearerGuard admits a request (so it only ever sees a valid token) and
// enforces two things that must be scoped to a single share session: it binds the
// share to the first remote device and rejects the token replayed from any other, and
// it records the first-use trail event once per device. Both pieces of state are
// per-Start, so a supersede (which rebuilds Start) begins unbound with an empty
// seen-set.
type sessionGuard struct {
	m *Manager

	bindMu    sync.Mutex
	boundHost string // the first device's host; empty until the first valid request binds it

	seenMu sync.Mutex
	seen   map[string]struct{}
}

// newSessionGuard builds an unbound guard for one share session.
func newSessionGuard(m *Manager) *sessionGuard {
	return &sessionGuard{m: m, seen: make(map[string]struct{})}
}

// admit wraps a guarded handler so device binding and first-use recording run only on
// an already-verified request (BearerGuard sits in front). A token replayed from a
// device other than the one that first bound the share is rejected with 403 before the
// handler runs; the bound device is served, and its first request records one trail
// event.
func (g *sessionGuard) admit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.bindDevice(remoteHost(r)) {
			http.Error(w, shareBoundOtherDeviceMsg, http.StatusForbidden)
			return
		}
		g.recordFirstUse(r)
		next.ServeHTTP(w, r)
	})
}

// bindDevice ties the live share to the FIRST remote host that presents a valid token
// and reports whether host may proceed. The LAN share listener is plain HTTP, so a
// passive sniffer on the network can capture and replay the token; binding makes a
// sniffed token useless from any device other than the one that first used it.
//
// The whole check-and-set runs under bindMu, so when two devices race the first request
// exactly one wins the binding (sets boundHost) and the other is measured against it -
// there is no window where both read an empty boundHost and both bind. boundHost is
// per-Start state, so a supersede (a fresh sessionGuard) begins unbound; the binding
// lives exactly as long as this share's listener and token.
//
// Caveat: the identity is the source IP, which is NOT stable across a NAT rebind or a
// Wi-Fi-to-cellular handoff. A legitimate phone that changes IP mid-session is locked
// out (gets 403) and the operator must re-share. That false-reject is the accepted
// cost of making a sniffed plaintext token useless from a different device; a rejected
// device never tears the share down, it just cannot use this link.
func (g *sessionGuard) bindDevice(host string) bool {
	g.bindMu.Lock()
	defer g.bindMu.Unlock()
	if g.boundHost == "" {
		g.boundHost = host
		return true
	}
	return g.boundHost == host
}

// recordFirstUse records one "share link opened" activity event the first time a
// given remote device presents a valid token on a live share, and nothing on that
// device's subsequent requests. It dedupes on the remote HOST (not host:port, which
// varies per TCP connection), so a page's many requests do not spam the trail. The
// user-agent and remote IP ride the event so the console can attribute the connect.
// No-op when no trail dir is configured.
//
// The dedupe decision runs synchronously under the lock (so "once per host" holds
// even when several requests race), but the disk write is spawned in a goroutine so
// recording genuinely never blocks the response the phone is waiting on - the reason
// the request fields are copied out before the goroutine starts.
func (g *sessionGuard) recordFirstUse(r *http.Request) {
	if g.m.trailDir == "" {
		return
	}
	host := remoteHost(r)
	g.seenMu.Lock()
	_, dup := g.seen[host]
	if !dup {
		g.seen[host] = struct{}{}
	}
	g.seenMu.Unlock()
	if dup {
		return
	}
	ua := r.UserAgent()
	// KindTokenLifecycle is the closest existing activity kind: a share token being
	// exercised by a remote device is a lifecycle event of that token. No new proto
	// kind is added; the console derives the alert from this event frontend-side.
	go trail.Append(g.m.trailDir, trail.Event{
		Ts:        time.Now().UnixMilli(),
		Kind:      trail.KindTokenLifecycle,
		Actor:     "share-guest",
		Action:    "share.open",
		Outcome:   trail.OutcomeOK,
		UserAgent: ua,
		Preview:   "share link opened from " + host,
	})
}

// remoteHost returns the host portion of r.RemoteAddr, dropping the per-connection
// port. It is the stable per-device identity used both to dedupe the trail event and
// to bind the share to one device; a plain RemoteAddr would vary per TCP connection
// (a new ephemeral port each time) and defeat both.
func remoteHost(r *http.Request) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host
}

// Close tears down the active share, if any. Idempotent. Called on daemon
// shutdown so no listener outlives the process.
func (m *Manager) Close() {
	m.mu.Lock()
	cur := m.cur
	m.cur = nil
	m.mu.Unlock()
	if cur != nil {
		cur.cancel()
	}
}

// CloseIf tears the active share down only when its token fingerprint (the first 8
// hex of the SHA-256, as [Manager.Active] reports it) still equals fingerprint, and
// reports whether it did. It is the atomic check-and-close a revoke needs: a caller
// that read the active fingerprint via Active and then called Close could, in the
// window between the two, race a supersede and tear down a DIFFERENT share minted in
// the meantime. CloseIf re-checks identity while holding the lock, so it revokes
// exactly the share the caller named or nothing - a lost race leaves the new share
// alive and returns false (the revoke maps that to NotFound).
func (m *Manager) CloseIf(fingerprint string) bool {
	m.mu.Lock()
	cur := m.cur
	if cur == nil || cur.tok.SHA256[:8] != fingerprint {
		m.mu.Unlock()
		return false
	}
	m.cur = nil
	m.mu.Unlock()
	cur.cancel()
	return true
}
