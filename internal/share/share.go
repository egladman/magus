// Package share implements the server side of "share to phone": an on-demand,
// time-boxed LAN listener that serves the console's READ surface to a phone on
// the same network, guarded by a single short-lived read-only token.
//
// Deliberately a THIRD listener, distinct from the server's loopback-bound standing
// ones: it binds the machine's LAN IPv4 on an ephemeral port and exists only while a
// share is active. There is no loopback guard (the phone is remote, which is the
// point), so the TOKEN is the sole gate, and listener and token are created and
// destroyed together so neither outlives the other.
//
// This package is the subject of two figures on the docs site; edit them alongside it.
// magus:diagram server-share (the boxes "/api/v1/share" and "LAN listener").
// magus:diagram server-http (the box "/api/v1/share").
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

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// iface is the minimal, testable projection of a network interface that
// pickLANIPv4 needs: whether it is up and a loopback, and its addresses. The
// real selector maps net.Interface into this; tests construct it directly so the
// filtering logic is exercised without a live network.
type iface struct {
	Up       bool
	Loopback bool
	Addrs    []netip.Addr
}

// pickLANIPv4 returns the first usable private LAN IPv4 across ifaces, in
// interface then address order: the interface must be up and not loopback, and
// the address must be an IPv4 in an RFC-1918 private range (10/8, 172.16/12,
// 192.168/16). Link-local (169.254/16), loopback, IPv6, and public addresses
// are all skipped. It reports false when nothing qualifies; the caller turns
// that into a clear "no LAN interface" error rather than sharing on a public or
// nonexistent address.
func pickLANIPv4(ifaces []iface) (netip.Addr, bool) {
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
	ifaces := make([]iface, 0, len(raw))
	for _, ri := range raw {
		ifc := iface{
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

// Link is the public description of an active share link, returned to the console.
type Link struct {
	// URL is the full link (with the token in the fragment) a phone loads.
	URL string
	// ExpiresAt is when the listener closes and the token dies.
	ExpiresAt time.Time
	// Superseded reports whether starting this share revoked a previous active
	// one, so the UI can tell the user the old QR just died.
	Superseded bool
	// Credential is the minted link's credential, for the trail. Never its secret.
	Credential types.Credential
}

// active holds the runtime state of one live share: the closer that tears the
// listener down plus the token record and mint time. Exactly one is live at a time.
// It retains the token record (hash and expiry, never the secret) and the mint
// time so a management surface can list and identify the live share via
// [Manager.Active] without reaching into the URL for the secret.
type active struct {
	cancel  context.CancelFunc
	tok     auth.ShareToken
	created time.Time
}

// TokenInfo is the secret-free description of the active share token, for a management
// surface (the console Settings token list). ID is the 8-hex identifier used to revoke
// it; it never contains the token bytes.
type TokenInfo struct {
	ID      string
	Created time.Time
	Expires time.Time
}

// Manager owns the at-most-one active share. Start opens a fresh listener
// (superseding any current one); Close tears the current one down. It is safe
// for concurrent use.
type Manager struct {
	parent context.Context
	log    *slog.Logger

	// selectAddr picks the bind IP. Production uses SelectLANIPv4; tests swap in
	// a loopback selector so the listener lifecycle can be exercised off a LAN.
	selectAddr func() (netip.Addr, error)
	// mint is auth.MintShare; a test swaps in one whose token expires sooner than the
	// minimum, to exercise the timeout teardown without waiting a minute.
	mint func(minter types.Grant, ttl time.Duration) (string, auth.ShareToken, error)

	// grace is shutdownGrace; a test shortens it.
	grace time.Duration

	// trailDir is the activity-trail base (the workspace cache dir). When set, the
	// first authenticated request from each remote device on a link records one
	// "share link opened" event there. Empty disables recording (the trail is never
	// a precondition for serving a share).
	trailDir string

	mu  sync.Mutex
	cur *active
}

// option configures a Manager at construction. It is the variadic-options seam so a
// caller adds behavior (a trail dir today) without a wider NewManager signature or a
// post-construction setter whose ordering the caller has to get right.
type option func(*Manager)

// WithTrailDir points the manager at the activity-trail base directory so that the
// first request from each remote device on a live share records a "share link opened"
// event. Empty disables recording (the trail is never a precondition for serving a
// share). It replaces the old SetTrailDir setter, so the wiring is set once at
// construction and there is no "call it before Start" ordering trap.
func WithTrailDir(dir string) option {
	return func(m *Manager) { m.trailDir = dir }
}

// WithListenAddr binds every share listener on addr instead of the first LAN IPv4, for a
// machine whose phone reaches it on another interface.
func WithListenAddr(addr netip.Addr) option {
	return func(m *Manager) { m.selectAddr = func() (netip.Addr, error) { return addr, nil } }
}

// NewManager returns a Manager whose listeners are torn down when parent is cancelled (server
// shutdown).
func NewManager(parent context.Context, log *slog.Logger, opts ...option) *Manager {
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{parent: parent, log: log, selectAddr: SelectLANIPv4, mint: auth.MintShare, grace: shutdownGrace}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Route is one data route a share serves: its handler, the format its refusals are written
// in, and the Need of each path under it (a Connect service names each procedure; a plain
// route names itself). They are the same Needs the loopback server holds the route to.
type Route struct {
	Handler http.Handler
	Format  rpcerr.Format
	Needs   map[string]types.Need
}

// Start mints a fresh read-only token and opens a new LAN listener serving the
// console from consoleDir at /console/ (unauthenticated static assets) and every
// route in guarded behind the new token (path -> route). Any previously
// active share is revoked first, so there is exactly one live token bound 1:1 to
// exactly one live listener: a token from a prior link validates nowhere.
// The listener closes and the token expires together after ttl (or on parent
// cancellation / Close); a non-positive ttl is auth.DefaultShareTTL. When the link dies, every
// request on it is cancelled at once (their contexts derive from the link's) and the listener
// is closed after a short grace, so an open stream does not outlive the link. consoleDir must
// contain the built console.
//
// minter is the grant of the credential that asked. The link is minted by the same rule as
// every stored token (auth.MintShare), so a minter below [types.GrantViewer] gets
// auth.ErrExceedsGrant and a ttl outside [auth.MinShareTTL, auth.MaxShareTTL] gets
// auth.ErrShareLifetime; neither opens a listener.
//
// No ctx parameter on purpose: a share OUTLIVES the request that opened it, so accepting
// the caller's context invites the wrong wiring: the HTTP handler passes r.Context(),
// which would tear the share down the instant that POST returned.
func (m *Manager) Start(minter types.Grant, consoleDir string, guarded map[string]Route, ttl time.Duration) (Link, error) {
	if ttl <= 0 {
		ttl = auth.DefaultShareTTL
	}
	secret, tok, err := m.mint(minter, ttl)
	if err != nil {
		return Link{}, err
	}
	addr, err := m.selectAddr()
	if err != nil {
		return Link{}, err
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(addr.String(), "0"))
	if err != nil {
		return Link{}, fmt.Errorf("share: bind LAN listener on %s: %w", addr, err)
	}
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return Link{}, fmt.Errorf("share: LAN listener address is not TCP")
	}
	port := tcp.Port
	// The token rides the fragment (#token=), never the path or query, so it is
	// not sent to the server on the initial document GET and does not land in an
	// access log; the console reads it client-side and replays it as a bearer.
	url := fmt.Sprintf("http://%s:%d/console/#token=%s", addr, port, secret)

	// The verifier is bound to THIS link's token only. A new link builds a
	// new closure over a new token, so an old link cannot authenticate here, and it
	// accepts only the mgl_ class, so the operator token never authenticates on the LAN.
	verify := func(presented string) (types.Credential, bool) {
		if !tok.Verify(presented, time.Now()) {
			return types.Credential{}, false
		}
		return tok.Credential(), true
	}
	mux := http.NewServeMux()
	// Static console: unauthenticated. The app shell is not a secret; it reads the
	// fragment token and replays it as a bearer on the guarded API routes below. It is
	// the SAME console.StaticHandler the loopback server mounts, so a phone reload of a
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
	// sg is this link's guard: the first-device binding plus the first-use
	// trail dedupe, both scoped to a single share. It is built per-Start, so a supersede
	// (which rebuilds Start) begins unbound with an empty seen-set.
	sg := newLinkGuard(m)
	// Every data route requires the session token. Header-only: the console reads
	// live data over fetch()-based SSE and Connect, both of which set an
	// Authorization header, so the token never needs to ride a URL here either. sg.admit
	// runs after BearerGuard, so it only ever sees requests that already carry a valid
	// token; it binds the first device and rejects the token replayed from any other.
	for pattern, rt := range guarded {
		h, err := httpx.ProcedureGuard(rt.Format, verify, rt.Needs, sg.admit(rt.Format, rt.Handler))
		if err != nil {
			_ = ln.Close()
			return Link{}, fmt.Errorf("share: %s: %w", pattern, err)
		}
		mux.Handle(pattern, h)
	}
	// The TTL and the parent lifetime are one context: whichever fires first
	// (timeout, server shutdown, or a Close/supersede cancel) tears the listener
	// down. Closing the listener and expiring the token are therefore the same
	// event: there is never a live listener with a dead token or vice versa.
	//
	// cancel is stored on active.cancel below (m.cur.cancel) and invoked later by
	// Manager.Close, Manager.CloseIf, and the supersede branch just below on the NEXT
	// Start, not leaked, just called through a field. This used to carry a
	// //nolint:gosec for G118; gosec no longer flags it, so nolintlint reported the
	// directive as unused and it is gone. The reasoning stays: it is why the pattern
	// is safe, not merely why a linter was quiet.
	ctx, cancel := context.WithDeadline(m.parent, tok.Expires)
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	// Supersede any current share and publish this one under the lock BEFORE starting
	// Serve and the shutdown watcher. Publishing first closes a race on teardown: if
	// the parent context is already cancelled (server shutting down), the watcher below
	// must find m.cur pointing at THIS link so Close/CloseIf can tear it down: a
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
	grace := m.grace
	go func() {
		<-ctx.Done()
		// WithoutCancel, not Background: this runs precisely BECAUSE ctx is done, so a
		// shutdown context derived from it directly would arrive already cancelled and
		// Shutdown would return without draining a single connection. WithoutCancel drops
		// the cancellation while keeping whatever values the server's context carries,
		// which is what anything logging during shutdown reads.
		shutCtx, sc := context.WithTimeout(context.WithoutCancel(ctx), grace)
		defer sc()
		// A stream that ignores its cancelled context holds Shutdown open; Close ends it.
		if err := srv.Shutdown(shutCtx); err != nil {
			_ = srv.Close()
		}
	}()

	if superseded {
		m.log.InfoContext(ctx, "[SHARE] superseded previous share", slog.String("addr", fmt.Sprintf("%s:%d", addr, port)))
	}
	m.log.InfoContext(ctx, "[SHARE] LAN share opened",
		slog.String("addr", fmt.Sprintf("%s:%d", addr, port)),
		slog.Time("expires", tok.Expires),
	)
	return Link{URL: url, ExpiresAt: tok.Expires, Superseded: superseded, Credential: tok.Credential()}, nil
}

// shutdownGrace is how long a dead link's listener lets in-flight requests finish before it
// closes their connections.
const shutdownGrace = 5 * time.Second

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
		ID:      m.cur.tok.ID(),
		Created: m.cur.created,
		Expires: m.cur.tok.Expires,
	}, true
}

// shareBoundOtherDeviceMsg is the 403 body a device gets when it presents a valid token
// but the share is already bound to a DIFFERENT device (a likely token-replay from a
// LAN sniffer). Plain ASCII, no trailing period, so it reads cleanly as an HTTP body.
const shareBoundOtherDeviceMsg = "share link is bound to another device"

// linkGuard is the per-Start post-verification guard for one live share. It runs
// only after BearerGuard admits a request (so it only ever sees a valid token) and
// enforces two things that must be scoped to a single share link: it binds the
// share to the first remote device and rejects the token replayed from any other, and
// it records the first-use trail event once per device. Both pieces of state are
// per-Start, so a supersede (which rebuilds Start) begins unbound with an empty
// seen-set.
type linkGuard struct {
	m *Manager

	bindMu    sync.Mutex
	boundHost string // the first device's host; empty until the first valid request binds it

	seenMu sync.Mutex
	seen   map[string]struct{}
}

// newLinkGuard builds an unbound guard for one share link.
func newLinkGuard(m *Manager) *linkGuard {
	return &linkGuard{m: m, seen: make(map[string]struct{})}
}

// admit wraps a guarded handler so device binding and first-use recording run only on
// an already-verified request (BearerGuard sits in front). A token replayed from a
// device other than the one that first bound the share is rejected with 403 before the
// handler runs; the bound device is served, and its first request records one trail
// event.
func (g *linkGuard) admit(format rpcerr.Format, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.bindDevice(remoteHost(r)) {
			format.Write(w, r, rpcerr.Error{
				Code:    connect.CodePermissionDenied,
				Reason:  types.ShareBoundToAnotherDevice,
				Message: shareBoundOtherDeviceMsg,
			})
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
// The whole check-and-set runs under bindMu, so two racing devices cannot both read an
// empty boundHost and both bind. boundHost is per-Start state, so a supersede begins
// unbound and the binding lives exactly as long as this share's listener and token.
//
// CAVEAT: the identity is the source IP, which is NOT stable across a NAT rebind or a
// Wi-Fi-to-cellular handoff. A phone that changes IP mid-session gets 403 and the
// operator must re-share, the accepted cost of making a sniffed plaintext token
// useless from another device. A rejected device never tears the share down.
func (g *linkGuard) bindDevice(host string) bool {
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
// recording genuinely never blocks the response the phone is waiting on, the reason
// the request fields are copied out before the goroutine starts.
func (g *linkGuard) recordFirstUse(r *http.Request) {
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
	// WithoutCancel because the append runs in a goroutine that outlives the request:
	// the context is carried for the secret resolver Append redacts against, not for
	// cancellation, and a cancelled ctx would still redact correctly but reads as a
	// deadline being ignored. A share listener carries no resolver today, so redaction
	// degrades to a no-op, which is the documented contract, not an oversight.
	trailCtx := context.WithoutCancel(r.Context())
	// KindTokenLifecycle is the closest existing activity kind: a share token being
	// exercised by a remote device is a lifecycle event of that token. No new proto
	// kind is added; the console derives the alert from this event frontend-side.
	go trail.Append(trailCtx, g.m.trailDir, trail.Event{
		Ts:        time.Now().UnixMilli(),
		Kind:      trail.KindTokenLifecycle,
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

// Close tears down the active share, if any. Idempotent. Called on server
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

// CloseIf tears the active share down only when its token id (as [Manager.Active]
// reports it) still equals id, and reports whether it did. It is the atomic
// check-and-close a revoke needs: a caller that read the active id via Active and then
// called Close could, in the window between the two, race a supersede and tear down a
// DIFFERENT share minted in the meantime. CloseIf re-checks identity while holding the
// lock, so it revokes exactly the share the caller named or nothing: a lost race leaves
// the new share alive and returns false (the revoke maps that to NotFound).
func (m *Manager) CloseIf(id string) bool {
	m.mu.Lock()
	cur := m.cur
	if cur == nil || cur.tok.ID() != id {
		m.mu.Unlock()
		return false
	}
	m.cur = nil
	m.mu.Unlock()
	cur.cancel()
	return true
}
