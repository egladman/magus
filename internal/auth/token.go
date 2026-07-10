// Package auth manages the shared-secret bearer token that guards the magus
// MCP HTTP endpoint and provides the HTTP middleware that enforces it. The CLI
// (`magus config mcp token ...`) and the MCP server resolve and read the exact
// same token file and share one implementation.
//
// The token is a 256-bit random secret, base64url-encoded, stored 0600 in the
// user config dir. It is a local shared secret — equivalent in sensitivity to
// the workspace it grants access to — not an OAuth credential. See the MCP
// authorization spec: stdio transports derive trust from the process, HTTP
// transports must authenticate. magus's loopback HTTP server uses this token
// as defense-in-depth on top of the 127.0.0.1 bind and Host/Origin guard.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/config"
)

// ErrNoToken is returned by Load when no token file exists yet.
var ErrNoToken = errors.New("mcpauth: no token configured")

// tokenBytes is the size of the raw random secret before encoding.
const tokenBytes = 32

// Path returns the absolute path to the MCP token file:
// <os.UserConfigDir>/magus/mcp_token. Both the CLI and the daemon resolve it
// this way so they always agree on the location.
func Path() (string, error) {
	dir, err := config.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("mcpauth: locate config dir: %w", err)
	}
	return filepath.Join(dir, "magus", "mcp_token"), nil
}

// Generate returns a fresh base64url-encoded 256-bit token. It does not persist
// anything; callers pass the result to Save.
func Generate() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("mcpauth: read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Save writes token to the token file with 0600 permissions, creating the
// parent directory if needed. The write is atomic (temp file + rename) so a
// concurrent reader never observes a half-written secret. It returns the path
// written.
func Save(token string) (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mcpauth: create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".mcp_token-*")
	if err != nil {
		return "", fmt.Errorf("mcpauth: create temp: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we bail before the rename.
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("mcpauth: chmod temp: %w", err)
	}
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("mcpauth: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("mcpauth: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", fmt.Errorf("mcpauth: install token: %w", err)
	}
	return path, nil
}

// SaveNew writes token only if no token file exists yet, using O_EXCL so the
// create-or-fail decision is atomic. It returns a path on success and an error
// satisfying errors.Is(err, os.ErrExist) if a token is already present — this
// closes the check-then-act race between a CLI `generate` and the daemon's
// auto-provision, so neither can silently clobber a token the other is serving.
func SaveNew(token string) (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mcpauth: create %s: %w", dir, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		// Returned unwrapped so callers can test errors.Is(err, os.ErrExist).
		return "", err
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("mcpauth: write token: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("mcpauth: close token: %w", err)
	}
	return path, nil
}

// Load reads and returns the token. It returns ErrNoToken if the file does not
// exist. As a guard against an accidentally world/group-readable secret, Load
// refuses a file whose permissions are looser than 0600.
func Load() (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNoToken
		}
		return "", fmt.Errorf("mcpauth: stat token: %w", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return "", fmt.Errorf("mcpauth: token file %s has insecure permissions %#o (want 0600); fix with: chmod 600 %s", path, perm, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("mcpauth: read token: %w", err)
	}
	tok := strings.TrimSpace(string(raw))
	if tok == "" {
		return "", fmt.Errorf("mcpauth: token file %s is empty", path)
	}
	return tok, nil
}

// Exists reports whether a token file is present.
func Exists() (bool, error) {
	path, err := Path()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("mcpauth: stat token: %w", err)
	}
	return true, nil
}

// Revoke deletes the token file. It is not an error if no token exists.
func Revoke() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("mcpauth: revoke token: %w", err)
	}
	return nil
}

// Fingerprint returns a short, non-reversible identifier for a token (the first
// 8 hex chars of its SHA-256) suitable for display in status output without
// revealing the secret.
func Fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:8]
}
