package selfupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
}

// TestReleaseTrustAnchorMatchesInstallerAndCI keeps the ring's copies in step.
//
// The installer and the setup action must accept EVERY key a binary accepts, or a
// rotation strands one path while the other works. The verify guide publishes the
// ACTIVE key alone: it tells a reader which key signed the release in front of them,
// and listing keys that have signed nothing would be noise a human has to resolve.
func TestReleaseTrustAnchorMatchesInstallerAndCI(t *testing.T) {
	root := repoRoot(t)
	installer, err := os.ReadFile(filepath.Join(root, "docs", "gen", "install"))
	require.NoError(t, err)
	action, err := os.ReadFile(filepath.Join(root, ".github", "actions", "setup-magus", "action.yml"))
	require.NoError(t, err)
	guide, err := os.ReadFile(filepath.Join(root, "docs", "setup", "verify.md"))
	require.NoError(t, err)

	for _, key := range ReleaseKeys.Verifiers() {
		keyHex := hex.EncodeToString(key.Pub)
		assert.Equal(t, 1, strings.Count(string(installer), keyHex),
			"installer must carry release key %s exactly once", key.ID)
		assert.Equal(t, 1, strings.Count(string(action), keyHex),
			"setup action must carry release key %s exactly once", key.ID)
	}

	// A revoked key must be gone from both, or the two install paths keep accepting
	// what self update has already been told to refuse.
	for _, key := range ReleaseKeys {
		if key.State != KeyRevoked {
			continue
		}
		keyHex := hex.EncodeToString(key.Pub)
		assert.NotContains(t, string(installer), keyHex, "installer still carries revoked key %s", key.ID)
		assert.NotContains(t, string(action), keyHex, "setup action still carries revoked key %s", key.ID)
	}

	active, err := ReleaseKeys.Active()
	require.NoError(t, err)
	assert.Contains(t, string(guide), base64.StdEncoding.EncodeToString(active.Pub),
		"verify guide must publish the active release key %s", active.ID)
}

// TestKeyIDIsDerivedNotDeclared pins the fingerprint of the key shipping today. It is
// what an error message names and what the signed index declares, so a change to how
// it is computed renames a key the field has already seen.
func TestKeyIDIsDerivedNotDeclared(t *testing.T) {
	active, err := ReleaseKeys.Active()
	require.NoError(t, err)
	assert.Equal(t, "59160bb7b179dc68", active.ID)
	assert.Equal(t, active.ID, KeyID(active.Pub), "the ID must follow from the key, never from the file")
}

func TestParseKeyringRejectsAmbiguity(t *testing.T) {
	good := hex.EncodeToString(mustKey(t))
	other := hex.EncodeToString(mustKey(t))
	cases := []struct{ name, body, wantErr string }{
		{"no keys", `{"keys":[]}`, "no keys"},
		{"bad hex", `{"keys":[{"state":"active","key":"zz"}]}`, "decode"},
		{"short key", `{"keys":[{"state":"active","key":"aabb"}]}`, "want 32"},
		{"unknown state", fmt.Sprintf(`{"keys":[{"state":"probationary","key":%q}]}`, good), "unknown state"},
		{"duplicate", fmt.Sprintf(`{"keys":[{"state":"active","key":%q},{"state":"retired","key":%q}]}`, good, good), "appears twice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseKeyring([]byte(c.body))
			require.ErrorContains(t, err, c.wantErr)
		})
	}

	// Which key signed a release has to be answerable from the file alone, so two
	// active keys is a configuration error rather than a choice to make at runtime.
	ring, err := parseKeyring([]byte(fmt.Sprintf(`{"keys":[{"state":"active","key":%q},{"state":"active","key":%q}]}`, good, other)))
	require.NoError(t, err)
	_, err = ring.Active()
	require.ErrorContains(t, err, "2 active keys")
}

// TestVerifyAcceptsAnyNonRevokedKey is the property the ring exists for: a standby key
// verifies before it has ever signed, so a rotation needs no compatibility release for
// anyone to skip.
func TestVerifyAcceptsAnyNonRevokedKey(t *testing.T) {
	activePub, activePriv := mustPair(t)
	standbyPub, standbyPriv := mustPair(t)
	revokedPub, revokedPriv := mustPair(t)
	ring := Keyring{
		{ID: KeyID(activePub), State: KeyActive, Pub: activePub},
		{ID: KeyID(standbyPub), State: KeyStandby, Pub: standbyPub},
		{ID: KeyID(revokedPub), State: KeyRevoked, Pub: revokedPub},
	}
	msg := []byte("release index bytes")

	signer, err := ring.Verify(msg, ed25519.Sign(standbyPriv, msg))
	require.NoError(t, err)
	assert.Equal(t, KeyID(standbyPub), signer.ID, "a standby key verifies before it has signed a release")

	signer, err = ring.Verify(msg, ed25519.Sign(activePriv, msg))
	require.NoError(t, err)
	assert.Equal(t, KeyID(activePub), signer.ID)

	_, err = ring.Verify(msg, ed25519.Sign(revokedPriv, msg))
	require.ErrorContains(t, err, "matches none of the 2 release key(s)")

	// Without() is what the client applies after reading the signed index, so a key
	// revoked there stops verifying even though this binary was built trusting it.
	_, err = ring.Without([]string{KeyID(standbyPub)}).Verify(msg, ed25519.Sign(standbyPriv, msg))
	require.ErrorContains(t, err, "matches none of the 1 release key(s)")
}

func TestRevokedIDsAreWhatGetsPublished(t *testing.T) {
	activePub, _ := mustPair(t)
	revokedPub, _ := mustPair(t)
	ring := Keyring{
		{ID: KeyID(activePub), State: KeyActive, Pub: activePub},
		{ID: KeyID(revokedPub), State: KeyRevoked, Pub: revokedPub},
	}
	assert.Equal(t, []string{KeyID(revokedPub)}, ring.RevokedIDs())
	assert.Empty(t, ReleaseKeys.RevokedIDs(), "nothing is revoked today; publishing an ID would strand it")
}

func mustPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, priv
}

func mustKey(t *testing.T) ed25519.PublicKey {
	t.Helper()
	pub, _ := mustPair(t)
	return pub
}

// ringOf wraps a test public key as a one-key active ring, the shape every call site
// had before the ring existed.
func ringOf(pub ed25519.PublicKey) Keyring {
	return Keyring{{ID: KeyID(pub), State: KeyActive, Pub: pub}}
}

// badRing is a ring holding one key of the wrong length. ed25519.Verify panics on
// those, so every entry point has to reject them before it verifies anything.
func badRing(n int) Keyring {
	return Keyring{{ID: "malformed", State: KeyActive, Pub: make(ed25519.PublicKey, n)}}
}
