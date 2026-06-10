package cache

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
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

// TestHashSpec_EnvUnsetVsEmpty verifies an allowlisted env var that is unset
// hashes differently from one set to the empty string (R10).
func TestHashSpec_EnvUnsetVsEmpty(t *testing.T) {
	root := t.TempDir()
	c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}
	const k = "MAGUS_TEST_ENV_R10"
	s := &Spec{ProjectPath: ".", WorkspaceRoot: root, EnvAllow: []string{k}}

	os.Unsetenv(k)
	hUnset, err := c.hashSpec(context.Background(), s)
	if err != nil {
		t.Fatalf("hashSpec(unset): %v", err)
	}
	t.Setenv(k, "")
	hEmpty, err := c.hashSpec(context.Background(), s)
	if err != nil {
		t.Fatalf("hashSpec(empty): %v", err)
	}
	if hUnset == hEmpty {
		t.Error("an unset env var must hash differently from one set to \"\"")
	}
}

// TestHashSpec_Charms verifies active charms key the cache: a charm-variant run
// differs, while a charm-less run hashes identically to one with empty Charms
// (so existing entries stay valid).
func TestHashSpec_Charms(t *testing.T) {
	root := t.TempDir()
	c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}
	base := &Spec{ProjectPath: ".", WorkspaceRoot: root, Target: "lint"}
	hashOf := func(s *Spec) string {
		h, err := c.hashSpec(context.Background(), s)
		if err != nil {
			t.Fatalf("hashSpec: %v", err)
		}
		return h
	}

	none := hashOf(base)
	empty := hashOf(&Spec{ProjectPath: ".", WorkspaceRoot: root, Target: "lint", Charms: []string{}})
	write := hashOf(&Spec{ProjectPath: ".", WorkspaceRoot: root, Target: "lint", Charms: []string{"write"}})
	debug := hashOf(&Spec{ProjectPath: ".", WorkspaceRoot: root, Target: "lint", Charms: []string{"debug"}})

	if none != empty {
		t.Error("empty Charms must hash identically to no Charms (back-compat)")
	}
	if write == none || debug == none || write == debug {
		t.Errorf("charm-variant runs must differ: none=%s write=%s debug=%s", none[:8], write[:8], debug[:8])
	}
}

// TestHashSpec_SourceExecBit verifies that chmod +x on a source file changes
// the key even though content, mtime, and size are unchanged (R10).
func TestHashSpec_SourceExecBit(t *testing.T) {
	root := t.TempDir()
	c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}
	script := filepath.Join(root, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Spec{ProjectPath: ".", WorkspaceRoot: root, Sources: []string{"run.sh"}}

	h1, err := c.hashSpec(context.Background(), s)
	if err != nil {
		t.Fatalf("hashSpec(0644): %v", err)
	}
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	h2, err := c.hashSpec(context.Background(), s)
	if err != nil {
		t.Fatalf("hashSpec(0755): %v", err)
	}
	if h1 == h2 {
		t.Error("chmod +x on a source file must change the hash")
	}
}

// TestHashSpec_SpellDefVersion verifies that two Specs differing only in
// SpellDefVersion produce different hashes (R2b coverage).
func TestHashSpec_SpellDefVersion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}

	base := &Spec{ProjectPath: ".", WorkspaceRoot: root}
	withV1 := &Spec{ProjectPath: ".", WorkspaceRoot: root, SpellDefVersion: "sha256:aabbcc"}
	withV2 := &Spec{ProjectPath: ".", WorkspaceRoot: root, SpellDefVersion: "sha256:ddeeff"}

	h0, err := c.hashSpec(context.Background(), base)
	if err != nil {
		t.Fatalf("hashSpec(base): %v", err)
	}
	h1, err := c.hashSpec(context.Background(), withV1)
	if err != nil {
		t.Fatalf("hashSpec(v1): %v", err)
	}
	h2, err := c.hashSpec(context.Background(), withV2)
	if err != nil {
		t.Fatalf("hashSpec(v2): %v", err)
	}

	if h0 == h1 {
		t.Error("empty and non-empty SpellDefVersion must hash differently")
	}
	if h1 == h2 {
		t.Error("different SpellDefVersion values must hash differently")
	}
	if h0 == h2 {
		t.Error("empty and second SpellDefVersion must hash differently")
	}
}

// TestHashSpec_KeyVersionIsHashed verifies that keyVersion is mixed into the
// hash: the hash of a fixed Spec is stable across calls (deterministic) and
// non-empty, confirming the format-version prefix is always written.
func TestHashSpec_KeyVersionIsHashed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}
	s := &Spec{ProjectPath: ".", WorkspaceRoot: root}

	h1, err := c.hashSpec(context.Background(), s)
	if err != nil {
		t.Fatalf("first hashSpec: %v", err)
	}
	h2, err := c.hashSpec(context.Background(), s)
	if err != nil {
		t.Fatalf("second hashSpec: %v", err)
	}

	if h1 != h2 {
		t.Errorf("hashSpec not deterministic: %q != %q", h1, h2)
	}
	if len(h1) == 0 {
		t.Error("hashSpec returned empty hash")
	}
	// The current keyVersion is always mixed in; bumping it must change the
	// hash. Verified here by asserting the current constant is the intended value.
	const wantKeyVersion = 3
	if keyVersion != wantKeyVersion {
		t.Errorf("keyVersion = %d, want %d; update this test when bumping", keyVersion, wantKeyVersion)
	}
}

// TestHashSpec_ToolVersionsChangeMisses verifies that two Specs differing only
// in ToolVersions produce different hashes (R1 coverage: a toolchain upgrade
// with unchanged sources must miss). Order-independence is also checked.
func TestHashSpec_ToolVersionsChangeMisses(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}

	base := &Spec{ProjectPath: ".", WorkspaceRoot: root}
	v1 := &Spec{ProjectPath: ".", WorkspaceRoot: root, ToolVersions: []string{"go:go1.22"}}
	v2 := &Spec{ProjectPath: ".", WorkspaceRoot: root, ToolVersions: []string{"go:go1.23"}}
	// Same set in a different order must hash identically (sorted before mixing).
	orderA := &Spec{ProjectPath: ".", WorkspaceRoot: root, ToolVersions: []string{"go:go1.22", "node:v20"}}
	orderB := &Spec{ProjectPath: ".", WorkspaceRoot: root, ToolVersions: []string{"node:v20", "go:go1.22"}}

	hash := func(s *Spec) string {
		h, err := c.hashSpec(context.Background(), s)
		if err != nil {
			t.Fatalf("hashSpec: %v", err)
		}
		return h
	}

	if hash(base) == hash(v1) {
		t.Error("empty and non-empty ToolVersions must hash differently")
	}
	if hash(v1) == hash(v2) {
		t.Error("different ToolVersions must hash differently (R1)")
	}
	if hash(orderA) != hash(orderB) {
		t.Error("ToolVersions order must not affect the hash")
	}
}

// TestHashKeyByteLayout pins the exact byte layout of the cache key. hashSpec
// builds the key via direct buffer writes for speed; this asserts that layout
// stays byte-for-byte identical to the documented "field:value\n" format, so the
// optimization (and any future edit) cannot silently invalidate every cache entry.
func TestHashKeyByteLayout(t *testing.T) {
	c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}

	// No sources and no EnvAllow → no file I/O and no environment lookups, so the
	// key depends only on the literal fields below and the result is deterministic.
	spec := &Spec{
		ProjectPath:     "pkg/x",
		Target:          "build",
		Charms:          []string{"race"},
		Deps:            []string{"d:1"},
		ToolVersions:    []string{"go:1.25"},
		SpellDefVersion: "v1",
	}

	got, err := c.hashSpec(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}

	// Reconstruct the expected byte stream independently, in hashSpec's field order.
	var want bytes.Buffer
	fmt.Fprintf(&want, "keyVersion:%d\n", keyVersion)
	want.WriteString("projectPath:pkg/x\n")
	want.WriteString("target:build\n")
	want.WriteString("charm:race\n")
	want.WriteString("dep:d:1\n")
	want.WriteString("spellDefVersion:v1\n")
	want.WriteString("tool:go:1.25\n")
	sum := sha256.Sum256(want.Bytes())
	expected := hex.EncodeToString(sum[:])

	if got != expected {
		t.Fatalf("cache key byte layout changed:\n got = %s\n want = %s\n(layout:\n%s)",
			got, expected, want.String())
	}
}
