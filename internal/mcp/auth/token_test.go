package auth

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate points os.UserConfigDir at a temp dir so tests never touch the real
// user config. On Linux os.UserConfigDir honours XDG_CONFIG_HOME.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

func TestRoundTrip(t *testing.T) {
	isolate(t)

	if _, err := Load(); err != ErrNoToken {
		t.Fatalf("Load on empty: got %v, want ErrNoToken", err)
	}
	if ok, err := Exists(); err != nil || ok {
		t.Fatalf("Exists on empty: got (%v, %v), want (false, nil)", ok, err)
	}

	tok, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if tok == "" {
		t.Fatal("Generate returned empty token")
	}

	path, err := Save(tok)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat saved token: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token perms = %#o, want 0600", perm)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != tok {
		t.Errorf("Load = %q, want %q", got, tok)
	}

	if ok, err := Exists(); err != nil || !ok {
		t.Fatalf("Exists after Save: got (%v, %v), want (true, nil)", ok, err)
	}
}

func TestGenerateUnique(t *testing.T) {
	a, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if a == b {
		t.Error("Generate produced identical tokens")
	}
}

func TestLoadRejectsInsecurePerms(t *testing.T) {
	isolate(t)

	tok, _ := Generate()
	path, err := Save(tok)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if _, err := Load(); err == nil {
		t.Error("Load accepted a world-readable token file; want error")
	}
}

func TestRevoke(t *testing.T) {
	isolate(t)

	// Revoke with no token is a no-op.
	if err := Revoke(); err != nil {
		t.Fatalf("Revoke on empty: %v", err)
	}

	tok, _ := Generate()
	if _, err := Save(tok); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := Revoke(); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := Load(); err != ErrNoToken {
		t.Errorf("Load after Revoke: got %v, want ErrNoToken", err)
	}
}

func TestPathLocation(t *testing.T) {
	dir := isolate(t)
	path, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want := filepath.Join(dir, "magus", "mcp_token")
	if path != want {
		t.Errorf("Path = %q, want %q", path, want)
	}
}

func TestFingerprintStable(t *testing.T) {
	const tok = "abc"
	if Fingerprint(tok) != Fingerprint(tok) {
		t.Error("Fingerprint not deterministic")
	}
	if Fingerprint("abc") == Fingerprint("abd") {
		t.Error("Fingerprint collision on distinct tokens")
	}
	if len(Fingerprint(tok)) != 8 {
		t.Errorf("Fingerprint len = %d, want 8", len(Fingerprint(tok)))
	}
}

// reqStatus drives one request through h with the given Authorization header
// (empty = none) and returns the status code.
func reqStatus(h http.Handler, authHeader string) int {
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}

// TestGuardHotReload proves Guard(Load, ...) picks up a rotated or revoked
// token without rebuilding the handler — the daemon's hot-reload contract.
func TestGuardHotReload(t *testing.T) {
	isolate(t)

	a, _ := Generate()
	if _, err := SaveNew(a); err != nil {
		t.Fatalf("SaveNew: %v", err)
	}
	h := Guard(Load, okHandler)

	if code := reqStatus(h, "Bearer "+a); code != http.StatusOK {
		t.Errorf("token A: got %d, want 200", code)
	}

	// Rotate to B without touching the handler.
	b, _ := Generate()
	if _, err := Save(b); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if code := reqStatus(h, "Bearer "+a); code != http.StatusUnauthorized {
		t.Errorf("old token A after rotate: got %d, want 401", code)
	}
	if code := reqStatus(h, "Bearer "+b); code != http.StatusOK {
		t.Errorf("new token B: got %d, want 200", code)
	}

	// Revoke fails closed.
	if err := Revoke(); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if code := reqStatus(h, "Bearer "+b); code != http.StatusUnauthorized {
		t.Errorf("after revoke: got %d, want 401", code)
	}
}

// TestResolveGeneratesWithoutLoggingSecret pins the security-critical contract:
// Resolve provisions and persists a token on first use but never writes the
// secret to the logger (which commonly lands in journald/nohup.out).
func TestResolveGeneratesWithoutLoggingSecret(t *testing.T) {
	isolate(t)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	tok, err := Resolve(log)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if tok == "" {
		t.Fatal("Resolve returned empty token")
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load after Resolve: %v", err)
	}
	if got != tok {
		t.Errorf("persisted token = %q, want %q", got, tok)
	}

	if strings.Contains(buf.String(), tok) {
		t.Errorf("Resolve leaked the secret into the log: %q", buf.String())
	}
}

// TestResolveIdempotent confirms a second Resolve returns the already-persisted
// token instead of minting a new one.
func TestResolveIdempotent(t *testing.T) {
	isolate(t)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := Resolve(log)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	b, err := Resolve(log)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if a != b {
		t.Errorf("Resolve not idempotent: %q vs %q", a, b)
	}
}

// TestSaveNewRefusesOverwrite confirms the create-only write fails closed (with
// os.ErrExist) rather than clobbering an existing token.
func TestSaveNewRefusesOverwrite(t *testing.T) {
	isolate(t)

	first, _ := Generate()
	if _, err := SaveNew(first); err != nil {
		t.Fatalf("SaveNew first: %v", err)
	}
	if _, err := SaveNew("other"); !errors.Is(err, os.ErrExist) {
		t.Errorf("SaveNew over existing: got %v, want os.ErrExist", err)
	}
	got, _ := Load()
	if got != first {
		t.Errorf("token clobbered: got %q, want %q", got, first)
	}
}
