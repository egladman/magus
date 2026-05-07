package auth

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

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
