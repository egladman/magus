package std

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Known SHA-256 vectors from FIPS 180-4 / RFC examples.
const (
	sha256Empty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	sha256ABC   = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
)

func TestCryptoSha256Hex(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got, err := CryptoSha256Hex(context.Background(), "")
		require.NoError(t, err)
		assert.Equal(t, sha256Empty, got)
	})
	t.Run("abc", func(t *testing.T) {
		got, err := CryptoSha256Hex(context.Background(), "abc")
		require.NoError(t, err)
		assert.Equal(t, sha256ABC, got)
	})
}

func TestCryptoSha256File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(path, []byte("abc"), 0o644))
	got, err := CryptoSha256File(context.Background(), path)
	require.NoError(t, err)
	assert.Equal(t, sha256ABC, got)
}

func TestCryptoSha256FileMissing(t *testing.T) {
	_, err := CryptoSha256File(context.Background(), filepath.Join(t.TempDir(), "nope"))
	assert.Error(t, err, "expected error for a missing file")
}

// Known digests of "abc"/"" from the standard test vectors.
func TestCryptoDigests(t *testing.T) {
	digest := func(fn func(context.Context, string) (string, error), in string) string {
		got, err := fn(context.Background(), in)
		require.NoError(t, err)
		return got
	}

	t.Run("sha512/abc", func(t *testing.T) {
		assert.Equal(t, "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f", digest(CryptoSha512Hex, "abc"))
	})
	t.Run("sha1/abc", func(t *testing.T) {
		assert.Equal(t, "a9993e364706816aba3e25717850c26c9cd0d89d", digest(CryptoSha1Hex, "abc"))
	})
	t.Run("sha1/empty", func(t *testing.T) {
		assert.Equal(t, "da39a3ee5e6b4b0d3255bfef95601890afd80709", digest(CryptoSha1Hex, ""))
	})
	t.Run("md5/abc", func(t *testing.T) {
		assert.Equal(t, "900150983cd24fb0d6963f7d28e17f72", digest(CryptoMd5Hex, "abc"))
	})
	t.Run("md5/empty", func(t *testing.T) {
		assert.Equal(t, "d41d8cd98f00b204e9800998ecf8427e", digest(CryptoMd5Hex, ""))
	})
}

func TestCryptoSignFileHonorsSandbox(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.bin")
	require.NoError(t, os.WriteFile(path, []byte("payload"), 0o644))

	keyEnv := "MAGUS_TEST_SIGN_KEY"
	_, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	t.Setenv(keyEnv, hex.EncodeToString(priv))

	// A policy with no matching rule denies both read and write.
	p := &sandbox.Policy{Workspace: t.TempDir()}
	ctx := sandbox.WithPolicy(context.Background(), p)

	_, err = CryptoSignFile(ctx, SignEd25519, path, keyEnv)
	assert.Error(t, err, "expected sandbox read denial")
	_, statErr := os.Stat(path + ".sig")
	assert.True(t, os.IsNotExist(statErr), "a sandbox-denied sign_file must not write a .sig")
}

func TestCryptoSignFileTracingNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.bin")
	require.NoError(t, os.WriteFile(path, []byte("payload"), 0o644))

	keyEnv := "MAGUS_TEST_SIGN_KEY_TRACE"
	_, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	t.Setenv(keyEnv, hex.EncodeToString(priv))

	ctx := types.WithTrace(context.Background())
	got, err := CryptoSignFile(ctx, SignEd25519, path, keyEnv)
	require.NoError(t, err)
	assert.Equal(t, "", got, "a dry run must not produce a real signature")
	_, statErr := os.Stat(path + ".sig")
	assert.True(t, os.IsNotExist(statErr), "a dry run must not write a .sig file")
}

// TestCryptoSha512File exercises hashFile through one of the new algorithms.
func TestCryptoSha512File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(path, []byte("abc"), 0o644))
	got, err := CryptoSha512File(context.Background(), path)
	require.NoError(t, err)
	const sha512ABC = "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"
	assert.Equal(t, sha512ABC, got)
}

func TestCryptoHmacSha256(t *testing.T) {
	ctx := context.Background()

	// RFC 4231 test case 1.
	key := []byte{0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b,
		0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b, 0x0b}
	got, err := CryptoHmacSha256Hex(ctx, key, []byte("Hi There"))
	require.NoError(t, err)
	assert.Equal(t, "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7", got)
}

func TestCryptoHmacChainsAsAwsSigV4Does(t *testing.T) {
	ctx := context.Background()

	// The shape spells/aws/s3-cache builds: each raw digest keys the next call.
	// This is why hmac_sha256 returns BYTES and not a str: a rune-oriented
	// string would not survive an arbitrary digest.
	kDate, err := CryptoHmacSha256(ctx, []byte("AWS4secret"), []byte("20260811"))
	require.NoError(t, err)
	kRegion, err := CryptoHmacSha256(ctx, kDate, []byte("us-east-1"))
	require.NoError(t, err)
	kService, err := CryptoHmacSha256(ctx, kRegion, []byte("s3"))
	require.NoError(t, err)
	kSigning, err := CryptoHmacSha256(ctx, kService, []byte("aws4_request"))
	require.NoError(t, err)

	sig, err := CryptoHmacSha256Hex(ctx, kSigning, []byte("string-to-sign"))
	require.NoError(t, err)
	assert.Len(t, sig, 64, "a hex SHA-256 signature is 64 characters")

	// Deterministic: the same inputs must always yield the same signature.
	again, err := CryptoHmacSha256Hex(ctx, kSigning, []byte("string-to-sign"))
	require.NoError(t, err)
	assert.Equal(t, sig, again)
}

func TestCryptoBase64BytesRoundTripsBinary(t *testing.T) {
	ctx := context.Background()
	// NUL and 0xFF: invalid UTF-8, so this fails if the payload ever passes
	// through a rune-oriented str.
	blob := []byte{0x00, 0x01, 0xff, 0xfe, 'h', 'i', 0x00, 0x80}

	enc, err := CryptoBase64EncodeBytes(ctx, blob)
	require.NoError(t, err)
	back, err := CryptoBase64DecodeBytes(ctx, enc)
	require.NoError(t, err)
	assert.Equal(t, blob, back)

	_, err = CryptoBase64DecodeBytes(ctx, "not!valid!base64")
	require.Error(t, err)
}

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
// agree with the string form over the same bytes: the file path streams, the
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
