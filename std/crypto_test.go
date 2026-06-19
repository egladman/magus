package std

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
