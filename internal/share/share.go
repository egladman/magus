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

// active holds the runtime state of one live share: its token, expiry, and the
// closer that tears the listener down. Exactly one is live at a time.
type active struct {
	url       string
	expiresAt time.Time
	cancel    context.CancelFunc
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

	mu  sync.Mutex
	cur *active
}

// NewManager returns a Manager whose shares live for ttl (<=0 uses DefaultTTL)
// and whose listeners are torn down when parent is cancelled (daemon shutdown).
func NewManager(parent context.Context, ttl time.Duration, log *slog.Logger) *Manager {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if log == nil {
		log = slog.Default()
	}
	return &Manager{parent: parent, ttl: ttl, log: log, selectAddr: SelectLANIPv4}
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
	// fragment token and replays it as a bearer on the guarded API routes below.
	fs := http.StripPrefix("/console/", http.FileServer(http.Dir(consoleDir)))
	mux.Handle("/console/", fs)
	// A bare "/" load is a convenience redirect into the app.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/console/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
	// Every data route requires the session token. Header-only: the console reads
	// live data over fetch()-based SSE and Connect, both of which set an
	// Authorization header, so the token never needs to ride a URL here either.
	for pattern, h := range guarded {
		mux.Handle(pattern, httpx.BearerGuard(verify, h))
	}

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	// The TTL and the parent lifetime are one context: whichever fires first
	// (timeout, daemon shutdown, or a Close/supersede cancel) tears the listener
	// down. Closing the listener and expiring the token are therefore the same
	// event - there is never a live listener with a dead token or vice versa.
	ctx, cancel := context.WithTimeout(m.parent, m.ttl)
	go func() {
		_ = srv.Serve(ln)
	}()
	go func() {
		<-ctx.Done()
		shutCtx, sc := context.WithTimeout(context.Background(), 5*time.Second)
		defer sc()
		_ = srv.Shutdown(shutCtx)
	}()

	// Supersede any current share: revoke its token and close its listener before
	// this one is published, so only one share is ever live.
	m.mu.Lock()
	superseded := m.cur != nil
	if m.cur != nil {
		m.cur.cancel()
	}
	m.cur = &active{url: url, expiresAt: tok.Expires, cancel: cancel}
	m.mu.Unlock()

	if superseded {
		m.log.Info("[SHARE] superseded previous share", slog.String("addr", fmt.Sprintf("%s:%d", addr, port)))
	}
	m.log.Info("[SHARE] LAN share opened",
		slog.String("addr", fmt.Sprintf("%s:%d", addr, port)),
		slog.Time("expires", tok.Expires),
	)
	return Session{URL: url, ExpiresAt: tok.Expires, Superseded: superseded}, nil
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
