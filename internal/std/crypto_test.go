package std

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
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

// TestCryptoKeyed exercises RegisterExtraCrypto from Buzz the way a spell uses
// it: HMAC against a known vector, the byte-list output chaining as the next key,
// and base64 round-tripping. The chain test reproduces AWS's published SigV4
// signing-key derivation vector, proving the str→bytes→bytes→hex path is
// byte-exact and AWS-correct (not merely self-consistent).
func TestCryptoKeyed(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	sess.SetSyntheticModule("magus/extra/crypto", RegisterExtraCrypto(ctx, sess))

	const src = `
import "magus/extra/crypto" as xc;

// RFC 4231-style known HMAC-SHA256 vector.
export fun hmacHex() > str {
    return xc.hmacSha256Hex("key", "The quick brown fox jumps over the lazy dog");
}

// AWS "Deriving the signing key" example: the result is hex(kSigning) because
// kSigning = HMAC(kService, "aws4_request"). The byte-list outputs feed straight
// back as the next key, so this also proves the chain never corrupts a byte.
export fun awsSigningKeyHex() > str {
    final kDate    = xc.hmacSha256("AWS4wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "20150830");
    final kRegion  = xc.hmacSha256(kDate, "us-east-1");
    final kService = xc.hmacSha256(kRegion, "iam");
    return xc.hmacSha256Hex(kService, "aws4_request");
}

export fun b64() > str { return xc.base64Encode("hello"); }
export fun b64roundtrip() > str { return xc.base64Encode(xc.base64Decode("aGVsbG8=")); }
`
	require.NoError(t, sess.Exec(ctx, src), "exec")
	call := func(fn string) string {
		t.Helper()
		rv, err := sess.CallValue(ctx, sess.Exports()[fn], nil)
		require.NoError(t, err, fn)
		return rv.AsString()
	}

	assert.Equal(t, "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8", call("hmacHex"))
	assert.Equal(t, "2c94c0cf5378ada6887f09bb697df8fc0affdb34ba1cdd5bda32b664bd55b73c", call("awsSigningKeyHex"), "AWS signing key (chain corrupted a byte)")
	assert.Equal(t, "aGVsbG8=", call("b64"))
	assert.Equal(t, "aGVsbG8=", call("b64roundtrip"), "base64 round-trip")
}
