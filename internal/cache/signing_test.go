package cache

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/egladman/magus/internal/codec"
)

func mustKeypair(t *testing.T) (ed25519.PublicKey, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv.Seed()
}

// TestSignVerifyRoundTrip is the happy path: a manifest signed by a key in the
// trust set verifies.
func TestSignVerifyRoundTrip(t *testing.T) {
	pub, seed := mustKeypair(t)
	s, err := newSigner(seed)
	if err != nil {
		t.Fatalf("newSigner: %v", err)
	}
	v, err := newVerifier([][]byte{pub})
	if err != nil {
		t.Fatalf("newVerifier: %v", err)
	}

	manifest := []byte(`{"projectPath":"test/pkg","hash":"abc123","outputs":[]}`)
	sig, err := s.sign(manifest)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := v.verify(sig, manifest); err != nil {
		t.Fatalf("verify valid signature: %v", err)
	}
	if keyID(pub) != s.keyid {
		t.Fatalf("signer keyid %q != derived %q", s.keyid, keyID(pub))
	}
}

// TestVerifyRejectsUntrustedKey: a signature from a key absent from the trust set
// must fail, even though it is itself valid.
func TestVerifyRejectsUntrustedKey(t *testing.T) {
	_, seedA := mustKeypair(t)
	pubB, _ := mustKeypair(t)
	s, _ := newSigner(seedA)
	v, _ := newVerifier([][]byte{pubB}) // trusts B, not A

	manifest := []byte(`{"hash":"x"}`)
	sig, _ := s.sign(manifest)
	if err := v.verify(sig, manifest); err == nil {
		t.Fatal("verify accepted a signature from an untrusted key")
	}
}

// TestVerifyRejectsTamperedManifest: the bytes presented at verify time must be
// the bytes that were signed; a single flipped byte fails.
func TestVerifyRejectsTamperedManifest(t *testing.T) {
	pub, seed := mustKeypair(t)
	s, _ := newSigner(seed)
	v, _ := newVerifier([][]byte{pub})

	manifest := []byte(`{"hash":"original"}`)
	sig, _ := s.sign(manifest)
	tampered := []byte(`{"hash":"poisoned"}`)
	if err := v.verify(sig, tampered); err == nil {
		t.Fatal("verify accepted a signature over different manifest bytes")
	}
}

// TestVerifyRejectsBadAlg: only ed25519 envelopes are accepted.
func TestVerifyRejectsBadAlg(t *testing.T) {
	pub, _ := mustKeypair(t)
	v, _ := newVerifier([][]byte{pub})
	env, _ := codec.Marshal(sigEnvelope{Alg: "rsa", KeyID: keyID(pub)})
	if err := v.verify(env, []byte("m")); err == nil {
		t.Fatal("verify accepted a non-ed25519 algorithm")
	}
}

// TestKeyMaterialValidation: malformed key material is rejected at construction.
func TestKeyMaterialValidation(t *testing.T) {
	if _, err := newSigner(make([]byte, 16)); err == nil {
		t.Error("newSigner accepted a 16-byte seed")
	}
	if _, err := newVerifier([][]byte{make([]byte, 16)}); err == nil {
		t.Error("newVerifier accepted a 16-byte public key")
	}
	if _, err := newVerifier(nil); err == nil {
		t.Error("newVerifier accepted an empty trust set")
	}
}

// TestKeyToolingConsistency: the CLI helpers agree with each other and with the
// verifier's keyid derivation, from both a public key and a seed.
func TestKeyToolingConsistency(t *testing.T) {
	km, err := GenerateSigningKey()
	if err != nil {
		t.Fatalf("GenerateSigningKey: %v", err)
	}

	fromPub, err := TrustedKeyInfo(km.PubB64)
	if err != nil {
		t.Fatalf("TrustedKeyInfo: %v", err)
	}
	if fromPub.PubB64 != km.PubB64 || fromPub.KeyID != km.KeyID {
		t.Fatalf("TrustedKeyInfo = %+v, want pub=%s id=%s", fromPub, km.PubB64, km.KeyID)
	}

	fromSeed, err := SigningKeyInfo(km.SeedB64)
	if err != nil {
		t.Fatalf("SigningKeyInfo: %v", err)
	}
	if fromSeed.PubB64 != km.PubB64 || fromSeed.KeyID != km.KeyID {
		t.Fatalf("SigningKeyInfo = %+v, want pub=%s id=%s", fromSeed, km.PubB64, km.KeyID)
	}
}

// TestKeyToolingValidation: the helpers reject malformed input clearly.
func TestKeyToolingValidation(t *testing.T) {
	if _, err := TrustedKeyInfo("not!base64!"); err == nil {
		t.Error("TrustedKeyInfo accepted non-base64")
	}
	if _, err := TrustedKeyInfo(base64.StdEncoding.EncodeToString(make([]byte, 16))); err == nil {
		t.Error("TrustedKeyInfo accepted a 16-byte key")
	}
	if _, err := SigningKeyInfo(base64.StdEncoding.EncodeToString(make([]byte, 16))); err == nil {
		t.Error("SigningKeyInfo accepted a 16-byte seed")
	}
}

// TestKeyIDDerivation: a keyid is a stable, 16-hex-char function of the key.
func TestKeyIDDerivation(t *testing.T) {
	pub, _ := mustKeypair(t)
	id := keyID(pub)
	if len(id) != keyIDLen {
		t.Fatalf("keyid length = %d, want %d", len(id), keyIDLen)
	}
	if keyID(pub) != id {
		t.Fatal("keyid is not deterministic")
	}
}
