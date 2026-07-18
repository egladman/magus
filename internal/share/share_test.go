package share

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus/internal/trail"
)

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

func TestPickLANIPv4(t *testing.T) {
	tests := []struct {
		name   string
		ifaces []Iface
		want   string // "" means expect no pick
	}{
		{
			name: "first private ipv4 on an up non-loopback interface",
			ifaces: []Iface{
				{Up: true, Loopback: true, Addrs: []netip.Addr{addr("127.0.0.1")}},
				{Up: true, Loopback: false, Addrs: []netip.Addr{addr("192.168.1.20")}},
			},
			want: "192.168.1.20",
		},
		{
			name: "down interface is skipped even with a private addr",
			ifaces: []Iface{
				{Up: false, Loopback: false, Addrs: []netip.Addr{addr("10.0.0.5")}},
				{Up: true, Loopback: false, Addrs: []netip.Addr{addr("172.16.3.4")}},
			},
			want: "172.16.3.4",
		},
		{
			name: "public and link-local and ipv6 addresses are skipped",
			ifaces: []Iface{
				{Up: true, Loopback: false, Addrs: []netip.Addr{
					addr("8.8.8.8"),       // public
					addr("169.254.10.10"), // link-local
					addr("fe80::1"),       // ipv6 link-local
					addr("2001:db8::1"),   // ipv6
					addr("10.1.2.3"),      // private -> chosen
				}},
			},
			want: "10.1.2.3",
		},
		{
			name: "loopback interface never chosen",
			ifaces: []Iface{
				{Up: true, Loopback: true, Addrs: []netip.Addr{addr("127.0.0.1")}},
			},
			want: "",
		},
		{
			name:   "no interfaces yields no pick",
			ifaces: nil,
			want:   "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pickLANIPv4(tc.ifaces)
			if tc.want == "" {
				if ok {
					t.Fatalf("expected no pick, got %s", got)
				}
				return
			}
			if !ok {
				t.Fatalf("expected %s, got no pick", tc.want)
			}
			if got.String() != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// newTestManager returns a Manager that binds its share listener on loopback,
// so the token/listener lifecycle can be exercised without a real LAN. Extra
// options (e.g. WithTrailDir) pass straight through to NewManager.
func newTestManager(t *testing.T, parent context.Context, ttl time.Duration, opts ...Option) *Manager {
	t.Helper()
	m := NewManager(parent, ttl, nil, opts...)
	m.selectAddr = func() (netip.Addr, error) { return netip.MustParseAddr("127.0.0.1"), nil }
	return m
}

// consoleDirFixture writes a minimal built console (just index.html) to a temp
// dir so the static mount has something to serve.
func consoleDirFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>console</title>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	return dir
}

// okHandler is a stand-in for a guarded read route: it answers 200 with a body.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	_, _ = io.WriteString(w, "status-ok")
})

// get issues a GET to url with an optional bearer token and returns the status.
func get(t *testing.T, url, token string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// tokenFromURL pulls the #token= fragment out of a share URL.
func tokenFromURL(t *testing.T, share string) string {
	t.Helper()
	const marker = "#token="
	i := len(share)
	for j := 0; j+len(marker) <= len(share); j++ {
		if share[j:j+len(marker)] == marker {
			i = j + len(marker)
			break
		}
	}
	return share[i:]
}

func TestManagerServesGuardedRoutesWithToken(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newTestManager(t, ctx, time.Minute)
	consoleDir := consoleDirFixture(t)

	sess, err := m.Start(consoleDir, map[string]http.Handler{"/api/v1/status": okHandler})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if sess.Superseded {
		t.Fatalf("first share should not report Superseded")
	}
	token := tokenFromURL(t, sess.URL)
	base := "http://" + hostOfURL(t, sess.URL)

	// Static console is unauthenticated.
	if code := get(t, base+"/console/", ""); code != http.StatusOK {
		t.Fatalf("GET /console/ = %d, want 200", code)
	}
	// The guarded route requires the token.
	if code := get(t, base+"/api/v1/status", ""); code != http.StatusUnauthorized {
		t.Fatalf("GET /api/v1/status without token = %d, want 401", code)
	}
	if code := get(t, base+"/api/v1/status", token); code != http.StatusOK {
		t.Fatalf("GET /api/v1/status with token = %d, want 200", code)
	}
	// The share endpoint and /mcp are never mounted on the LAN listener.
	if code := get(t, base+"/mcp", token); code != http.StatusNotFound {
		t.Fatalf("GET /mcp on share listener = %d, want 404", code)
	}
	if code := get(t, base+"/api/v1/share", token); code != http.StatusNotFound {
		t.Fatalf("GET /api/v1/share on share listener = %d, want 404", code)
	}
	// The unguarded health/probe routes are a loopback-daemon concept and must NOT
	// exist on the remote LAN listener: it is off-machine, so an UP/DOWN probe there
	// would both leak the daemon's existence and answer to anyone on the network. The
	// share mux mounts only the console and the per-session token-guarded read routes.
	for _, route := range []string{"/livez", "/readyz", "/healthz"} {
		if code := get(t, base+route, token); code != http.StatusNotFound {
			t.Fatalf("GET %s on share listener = %d, want 404 (no health routes on the LAN listener)", route, code)
		}
	}
}

func TestManagerSupersedeRevokesOldToken(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newTestManager(t, ctx, time.Minute)
	consoleDir := consoleDirFixture(t)

	first, err := m.Start(consoleDir, map[string]http.Handler{"/api/v1/status": okHandler})
	if err != nil {
		t.Fatalf("Start first: %v", err)
	}
	oldToken := tokenFromURL(t, first.URL)

	second, err := m.Start(consoleDir, map[string]http.Handler{"/api/v1/status": okHandler})
	if err != nil {
		t.Fatalf("Start second: %v", err)
	}
	if !second.Superseded {
		t.Fatalf("second share should report Superseded")
	}
	newToken := tokenFromURL(t, second.URL)
	newBase := "http://" + hostOfURL(t, second.URL)

	// Give the superseded listener a beat to shut down.
	waitClosed(t, "http://"+hostOfURL(t, first.URL)+"/console/")

	// The OLD token is rejected on the NEW listener (bound 1:1 to the new token).
	if code := get(t, newBase+"/api/v1/status", oldToken); code != http.StatusUnauthorized {
		t.Fatalf("old token on new listener = %d, want 401", code)
	}
	// The NEW token works on the NEW listener.
	if code := get(t, newBase+"/api/v1/status", newToken); code != http.StatusOK {
		t.Fatalf("new token on new listener = %d, want 200", code)
	}
}

// TestCloseIfOnlyClosesMatchingFingerprint proves the revoke TOCTOU guard: CloseIf on a
// superseded fingerprint is a no-op that leaves the CURRENT share alive, and only CloseIf on
// the live fingerprint tears it down. This is what stops a revoke that read one fingerprint,
// then raced a supersede, from tearing down the wrong (newer) share.
func TestCloseIfOnlyClosesMatchingFingerprint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newTestManager(t, ctx, time.Minute)
	consoleDir := consoleDirFixture(t)

	if _, err := m.Start(consoleDir, map[string]http.Handler{"/api/v1/status": okHandler}); err != nil {
		t.Fatalf("Start first: %v", err)
	}
	first, ok := m.Active()
	if !ok {
		t.Fatalf("first share should be active")
	}

	// Supersede: the second Start replaces the first under the lock.
	second, err := m.Start(consoleDir, map[string]http.Handler{"/api/v1/status": okHandler})
	if err != nil {
		t.Fatalf("Start second: %v", err)
	}
	cur, ok := m.Active()
	if !ok {
		t.Fatalf("second share should be active")
	}
	newBase := "http://" + hostOfURL(t, second.URL)
	newToken := tokenFromURL(t, second.URL)

	// CloseIf on the OLD (superseded) fingerprint must not touch the live share.
	if m.CloseIf(first.Fingerprint) {
		t.Fatalf("CloseIf on a superseded fingerprint should report false")
	}
	if code := get(t, newBase+"/api/v1/status", newToken); code != http.StatusOK {
		t.Fatalf("current share should still serve after a mismatched CloseIf, got %d", code)
	}

	// CloseIf on the LIVE fingerprint tears it down.
	if !m.CloseIf(cur.Fingerprint) {
		t.Fatalf("CloseIf on the live fingerprint should report true")
	}
	waitClosed(t, newBase+"/console/")
	if _, ok := m.Active(); ok {
		t.Fatalf("no share should be active after CloseIf on the live fingerprint")
	}
}

func TestManagerCloseKillsListener(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newTestManager(t, ctx, time.Minute)
	consoleDir := consoleDirFixture(t)

	sess, err := m.Start(consoleDir, map[string]http.Handler{"/api/v1/status": okHandler})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	base := "http://" + hostOfURL(t, sess.URL)
	m.Close()
	waitClosed(t, base+"/console/")
}

func TestManagerTTLClosesListener(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A short TTL exercises the timeout teardown path.
	m := newTestManager(t, ctx, 150*time.Millisecond)
	consoleDir := consoleDirFixture(t)

	sess, err := m.Start(consoleDir, map[string]http.Handler{"/api/v1/status": okHandler})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	base := "http://" + hostOfURL(t, sess.URL)
	// Alive immediately.
	if code := get(t, base+"/console/", ""); code != http.StatusOK {
		t.Fatalf("GET /console/ pre-expiry = %d, want 200", code)
	}
	waitClosed(t, base+"/console/")
}

// getUA issues a GET with a bearer token and an explicit User-Agent, so the test
// can assert the recorded share-connect event carries the client's UA.
func getUA(t *testing.T, url, token, ua string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", ua)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// TestShareConnectRecordsOncePerDevice proves the first authenticated request from a
// remote device records exactly one "share link opened" activity event (carrying the
// UA and remote IP), and that a second request from the same device records none - so
// a page's many requests do not spam the trail.
func TestShareConnectRecordsOncePerDevice(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	trailDir := t.TempDir()
	m := newTestManager(t, ctx, time.Minute, WithTrailDir(trailDir))
	consoleDir := consoleDirFixture(t)

	sess, err := m.Start(consoleDir, map[string]http.Handler{"/api/v1/status": okHandler})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	token := tokenFromURL(t, sess.URL)
	base := "http://" + hostOfURL(t, sess.URL)
	const ua = "magus-test-agent/1.0"

	// First authenticated request records exactly one event. The disk append runs in a
	// goroutine (it must never block serving), so poll the trail rather than read once.
	if code := getUA(t, base+"/api/v1/status", token, ua); code != http.StatusOK {
		t.Fatalf("first GET = %d, want 200", code)
	}
	events := waitTrailCount(t, trailDir, 1)
	ev := events[0]
	if ev.Kind != trail.KindTokenLifecycle {
		t.Fatalf("event kind = %q, want %q", ev.Kind, trail.KindTokenLifecycle)
	}
	if ev.Outcome != trail.OutcomeOK {
		t.Fatalf("event outcome = %q, want %q", ev.Outcome, trail.OutcomeOK)
	}
	if ev.UserAgent != ua {
		t.Fatalf("event user_agent = %q, want %q", ev.UserAgent, ua)
	}
	if ev.Preview == "" {
		t.Fatalf("event preview should carry the remote IP, got empty")
	}

	// Second request from the same device (same host) records nothing new: the dedupe
	// decision is synchronous under the lock, so a duplicate never even spawns an append.
	if code := getUA(t, base+"/api/v1/status", token, ua); code != http.StatusOK {
		t.Fatalf("second GET = %d, want 200", code)
	}
	events = waitTrailCount(t, trailDir, 1)
	if len(events) != 1 {
		t.Fatalf("after second request from same device: got %d events, want still exactly 1", len(events))
	}
}

// waitTrailCount polls the trail until it holds exactly want events or a short deadline
// passes, returning the events. The "share link opened" append runs asynchronously, so a
// test must wait for it to land rather than read the trail the instant the request returns.
func waitTrailCount(t *testing.T, dir string, want int) []trail.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var events []trail.Event
	for time.Now().Before(deadline) {
		var err error
		events, err = trail.ReadRecent(dir, 10)
		if err != nil {
			t.Fatalf("ReadRecent: %v", err)
		}
		if len(events) == want {
			return events
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("trail never reached %d events; last saw %d", want, len(events))
	return nil
}

// hostOfURL extracts host:port from "http://host:port/console/#...".
func hostOfURL(t *testing.T, u string) string {
	t.Helper()
	const scheme = "http://"
	rest := u[len(scheme):]
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' {
			return rest[:i]
		}
	}
	return rest
}

// waitClosed polls url until the connection is refused, failing if it stays up.
func waitClosed(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return // connection refused: listener is down
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("listener at %s did not close", url)
}

// serveGuarded drives a sessionGuard.admit-wrapped handler with a synthetic
// RemoteAddr, returning the response status and body. It bypasses the real listener so
// a test can present arbitrary remote hosts - impossible over a single loopback IP -
// and thereby exercise the device-binding reject path end to end.
func serveGuarded(g *sessionGuard, remoteAddr string) (int, string) {
	h := g.admit(okHandler)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	r.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	res := w.Result()
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body)
}

// TestSessionGuardBindsFirstDeviceRejectsOthers proves the device-binding behavior at
// the guard boundary: the first remote host to present a valid token is served and
// binds the share; that same host keeps being served; a DIFFERENT host with the (by
// construction already-verified) token is rejected 403 with the bound-device body; and
// a fresh guard - what a supersede builds - starts unbound so a new first device binds.
func TestSessionGuardBindsFirstDeviceRejectsOthers(t *testing.T) {
	m := NewManager(context.Background(), time.Minute, nil)
	g := newSessionGuard(m)

	// First device (host A, varying ephemeral port) binds and is served.
	if code, _ := serveGuarded(g, "10.0.0.5:51000"); code != http.StatusOK {
		t.Fatalf("first device: got %d, want 200", code)
	}
	// Same device, new connection (different port) still served: binding is on host, not
	// on the per-connection port.
	if code, _ := serveGuarded(g, "10.0.0.5:51001"); code != http.StatusOK {
		t.Fatalf("bound device second request: got %d, want 200", code)
	}
	// A different device replaying the valid token is rejected with a clear 403 body.
	code, body := serveGuarded(g, "10.0.0.9:40000")
	if code != http.StatusForbidden {
		t.Fatalf("second device: got %d, want 403", code)
	}
	if got := strings.TrimSpace(body); got != shareBoundOtherDeviceMsg {
		t.Fatalf("403 body = %q, want %q", got, shareBoundOtherDeviceMsg)
	}
	// The bound device is unaffected by the rejected replay: still served.
	if code, _ := serveGuarded(g, "10.0.0.5:51002"); code != http.StatusOK {
		t.Fatalf("bound device after a rejected replay: got %d, want 200", code)
	}

	// Supersede builds a fresh guard: it starts unbound, so a brand-new first device
	// (even the one previously rejected) binds and is served.
	g2 := newSessionGuard(m)
	if code, _ := serveGuarded(g2, "10.0.0.9:40001"); code != http.StatusOK {
		t.Fatalf("new share, new first device: got %d, want 200", code)
	}
	// And the previously-bound host is now the "other" device on the new share.
	if code, _ := serveGuarded(g2, "10.0.0.5:51003"); code != http.StatusForbidden {
		t.Fatalf("old host on the new share: got %d, want 403", code)
	}
}

// TestBindDeviceRaceSingleWinner proves the first-device decision is race-safe: when
// many distinct hosts race the very first request, EXACTLY ONE wins the binding (is
// admitted as the first device); every other is rejected. Run under -race, the shared
// boundHost mutation is what the lock protects.
func TestBindDeviceRaceSingleWinner(t *testing.T) {
	m := NewManager(context.Background(), time.Minute, nil)
	g := newSessionGuard(m)

	const n = 64
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	results := make([]bool, n)
	for i := 0; i < n; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			host := netip.AddrFrom4([4]byte{10, 0, byte(i / 256), byte(i % 256)}).String()
			start.Wait() // release all goroutines at once to maximize the race
			results[i] = g.bindDevice(host)
		}(i)
	}
	start.Done()
	done.Wait()

	wins := 0
	for _, ok := range results {
		if ok {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("exactly one host must win the binding, got %d winners", wins)
	}
}
