package magus

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func b64Pub(t *testing.T) (pub string, seed string) {
	t.Helper()
	pk, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(pk), base64.StdEncoding.EncodeToString(priv.Seed())
}

// TestRemoteCacheRequiresTrustSet is the paranoid root: a wired remote backend
// with no declared trust set must be a hard error, not a silent unverified cache.
func TestRemoteCacheRequiresTrustSet(t *testing.T) {
	if _, err := remoteCacheSigningOpts(nil); err == nil {
		t.Fatal("empty trust set was accepted; a remote cache must require trusted_keys")
	}
	if _, err := remoteCacheSigningOpts([]string{}); err == nil {
		t.Fatal("empty trust-set slice was accepted")
	}
}

// TestRemoteCacheTrustSetDecodes: a valid trust set yields verification options;
// adding a signing-key env var yields a signing option too.
func TestRemoteCacheTrustSetDecodes(t *testing.T) {
	pub, seed := b64Pub(t)

	opts, err := remoteCacheSigningOpts([]string{pub})
	if err != nil {
		t.Fatalf("valid trust set rejected: %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("verify-only: got %d opts, want 1 (trusted keys only)", len(opts))
	}

	t.Setenv(signingKeyEnv, seed)
	opts, err = remoteCacheSigningOpts([]string{pub})
	if err != nil {
		t.Fatalf("valid trust set + signing key rejected: %v", err)
	}
	if len(opts) != 2 {
		t.Fatalf("signing: got %d opts, want 2 (trusted keys + signing key)", len(opts))
	}
}

// TestRemoteCacheRejectsMalformedKeys: bad base64 in either the trust set or the
// signing-key env var is a clear configuration error, not a silent fallback.
func TestRemoteCacheRejectsMalformedKeys(t *testing.T) {
	if _, err := remoteCacheSigningOpts([]string{"not!base64!"}); err == nil {
		t.Error("malformed trusted key was accepted")
	}
	pub, _ := b64Pub(t)
	t.Setenv(signingKeyEnv, "not!base64!")
	if _, err := remoteCacheSigningOpts([]string{pub}); err == nil {
		t.Error("malformed signing key was accepted")
	}
}
