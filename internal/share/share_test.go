package share

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
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
// so the token/listener lifecycle can be exercised without a real LAN.
func newTestManager(t *testing.T, parent context.Context, ttl time.Duration) *Manager {
	t.Helper()
	m := NewManager(parent, ttl, nil)
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
