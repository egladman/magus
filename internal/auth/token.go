package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/secrets"
	"github.com/egladman/magus/types"
)

// ErrNoToken is returned by LoadOperator when no operator token file exists yet.
var ErrNoToken = errors.New("auth: no token configured")

// OperatorPath returns the operator token file: <xdg.StateDir>/magus/mcp_token. It lives in
// the state dir, not the config dir, because config may be shared or committed and a secret
// must not ride along.
func OperatorPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mcp_token"), nil
}

// GenerateOperator returns a fresh mgo_ operator token without persisting it; pass it to
// SaveOperator or SaveNewOperator.
func GenerateOperator() (secrets.Secret, error) {
	tok, err := mintSecret(KindOperator)
	return secrets.New(tok), err
}

// SaveOperator writes token as the operator token, replacing any existing one atomically, at
// 0600. It returns the path written.
func SaveOperator(token secrets.Secret) (string, error) {
	path, err := OperatorPath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	// 0700 here, since WriteFileAtomic would create a missing directory 0755.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("auth: create %s: %w", dir, err)
	}
	// Synced: a crash that left the file empty would stop the server starting until the
	// token is reissued.
	if err := file.WriteFileAtomic(path, []byte(token.Reveal()+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("auth: install token: %w", err)
	}
	return path, nil
}

// SaveNewOperator writes token only if no operator token file exists yet. The O_EXCL create is
// the whole decision, so a CLI `generate` racing the server's first start cannot clobber the
// token the other is serving; the loser gets an error satisfying errors.Is(err, os.ErrExist).
func SaveNewOperator(token secrets.Secret) (string, error) {
	path, err := OperatorPath()
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
	if _, err := f.WriteString(token.Reveal() + "\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("auth: write token: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("auth: close token: %w", err)
	}
	return path, nil
}

// LoadOperator reads the operator token. It returns ErrNoToken when the file does not exist,
// an InsecureTokenPermissions error for a file looser than 0600, and an OperatorTokenFormat
// error for a file that does not hold a well-formed mgo_ token, which is what a file written
// before the kind prefix holds. There is no migration: the error names the command that
// re-issues it.
func LoadOperator() (secrets.Secret, error) {
	path, err := OperatorPath()
	if err != nil {
		return secrets.Secret{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return secrets.Secret{}, ErrNoToken
		}
		return secrets.Secret{}, fmt.Errorf("auth: stat token: %w", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return secrets.Secret{}, types.DiagnosticErrorf(types.InsecureTokenPermissions, "auth: token file %s has insecure permissions %#o (want 0600); fix with: chmod 600 %s", path, perm, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return secrets.Secret{}, fmt.Errorf("auth: read token: %w", err)
	}
	tok := strings.TrimSpace(string(raw))
	if kind, ok := credentialKind(tok); !ok || kind != KindOperator {
		return secrets.Secret{}, types.DiagnosticErrorf(types.OperatorTokenFormat,
			"auth: the operator token at %s is not an mgo_ token (it predates the kind prefix); re-issue it with `%s`", path, hint.MCPTokenGenerate.With("--force"))
	}
	return secrets.New(tok), nil
}

// RevokeOperator deletes the operator token file. It is not an error if none exists.
func RevokeOperator() error {
	path, err := OperatorPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("auth: revoke token: %w", err)
	}
	return nil
}

// SecretID is [TokenID] of a token magus holds, so a caller can name it without revealing it.
func SecretID(token secrets.Secret) string { return TokenID(token.Reveal()) }

// EnsureOperator loads the operator token, minting and persisting one when none exists. The
// server calls it before serving and fails closed on its error, so an old-format file stops
// the server with the OperatorTokenFormat error rather than serving without an operator.
//
// The secret is never logged: the server log lands in journald and nohup.out. Only the path
// is.
func EnsureOperator(ctx context.Context, log *slog.Logger) (secrets.Secret, error) {
	tok, err := LoadOperator()
	if err == nil {
		return tok, nil
	}
	if !errors.Is(err, ErrNoToken) {
		return secrets.Secret{}, err
	}
	tok, err = GenerateOperator()
	if err != nil {
		return secrets.Secret{}, err
	}
	path, err := SaveNewOperator(tok)
	if err != nil {
		// A racing `magus config token generate` won the create; serve its token.
		if errors.Is(err, os.ErrExist) {
			return LoadOperator()
		}
		return secrets.Secret{}, err
	}
	if log == nil {
		log = slog.Default()
	}
	log.WarnContext(ctx, "generated a new operator token; retrieve it with `magus config token print`",
		slog.String("path", path),
	)
	return tok, nil
}
