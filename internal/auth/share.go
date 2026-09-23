package auth

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/egladman/magus/types"
)

// A share token (mgl_) is the secret behind a share link: it lives only in the running
// daemon's memory, only as long as the LAN listener it guards, and only that listener's
// verifier accepts it. [Verify], the loopback verifier, refuses the class outright, and the
// share verifier accepts nothing else, so the operator token never crosses the LAN.

// Share lifetimes. A share link is a bearer credential served over plaintext HTTP on the LAN
// and bound to a device by source IP alone, so it lives a day at most. A request outside
// [MinShareTTL, MaxShareTTL] is an error, never clamped.
const (
	DefaultShareTTL = 15 * time.Minute
	MinShareTTL     = time.Minute
	MaxShareTTL     = 24 * time.Hour
)

// ErrShareLifetime is a share requested for less than MinShareTTL or more than MaxShareTTL.
var ErrShareLifetime = errors.New("auth: a share link lives between 1 minute and 24 hours")

// ShareToken is one minted share token, held in daemon memory only: the hash of the secret,
// never the secret, and its expiry. The zero value verifies nothing.
type ShareToken struct {
	SHA256  string
	Expires time.Time
}

// MintShare mints a share token that expires ttl from now and returns its secret once, for
// the link. It refuses a minter whose grant does not include [types.GrantShare]
// (ErrExceedsGrant) and a ttl outside [MinShareTTL, MaxShareTTL] (ErrShareLifetime).
func MintShare(minter types.Grant, ttl time.Duration) (secret string, tok ShareToken, err error) {
	if !types.GrantShare.Within(minter) {
		return "", ShareToken{}, fmt.Errorf("%w: a share link needs %s, the minter holds %q", ErrExceedsGrant, types.GrantShare, minter.String())
	}
	if ttl < MinShareTTL || ttl > MaxShareTTL {
		return "", ShareToken{}, fmt.Errorf("%w: asked for %s", ErrShareLifetime, ttl)
	}
	secret, err = mintSecret(types.ClassShare)
	if err != nil {
		return "", ShareToken{}, err
	}
	return secret, ShareToken{SHA256: digest(secret), Expires: time.Now().Add(ttl).UTC()}, nil
}

// Expired reports whether now is past the token's expiry.
func (t ShareToken) Expired(now time.Time) bool { return now.After(t.Expires) }

// ID is the token's stable id, the first 8 hex of its hash; "" for the zero token.
func (t ShareToken) ID() string {
	if len(t.SHA256) < 8 {
		return ""
	}
	return t.SHA256[:8]
}

// Credential is the credential this token verifies as.
func (t ShareToken) Credential() types.Credential {
	return types.Credential{Class: types.ClassShare, ID: t.ID(), Grant: types.GrantShare}
}

// Verify reports whether presented is exactly this token and unexpired at now. Anything that
// is not a well-formed mgl_ token fails before any hashing, so an operator or stored token
// never passes here even if the hashes matched. The digests are compared in constant time.
func (t ShareToken) Verify(presented string, now time.Time) bool {
	if t.SHA256 == "" || t.Expired(now) {
		return false
	}
	if class, ok := Class(presented); !ok || class != types.ClassShare {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(t.SHA256), []byte(digest(presented))) == 1
}
