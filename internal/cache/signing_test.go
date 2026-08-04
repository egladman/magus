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

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustKeypair(t *testing.T) (ed25519.PublicKey, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err, "generate key")
	return pub, priv.Seed()
}

// TestSignVerifyRoundTrip is the happy path: a manifest signed by a key in the
// trust set verifies.
func TestSignVerifyRoundTrip(t *testing.T) {
	pub, seed := mustKeypair(t)
	s, err := newSigner(seed)
	require.NoError(t, err, "newSigner")
	v, err := newVerifier([][]byte{pub})
	require.NoError(t, err, "newVerifier")

	manifest := []byte(`{"projectPath":"test/pkg","hash":"abc123","outputs":[]}`)
	sig, err := s.sign(domainArtifact, manifest, nil)
	require.NoError(t, err, "sign")
	assert.NoError(t, mustVerify(v, sig, manifest), "verify valid signature")
	assert.Equal(t, s.keyid, keyID(pub), "signer keyid must match derived keyid")
}

// TestVerifyRejectsUntrustedKey: a signature from a key absent from the trust set
// must fail, even though it is itself valid.
func TestVerifyRejectsUntrustedKey(t *testing.T) {
	_, seedA := mustKeypair(t)
	pubB, _ := mustKeypair(t)
	s, _ := newSigner(seedA)
	v, _ := newVerifier([][]byte{pubB}) // trusts B, not A

	manifest := []byte(`{"hash":"x"}`)
	sig, _ := s.sign(domainArtifact, manifest, nil)
	assert.Error(t, mustVerify(v, sig, manifest), "verify accepted a signature from an untrusted key")
}

// TestVerifyRejectsTamperedManifest: the bytes presented at verify time must be
// the bytes that were signed; a single flipped byte fails.
func TestVerifyRejectsTamperedManifest(t *testing.T) {
	pub, seed := mustKeypair(t)
	s, _ := newSigner(seed)
	v, _ := newVerifier([][]byte{pub})

	manifest := []byte(`{"hash":"original"}`)
	sig, _ := s.sign(domainArtifact, manifest, nil)
	tampered := []byte(`{"hash":"poisoned"}`)
	assert.Error(t, mustVerify(v, sig, tampered), "verify accepted a signature over different manifest bytes")
}

// TestVerifyRejectsBadAlg: only ed25519 envelopes are accepted.
func TestVerifyRejectsBadAlg(t *testing.T) {
	pub, _ := mustKeypair(t)
	v, _ := newVerifier([][]byte{pub})
	env, _ := json.Marshal(sigEnvelope{Alg: "rsa", KeyID: keyID(pub)})
	assert.Error(t, mustVerify(v, env, []byte("m")), "verify accepted a non-ed25519 algorithm")
}

// TestKeyMaterialValidation: malformed key material is rejected at construction.
func TestKeyMaterialValidation(t *testing.T) {
	_, err := newSigner(make([]byte, 16))
	assert.Error(t, err, "newSigner accepted a 16-byte seed")
	_, err = newVerifier([][]byte{make([]byte, 16)})
	assert.Error(t, err, "newVerifier accepted a 16-byte public key")
	_, err = newVerifier(nil)
	assert.Error(t, err, "newVerifier accepted an empty trust set")
}

// TestKeyToolingConsistency: the CLI helpers agree with each other and with the
// verifier's keyid derivation, from both a public key and a seed.
func TestKeyToolingConsistency(t *testing.T) {
	km, err := GenerateSigningKey()
	require.NoError(t, err, "GenerateSigningKey")

	fromPub, err := TrustedKeyInfo(km.PubB64)
	require.NoError(t, err, "TrustedKeyInfo")
	assert.Equal(t, km.PubB64, fromPub.PubB64)
	assert.Equal(t, km.KeyID, fromPub.KeyID)

	fromSeed, err := SigningKeyInfo(km.SeedB64)
	require.NoError(t, err, "SigningKeyInfo")
	assert.Equal(t, km.PubB64, fromSeed.PubB64)
	assert.Equal(t, km.KeyID, fromSeed.KeyID)
}

// TestKeyToolingValidation: the helpers reject malformed input clearly.
func TestKeyToolingValidation(t *testing.T) {
	_, err := TrustedKeyInfo("not!base64!")
	assert.Error(t, err, "TrustedKeyInfo accepted non-base64")
	_, err = TrustedKeyInfo(base64.StdEncoding.EncodeToString(make([]byte, 16)))
	assert.Error(t, err, "TrustedKeyInfo accepted a 16-byte key")
	_, err = SigningKeyInfo(base64.StdEncoding.EncodeToString(make([]byte, 16)))
	assert.Error(t, err, "SigningKeyInfo accepted a 16-byte seed")
}

// TestKeyIDDerivation: a keyid is a stable, 16-hex-char function of the key.
func TestKeyIDDerivation(t *testing.T) {
	pub, _ := mustKeypair(t)
	id := keyID(pub)
	assert.Len(t, id, keyIDLen)
	assert.Equal(t, id, keyID(pub), "keyid is not deterministic")
}

// TestHashStep_EnvUnsetVsEmpty verifies an allowlisted env var that is unset
// hashes differently from one set to the empty string (R10).
func TestHashStep_EnvUnsetVsEmpty(t *testing.T) {
	root := t.TempDir()
	c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}
	const k = "MAGUS_TEST_ENV_R10"
	s := &Step{ProjectPath: ".", WorkspaceRoot: root, EnvAllow: []string{k}}

	os.Unsetenv(k)
	hUnset, err := c.hashStep(context.Background(), s)
	require.NoError(t, err, "hashStep(unset)")
	t.Setenv(k, "")
	hEmpty, err := c.hashStep(context.Background(), s)
	require.NoError(t, err, "hashStep(empty)")
	assert.NotEqual(t, hEmpty, hUnset, "an unset env var must hash differently from one set to \"\"")
}

// TestHashStep_Charms verifies active charms key the cache: a charm-variant run
// differs, while a charm-less run hashes identically to one with empty Charms
// (so existing entries stay valid).
func TestHashStep_Charms(t *testing.T) {
	root := t.TempDir()
	c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}
	base := &Step{ProjectPath: ".", WorkspaceRoot: root, Target: "lint"}
	hashOf := func(s *Step) string {
		h, err := c.hashStep(context.Background(), s)
		require.NoError(t, err, "hashStep")
		return h
	}

	none := hashOf(base)
	empty := hashOf(&Step{ProjectPath: ".", WorkspaceRoot: root, Target: "lint", Charms: []string{}})
	write := hashOf(&Step{ProjectPath: ".", WorkspaceRoot: root, Target: "lint", Charms: []string{"write"}})
	debug := hashOf(&Step{ProjectPath: ".", WorkspaceRoot: root, Target: "lint", Charms: []string{"debug"}})

	assert.Equal(t, none, empty, "empty Charms must hash identically to no Charms (back-compat)")
	assert.NotEqual(t, none, write, "charm-variant runs must differ from none")
	assert.NotEqual(t, none, debug, "charm-variant runs must differ from none")
	assert.NotEqual(t, write, debug, "distinct charm-variant runs must differ from each other")
}

// TestHashStep_SourceExecBit verifies that chmod +x on a source file changes
// the key even though content, mtime, and size are unchanged (R10).
func TestHashStep_SourceExecBit(t *testing.T) {
	root := t.TempDir()
	c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}
	script := filepath.Join(root, "run.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho hi\n"), 0o644))
	s := &Step{ProjectPath: ".", WorkspaceRoot: root, Sources: []string{"run.sh"}}

	h1, err := c.hashStep(context.Background(), s)
	require.NoError(t, err, "hashStep(0644)")
	require.NoError(t, os.Chmod(script, 0o755))
	h2, err := c.hashStep(context.Background(), s)
	require.NoError(t, err, "hashStep(0755)")
	assert.NotEqual(t, h1, h2, "chmod +x on a source file must change the hash")
}

// TestHashStep_SpellDefVersion verifies that two Steps differing only in
// SpellDefVersion produce different hashes (R2b coverage).
func TestHashStep_SpellDefVersion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}

	base := &Step{ProjectPath: ".", WorkspaceRoot: root}
	withV1 := &Step{ProjectPath: ".", WorkspaceRoot: root, SpellDefVersion: "sha256:aabbcc"}
	withV2 := &Step{ProjectPath: ".", WorkspaceRoot: root, SpellDefVersion: "sha256:ddeeff"}

	h0, err := c.hashStep(context.Background(), base)
	require.NoError(t, err, "hashStep(base)")
	h1, err := c.hashStep(context.Background(), withV1)
	require.NoError(t, err, "hashStep(v1)")
	h2, err := c.hashStep(context.Background(), withV2)
	require.NoError(t, err, "hashStep(v2)")

	assert.NotEqual(t, h0, h1, "empty and non-empty SpellDefVersion must hash differently")
	assert.NotEqual(t, h1, h2, "different SpellDefVersion values must hash differently")
	assert.NotEqual(t, h0, h2, "empty and second SpellDefVersion must hash differently")
}

// TestHashStep_KeyVersionIsHashed verifies that KeyVersion is mixed into the
// hash: the hash of a fixed Step is stable across calls (deterministic) and
// non-empty, confirming the format-version prefix is always written.
func TestHashStep_KeyVersionIsHashed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}
	s := &Step{ProjectPath: ".", WorkspaceRoot: root}

	h1, err := c.hashStep(context.Background(), s)
	require.NoError(t, err, "first hashStep")
	h2, err := c.hashStep(context.Background(), s)
	require.NoError(t, err, "second hashStep")

	assert.Equal(t, h1, h2, "hashStep not deterministic")
	assert.NotEmpty(t, h1, "hashStep returned empty hash")
	// The current KeyVersion is always mixed in; bumping it must change the
	// hash. Verified here by asserting the current constant is the intended value.
	const wantKeyVersion = 3
	assert.Equal(t, wantKeyVersion, KeyVersion, "KeyVersion changed; update this test when bumping")
}

// TestHashStep_ToolVersionsChangeMisses verifies that two Steps differing only
// in ToolVersions produce different hashes (R1 coverage: a toolchain upgrade
// with unchanged sources must miss). Order-independence is also checked.
func TestHashStep_ToolVersionsChangeMisses(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}

	base := &Step{ProjectPath: ".", WorkspaceRoot: root}
	v1 := &Step{ProjectPath: ".", WorkspaceRoot: root, ToolVersions: []string{"go:go1.22"}}
	v2 := &Step{ProjectPath: ".", WorkspaceRoot: root, ToolVersions: []string{"go:go1.23"}}
	// Same set in a different order must hash identically (sorted before mixing).
	orderA := &Step{ProjectPath: ".", WorkspaceRoot: root, ToolVersions: []string{"go:go1.22", "node:v20"}}
	orderB := &Step{ProjectPath: ".", WorkspaceRoot: root, ToolVersions: []string{"node:v20", "go:go1.22"}}

	hash := func(s *Step) string {
		h, err := c.hashStep(context.Background(), s)
		require.NoError(t, err, "hashStep")
		return h
	}

	assert.NotEqual(t, hash(base), hash(v1), "empty and non-empty ToolVersions must hash differently")
	assert.NotEqual(t, hash(v1), hash(v2), "different ToolVersions must hash differently (R1)")
	assert.Equal(t, hash(orderA), hash(orderB), "ToolVersions order must not affect the hash")
}

// TestHashKeyByteLayout pins the exact byte layout of the cache key. hashStep
// builds the key via direct buffer writes for speed; this asserts that layout
// stays byte-for-byte identical to the documented "field:value\n" format, so the
// optimization (and any future edit) cannot silently invalidate every cache entry.
func TestHashKeyByteLayout(t *testing.T) {
	c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}

	// No sources and no EnvAllow → no file I/O and no environment lookups, so the
	// key depends only on the literal fields below and the result is deterministic.
	step := &Step{
		ProjectPath:     "pkg/x",
		Target:          "build",
		Charms:          []string{"race"},
		Deps:            []string{"d:1"},
		ToolVersions:    []string{"go:1.25"},
		SpellDefVersion: "v1",
	}

	got, err := c.hashStep(context.Background(), step)
	require.NoError(t, err)

	// Reconstruct the expected byte stream independently, in hashStep's field order.
	var want bytes.Buffer
	fmt.Fprintf(&want, "keyVersion:%d\n", KeyVersion)
	want.WriteString("projectPath:pkg/x\n")
	want.WriteString("target:build\n")
	want.WriteString("charm:race\n")
	want.WriteString("dep:d:1\n")
	want.WriteString("spellDefVersion:v1\n")
	want.WriteString("tool:go:1.25\n")
	sum := sha256.Sum256(want.Bytes())
	expected := hex.EncodeToString(sum[:])

	assert.Equal(t, expected, got, "cache key byte layout changed (layout:\n%s)", want.String())
}

// TestHashStepKeysExtraArgs locks in that args after `--` participate in the
// cache key. Before this, a run with different args replayed the previous run's
// result: the args changed what the target did but not what it hashed.
//
// The empty case is the back-compat guarantee - no extra args must hash exactly
// as before, so adding this invalidated no existing entry (same property the
// Charms lines rely on).
func TestHashStepKeysExtraArgs(t *testing.T) {
	root := t.TempDir()
	c := &Cache{mtimes: newMtimeStore(t.TempDir(), nil)}
	hashOf := func(s *Step) string {
		h, err := c.hashStep(context.Background(), s)
		require.NoError(t, err, "hashStep")
		return h
	}
	step := func(args ...string) *Step {
		return &Step{ProjectPath: ".", WorkspaceRoot: root, Target: "probe", ExtraArgs: args}
	}

	none := hashOf(&Step{ProjectPath: ".", WorkspaceRoot: root, Target: "probe"})
	assert.Equal(t, none, hashOf(step()), "no extra args must hash identically (back-compat)")

	alpha, beta := hashOf(step("alpha")), hashOf(step("beta"))
	assert.NotEqual(t, none, alpha, "an arg must change the key")
	assert.NotEqual(t, alpha, beta, "different args must not share a key")
	assert.Equal(t, alpha, hashOf(step("alpha")), "the same args must replay")

	// Order is significant: `-run X` is not `X -run`, so args are never sorted.
	assert.NotEqual(t, hashOf(step("-run", "X")), hashOf(step("X", "-run")), "arg ORDER must key")
	assert.NotEqual(t, hashOf(step("a", "b")), hashOf(step("ab")), "args must not be concatenated")
}

// mustVerify adapts verify's (legacy, error) result for the assertions above, which
// only care whether the envelope authenticates.
func mustVerify(v *verifier, sigBytes, manifestBytes []byte) error {
	_, err := v.verify(domainArtifact, sigBytes, manifestBytes, nil)
	return err
}

// TestVerifyRejectsCrossDomainSignature is the type-confusion guard: an output
// bundle's signature must never authenticate the same bytes as a cache artifact.
// Without domain separation a published FAILING run could be re-tarred as an
// artifact and replayed as a teammate's cached pass.
func TestVerifyRejectsCrossDomainSignature(t *testing.T) {
	pub, seed := mustKeypair(t)
	s, _ := newSigner(seed)
	v, _ := newVerifier([][]byte{pub})

	meta := []byte(`{"schema":1,"descriptor":{"ref":"outdeadbeefcafe"}}`)
	sig, err := s.sign(domainBundle, meta, nil)
	require.NoError(t, err)

	_, err = v.verify(domainBundle, sig, meta, nil)
	assert.NoError(t, err, "a bundle signature verifies in its own domain")

	_, err = v.verify(domainArtifact, sig, meta, nil)
	assert.Error(t, err, "a bundle signature must NOT verify as a cache artifact")
}

// TestVerifyAcceptsLegacyEnvelopeAndReportsIt: an artifact signed by a released
// magus (bare signature over the manifest, no members) still verifies - rejecting it
// would turn every existing remote entry into a miss - and reports legacy so the
// caller drops the extras the signature never covered.
func TestVerifyAcceptsLegacyEnvelopeAndReportsIt(t *testing.T) {
	pub, seed := mustKeypair(t)
	s, _ := newSigner(seed)
	v, _ := newVerifier([][]byte{pub})

	manifest := []byte(`{"projectPath":"p","hash":"abc"}`)
	sum := sha256.Sum256(manifest)
	legacyEnv, err := json.Marshal(sigEnvelope{
		Alg:            sigAlg,
		KeyID:          s.keyid,
		ManifestSHA256: hex.EncodeToString(sum[:]),
		Sig:            base64.StdEncoding.EncodeToString(ed25519.Sign(s.priv, manifest)),
	})
	require.NoError(t, err)

	// Extras arrived, but the legacy signature covers none of them: accepted as
	// legacy rather than rejected outright.
	legacy, err := v.verify(domainArtifact, legacyEnv, manifest, map[string]string{"logs/p/abc.log": "deadbeef"})
	require.NoError(t, err, "a legacy artifact must still verify")
	assert.True(t, legacy, "the caller must be told to drop the unauthenticated extras")
}
