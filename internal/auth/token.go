// Package auth holds the daemon's bearer credentials and decides which credential a
// presented token is. It authenticates; it never authorizes. Whether a credential may use a
// route is its [types.Grant] against the route's [types.Need], decided in one place
// (internal/httpx's bearer guard).
//
// Three classes, told apart by the token's prefix (see format.go):
//
//	mgo_ operator  one retrievable file per user, every surface on loopback, never expires
//	mgs_ token     stored hashed in tokens.d, a grant within its minter's, always expires
//	mgl_ share     daemon memory only, console=read, the share link's LAN listener only
//
// The CLI (`magus config token ...`) and the daemon read the same files through this one
// implementation.
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// ErrNoToken is returned by Load when no operator token file exists yet.
var ErrNoToken = errors.New("auth: no token configured")

// Path returns the operator token file: <UserStateDir>/magus/mcp_token. It lives in the state
// dir, not the config dir, because config may be shared or committed and a secret must not
// ride along. The file name predates the operator class; it is not something anyone types, so
// it was not worth a rename.
func Path() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mcp_token"), nil
}

// Generate returns a fresh mgo_ operator token without persisting it; pass it to Save or
// SaveNew.
func Generate() (string, error) { return mintSecret(types.ClassOperator) }

// Save writes token as the operator token, replacing any existing one atomically, at 0600.
// It returns the path written.
func Save(token string) (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	if err := atomicWriteSecret(path, []byte(token+"\n")); err != nil {
		return "", err
	}
	return path, nil
}

// atomicWriteSecret writes data to path at 0600 via a temp file and rename, so a concurrent
// reader never observes a half-written secret. It creates the parent dir at 0700.
func atomicWriteSecret(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("auth: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".secret-*")
	if err != nil {
		return fmt.Errorf("auth: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("auth: chmod temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("auth: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("auth: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("auth: install secret: %w", err)
	}
	return nil
}

// SaveNew writes token only if no operator token file exists yet. The O_EXCL create is the
// whole decision, so a CLI `generate` racing the daemon's first start cannot clobber the
// token the other is serving; the loser gets an error satisfying errors.Is(err, os.ErrExist).
func SaveNew(token string) (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("auth: create %s: %w", dir, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("auth: write token: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("auth: close token: %w", err)
	}
	return path, nil
}

// Load reads the operator token. It returns ErrNoToken when the file does not exist, an
// InsecureTokenPermissions error for a file looser than 0600, and an OperatorTokenFormat
// error for a file that does not hold a well-formed mgo_ token, which is what a file written
// before the class prefix holds. There is no migration: the error names the command that
// re-issues it.
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
		return "", fmt.Errorf("auth: stat token: %w", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return "", types.DiagnosticErrorf(types.InsecureTokenPermissions, "auth: token file %s has insecure permissions %#o (want 0600); fix with: chmod 600 %s", path, perm, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("auth: read token: %w", err)
	}
	tok := strings.TrimSpace(string(raw))
	if class, ok := Class(tok); !ok || class != types.ClassOperator {
		return "", types.DiagnosticErrorf(types.OperatorTokenFormat,
			"auth: the operator token at %s is not an mgo_ token (it predates the class prefix); re-issue it with `%s`", path, hint.MCPTokenGenerate.With("--force"))
	}
	return tok, nil
}

// Revoke deletes the operator token file. It is not an error if none exists.
func Revoke() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("auth: revoke token: %w", err)
	}
	return nil
}

// OperatorCredential is the credential the operator token verifies as.
func OperatorCredential(token string) types.Credential {
	return types.Credential{Class: types.ClassOperator, ID: Fingerprint(token), Grant: types.GrantOperator}
}

// EnsureOperator loads the operator token, minting and persisting one when none exists. The
// daemon calls it before serving and fails closed on its error, so an old-format file stops
// the daemon with the OperatorTokenFormat error rather than serving without an operator.
//
// The secret is never logged: the daemon log lands in journald and nohup.out. Only the path
// is.
func EnsureOperator(ctx context.Context, log *slog.Logger) (string, error) {
	tok, err := Load()
	if err == nil {
		return tok, nil
	}
	if !errors.Is(err, ErrNoToken) {
		return "", err
	}
	tok, err = Generate()
	if err != nil {
		return "", err
	}
	path, err := SaveNew(tok)
	if err != nil {
		// A racing `magus config token generate` won the create; serve its token.
		if errors.Is(err, os.ErrExist) {
			return Load()
		}
		return "", err
	}
	if log == nil {
		log = slog.Default()
	}
	log.WarnContext(ctx, "[AGENT] generated a new operator token; retrieve it with `magus config token print`",
		slog.String("path", path),
	)
	return tok, nil
}
