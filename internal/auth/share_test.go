package auth

import (
	"testing"
	"time"
)

func TestMintShareTokenVerify(t *testing.T) {
	secret, tok, err := MintShareToken(15 * time.Minute)
	if err != nil {
		t.Fatalf("MintShareToken: %v", err)
	}
	if !validTokenFormat(secret) {
		t.Fatalf("minted secret %q is not a valid mgs_ token", secret)
	}
	if tok.Scope != ShareScopeRead {
		t.Fatalf("scope = %q, want %q", tok.Scope, ShareScopeRead)
	}
	now := time.Now()
	if !tok.Verify(secret, now) {
		t.Fatalf("Verify rejected the freshly minted secret")
	}

	// A different, independently valid share token must not verify against tok.
	other, _, err := MintShareToken(15 * time.Minute)
	if err != nil {
		t.Fatalf("MintShareToken (other): %v", err)
	}
	if tok.Verify(other, now) {
		t.Fatalf("Verify accepted a different share token")
	}

	// A garbage / non-mgs_ token is rejected offline.
	if tok.Verify("not-a-token", now) {
		t.Fatalf("Verify accepted a malformed token")
	}
}

func TestShareTokenExpiry(t *testing.T) {
	secret, tok, err := MintShareToken(time.Minute)
	if err != nil {
		t.Fatalf("MintShareToken: %v", err)
	}
	// Just before expiry it verifies; just after, it does not.
	if !tok.Verify(secret, tok.Expires.Add(-time.Second)) {
		t.Fatalf("Verify rejected an unexpired token")
	}
	if tok.Verify(secret, tok.Expires.Add(time.Second)) {
		t.Fatalf("Verify accepted an expired token")
	}
}

func TestShareTokenWrongScopeRejected(t *testing.T) {
	secret, tok, err := MintShareToken(time.Minute)
	if err != nil {
		t.Fatalf("MintShareToken: %v", err)
	}
	// A token whose scope is not read verifies nothing, even with the right bytes.
	tok.Scope = "write"
	if tok.Verify(secret, time.Now()) {
		t.Fatalf("Verify accepted a non-read-scoped token")
	}
	// The zero value verifies nothing.
	var zero ShareToken
	if zero.Verify(secret, time.Now()) {
		t.Fatalf("zero-value ShareToken verified a token")
	}
}

// TestVerifyBearerRejectsShareToken is the load-bearing separation test: the
// daemon's own verifier (which guards /mcp and every mutating console route)
// must NEVER accept a share token, even with a real cli token installed on disk.
func TestVerifyBearerRejectsShareToken(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	// Install a real cli token so VerifyBearer has something legitimate to accept:
	// the point is that it accepts THAT and still rejects the share token.
	cli, err := Generate()
	if err != nil {
		t.Fatalf("Generate cli token: %v", err)
	}
	if _, err := Save(cli); err != nil {
		t.Fatalf("Save cli token: %v", err)
	}
	if !VerifyBearer(cli) {
		t.Fatalf("VerifyBearer rejected the cli token")
	}

	shareSecret, _, err := MintShareToken(time.Minute)
	if err != nil {
		t.Fatalf("MintShareToken: %v", err)
	}
	if VerifyBearer(shareSecret) {
		t.Fatalf("VerifyBearer accepted a share token; the read scope must never reach /mcp or a mutating route")
	}
}
