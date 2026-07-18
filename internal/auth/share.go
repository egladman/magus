package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"time"
)

// Share tokens are the THIRD auth tier: a single, short-lived, READ-ONLY secret
// minted for the "share to phone" feature. Unlike the retrievable cli token
// (token.go) and the persisted connector tokens (connector.go), a share token
// lives only in the running daemon's memory and only for as long as the
// ephemeral LAN listener it guards - it is never written to disk. It reuses the
// mgs_ wire format so a leak scanner still catches it, but carries a distinct
// read-only SCOPE that VerifyBearer (which guards /mcp and every mutating
// console route) never consults: a leaked share link can therefore only ever
// reach the read surface it was minted for, and only for its brief lifetime.
//
// The scope is enforced structurally, not by convention: the loopback daemon
// verifier (VerifyBearer) matches the cli token and the connector store and
// NOTHING else, so a share token is rejected there; the LAN listener builds its
// bearer guard from ONE ShareToken.Verify closure, which additionally requires
// the read scope and rejects an expired token. The two verifiers never overlap.

// ShareScopeRead is the only scope a share token ever carries: read-only access
// to the console's read surface. It exists as an explicit, checkable field (not
// an implicit assumption) so ShareToken.Verify can reject anything else and a
// future scope cannot silently widen an existing token's reach.
const ShareScopeRead = "read"

// ShareToken is one minted share credential, held in daemon memory only. It
// stores the hash of the secret (never the secret itself), its scope, and its
// expiry. The zero value verifies nothing.
type ShareToken struct {
	SHA256  string    // hex SHA-256 of the full mgs_ secret
	Scope   string    // always ShareScopeRead
	Expires time.Time // UTC; a share token ALWAYS expires (no zero-means-never here)
}

// MintShareToken returns a fresh read-only share token that expires ttl from
// now, alongside the plaintext secret shown ONCE (embedded in the QR URL). The
// secret is in the mgs_ connector wire format so secret scanners catch a leak,
// but its scope keeps it off every mutating surface. ttl must be positive.
func MintShareToken(ttl time.Duration) (secret string, tok ShareToken, err error) {
	if ttl <= 0 {
		return "", ShareToken{}, fmt.Errorf("auth: share token ttl must be positive, got %s", ttl)
	}
	secret, err = mintToken()
	if err != nil {
		return "", ShareToken{}, err
	}
	sum := sha256.Sum256([]byte(secret))
	tok = ShareToken{
		SHA256:  hex.EncodeToString(sum[:]),
		Scope:   ShareScopeRead,
		Expires: time.Now().Add(ttl).UTC(),
	}
	return secret, tok, nil
}

// Expired reports whether the token is past its expiry as of now.
func (t ShareToken) Expired(now time.Time) bool {
	return now.After(t.Expires)
}

// Verify reports whether presented is exactly this share token, unexpired, and
// read-scoped. It rejects a malformed or checksum-failing token OFFLINE before
// any hash work, then compares SHA-256 digests with subtle.ConstantTimeCompare
// so the check reveals neither the secret's bytes nor its length. The scope gate
// is what makes "read-only" a property the verifier enforces rather than a label:
// a token whose scope is not ShareScopeRead never matches, even if its bytes do.
// A zero-value ShareToken (empty SHA256) verifies nothing.
func (t ShareToken) Verify(presented string, now time.Time) bool {
	if t.Scope != ShareScopeRead || t.SHA256 == "" {
		return false
	}
	if t.Expired(now) {
		return false
	}
	if !validTokenFormat(presented) {
		return false
	}
	sum := sha256.Sum256([]byte(presented))
	got := []byte(hex.EncodeToString(sum[:]))
	return subtle.ConstantTimeCompare([]byte(t.SHA256), got) == 1
}
