package difftui

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/types"
)

// Tokens is the shell's token store as a review call uses it: the operator mints and revokes,
// and the token a call carries is a console one.
type Tokens interface {
	Mint(req auth.MintRequest) (secret string, rec auth.Token, err error)
	Revoke(id string) error
}

// SetBearer is where a review's console token leaves for the server's API.
func SetBearer(req *http.Request, token types.Secret) {
	req.Header.Set("Authorization", "Bearer "+token.Reveal())
}

// bridgeTokenTTL covers a reader who leaves a review open. A crash
// leaves the record until then, and its secret died with the process.
const bridgeTokenTTL = 7 * 24 * time.Hour

// CallTokenTTL covers one request to a review route.
const CallTokenTTL = time.Minute

// MintToken mints the console token a CLI call to the review routes
// carries: they refuse the operator token. ok is false with no server at
// addr, which writes no record, or on a failed mint.
// The caller revokes tokenID.
func MintToken(ctx context.Context, tk Tokens, addr string, ttl time.Duration) (token types.Secret, tokenID string, ok bool) {
	warn := func(msg string, why ...any) {
		slog.WarnContext(ctx, msg, append([]any{attr.Notice(""), attr.Component("magus")}, why...)...)
	}
	probe := net.Dialer{Timeout: bridgeWrite}
	conn, err := probe.DialContext(ctx, "tcp", addr)
	if err != nil {
		if !errors.Is(err, syscall.ECONNREFUSED) {
			warn("the review is not shared with the server: it did not answer", attr.Error(err))
		}
		return types.Secret{}, "", false
	}
	_ = conn.Close()
	secret, rec, err := tk.Mint(auth.MintRequest{Grant: types.GrantConsole, TTL: ttl})
	if err != nil {
		warn("the review is not shared with the server: its console token could not be minted", attr.Error(err))
		return types.Secret{}, "", false
	}
	return types.NewSecret(secret), rec.ID, true
}

// RevokeToken deletes the record MintToken wrote.
// A record already gone, expired or revoked by hand, is what it wanted.
func RevokeToken(ctx context.Context, tk Tokens, tokenID string) {
	if err := tk.Revoke(tokenID); err != nil && !errors.Is(err, auth.ErrTokenNotFound) {
		slog.WarnContext(ctx, "a review's console token was not revoked; it expires on its own",
			attr.Notice(""), attr.Component("magus"), attr.Why(tokenID), attr.Error(err))
	}
}
