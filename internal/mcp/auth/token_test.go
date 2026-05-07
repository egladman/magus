package auth

import (
	"os"
	"path/filepath"
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
