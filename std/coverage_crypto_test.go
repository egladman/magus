package std

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// covSigningKey generates an ed25519 pair, puts the private half in a uniquely
// named environment variable, and returns (keyEnv, pubHex). The key is NAMED and
// never returned as a value, which is the shape crypto.sign is built around.
func covSigningKey(t *testing.T) (keyEnv, pubHex string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	keyEnv = "MAGUS_COV_SIGNING_KEY"
	t.Setenv(keyEnv, hex.EncodeToString(priv))
	return keyEnv, hex.EncodeToString(pub)
}

func TestCryptoSignVerifyRoundTrip(t *testing.T) {
	ctx := context.Background()
	keyEnv, pubHex := covSigningKey(t)

	sig, err := CryptoSign(ctx, SignEd25519, "payload", keyEnv)
	require.NoError(t, err)
	assert.Len(t, sig, ed25519.SignatureSize*2, "the signature is returned as hex")

	ok, err := CryptoVerify(ctx, SignEd25519, "payload", sig, pubHex)
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = CryptoVerify(ctx, SignEd25519, "tampered", sig, pubHex)
	require.NoError(t, err)
	assert.False(t, ok, "a signature over different data must not verify")

	// Surrounding whitespace is tolerated on both hex inputs: a signature pasted
	// from a file arrives with a trailing newline.
	ok, err = CryptoVerify(ctx, SignEd25519, "payload", "  "+sig+"\n", "\t"+pubHex+" ")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestCryptoPublicKey(t *testing.T) {
	keyEnv, pubHex := covSigningKey(t)

	got, err := CryptoPublicKey(context.Background(), SignEd25519, keyEnv)
	require.NoError(t, err)
	assert.Equal(t, pubHex, got, "the publisher prints exactly what its readers must pin")
}

// TestCryptoRejectsAnUnknownAlgorithm: the algorithm is a plain string parameter,
// so the refusal has to carry what an enum would have offered as completion.
func TestCryptoRejectsAnUnknownAlgorithm(t *testing.T) {
	ctx := context.Background()
	keyEnv, pubHex := covSigningKey(t)

	_, err := CryptoSign(ctx, "rsa", "payload", keyEnv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown signing algorithm "rsa" (want "ed25519")`)

	_, err = CryptoPublicKey(ctx, "rsa", keyEnv)
	assert.Error(t, err)

	_, err = CryptoVerify(ctx, "rsa", "payload", "00", pubHex)
	assert.Error(t, err)

	_, err = CryptoSignFile(ctx, "rsa", filepath.Join(t.TempDir(), "f"), keyEnv)
	assert.Error(t, err)
}

// TestSigningKeyRejectsABadKey covers every way the named variable can fail to
// hold a usable key. The invalid-hex message must not echo the value: a decode
// error on a secret that printed the secret back would defeat the whole design.
func TestSigningKeyRejectsABadKey(t *testing.T) {
	ctx := context.Background()

	_, err := CryptoSign(ctx, SignEd25519, "payload", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key_env is required")

	_, err = CryptoSign(ctx, SignEd25519, "payload", "MAGUS_COV_KEY_NEVER_SET")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MAGUS_COV_KEY_NEVER_SET is not set")

	t.Setenv("MAGUS_COV_KEY_NOT_HEX", "zzzz-not-hex-zzzz")
	_, err = CryptoSign(ctx, SignEd25519, "payload", "MAGUS_COV_KEY_NOT_HEX")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MAGUS_COV_KEY_NOT_HEX is not valid hex")
	assert.NotContains(t, err.Error(), "zzzz-not-hex-zzzz", "a decode error must not print the secret back")

	t.Setenv("MAGUS_COV_KEY_SHORT", hex.EncodeToString([]byte("too short")))
	_, err = CryptoSign(ctx, SignEd25519, "payload", "MAGUS_COV_KEY_SHORT")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be 64 bytes (128 hex chars), got 9 bytes")
}

// TestCryptoVerifyRejectsMalformedInputs: ed25519.Verify panics on a wrong-length
// key, so the length is checked rather than discovered.
func TestCryptoVerifyRejectsMalformedInputs(t *testing.T) {
	ctx := context.Background()
	_, pubHex := covSigningKey(t)

	_, err := CryptoVerify(ctx, SignEd25519, "payload", "not-hex", pubHex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature is not hex")

	_, err = CryptoVerify(ctx, SignEd25519, "payload", "00", "not-hex")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "public key is not hex")

	_, err = CryptoVerify(ctx, SignEd25519, "payload", "00", "0011")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "public key is 2 bytes, want 32")
}

// TestCryptoLegacyFileDigests covers the two interop-only file hashes. Each must
// agree with the string form over the same bytes - the file path streams, the
// string path does not, and a disagreement would make a checksum manifest wrong.
func TestCryptoLegacyFileDigests(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0o644))

	sha1Hex, err := CryptoSha1File(ctx, path)
	require.NoError(t, err)
	wantSha1, err := CryptoSha1Hex(ctx, "hello")
	require.NoError(t, err)
	assert.Equal(t, wantSha1, sha1Hex)
	assert.Equal(t, "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d", sha1Hex)

	md5Hex, err := CryptoMd5File(ctx, path)
	require.NoError(t, err)
	wantMd5, err := CryptoMd5Hex(ctx, "hello")
	require.NoError(t, err)
	assert.Equal(t, wantMd5, md5Hex)
	assert.Equal(t, "5d41402abc4b2a76b9719d911017c592", md5Hex)

	_, err = CryptoSha1File(ctx, filepath.Join(dir, "absent"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "crypto.sha1_file")

	_, err = CryptoMd5File(ctx, filepath.Join(dir, "absent"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "crypto.md5_file")
}
