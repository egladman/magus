package std

import (
	"context"
	"crypto/md5"  //nolint:gosec // G501: MD5 is exposed for interop with legacy checksum manifests, not security.
	"crypto/sha1" //nolint:gosec // G505: SHA-1 is exposed for interop with legacy/git checksums, not security.
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"

	"github.com/egladman/magus/internal/sandbox"
)

//go:generate go run ../../cmd/magus-bindings-gen -module crypto -lang buzz -out gen/buzz/crypto.go

func init() { Register(Crypto) }

// Crypto is the "crypto" host module: content digests for checksum manifests
// (SHA256SUMS for release assets) and verifying downloads. Digests only — not a
// general crypto toolkit (no HMAC, encryption, or signing). SHA-256/512 are the
// strong defaults; SHA-1 and MD5 exist for interop with legacy checksums and are
// not collision-resistant — never use them for anything security-relevant.
var Crypto = Module{
	Name: "crypto",
	Doc:  "Content digests (SHA-256/512; SHA-1 and MD5 for legacy-checksum interop).",
	Methods: []Method{
		{
			Name:    "sha256_hex",
			Doc:     "Return the lowercase hex SHA-256 digest of data.",
			Args:    []Arg{{Name: "data", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoSha256Hex,
		},
		{
			Name:    "sha256_file",
			Doc:     "Return the lowercase hex SHA-256 digest of the file at path.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoSha256File,
		},
		{
			Name:    "sha512_hex",
			Doc:     "Return the lowercase hex SHA-512 digest of data.",
			Args:    []Arg{{Name: "data", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoSha512Hex,
		},
		{
			Name:    "sha512_file",
			Doc:     "Return the lowercase hex SHA-512 digest of the file at path.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoSha512File,
		},
		{
			Name:    "sha1_hex",
			Doc:     "Return the lowercase hex SHA-1 digest of data. For interop with legacy/git checksums only — SHA-1 is not collision-resistant; use sha256 for anything security-relevant.",
			Args:    []Arg{{Name: "data", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoSha1Hex,
		},
		{
			Name:    "sha1_file",
			Doc:     "Return the lowercase hex SHA-1 digest of the file at path. For interop with legacy/git checksums only — SHA-1 is not collision-resistant; use sha256 for anything security-relevant.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoSha1File,
		},
		{
			Name:    "md5_hex",
			Doc:     "Return the lowercase hex MD5 digest of data. For interop with legacy checksum manifests only — MD5 is broken; use sha256 for anything security-relevant.",
			Args:    []Arg{{Name: "data", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoMd5Hex,
		},
		{
			Name:    "md5_file",
			Doc:     "Return the lowercase hex MD5 digest of the file at path. For interop with legacy checksum manifests only — MD5 is broken; use sha256 for anything security-relevant.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoMd5File,
		},
	},
}

// hashHex returns the lowercase hex digest of data using the hash from newHash.
func hashHex(newHash func() hash.Hash, data string) string {
	h := newHash()
	_, _ = h.Write([]byte(data)) // hash.Hash.Write never returns an error
	return hex.EncodeToString(h.Sum(nil))
}

// hashFile returns the lowercase hex digest of the file at path, streaming it so
// large files don't load into memory. label names the op in errors. The sandbox
// read policy is enforced, matching archive.* and fs.read_file.
func hashFile(ctx context.Context, label string, newHash func() hash.Hash, path string) (string, error) {
	if p := sandbox.FromContext(ctx); p != nil {
		if err := p.CheckRead(path); err != nil {
			return "", fmt.Errorf("%s: %w", label, err)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", label, err)
	}
	defer f.Close()

	h := newHash()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("%s: %w", label, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CryptoSha256Hex returns the lowercase hex SHA-256 digest of data.
func CryptoSha256Hex(_ context.Context, data string) (string, error) {
	return hashHex(sha256.New, data), nil
}

// CryptoSha256File returns the lowercase hex SHA-256 digest of the file at path.
func CryptoSha256File(ctx context.Context, path string) (string, error) {
	return hashFile(ctx, "crypto.sha256_file", sha256.New, path)
}

// CryptoSha512Hex returns the lowercase hex SHA-512 digest of data.
func CryptoSha512Hex(_ context.Context, data string) (string, error) {
	return hashHex(sha512.New, data), nil
}

// CryptoSha512File returns the lowercase hex SHA-512 digest of the file at path.
func CryptoSha512File(ctx context.Context, path string) (string, error) {
	return hashFile(ctx, "crypto.sha512_file", sha512.New, path)
}

// CryptoSha1Hex returns the lowercase hex SHA-1 digest of data (legacy interop).
func CryptoSha1Hex(_ context.Context, data string) (string, error) {
	return hashHex(sha1.New, data), nil
}

// CryptoSha1File returns the lowercase hex SHA-1 digest of the file at path (legacy interop).
func CryptoSha1File(ctx context.Context, path string) (string, error) {
	return hashFile(ctx, "crypto.sha1_file", sha1.New, path)
}

// CryptoMd5Hex returns the lowercase hex MD5 digest of data (legacy interop).
func CryptoMd5Hex(_ context.Context, data string) (string, error) {
	return hashHex(md5.New, data), nil
}

// CryptoMd5File returns the lowercase hex MD5 digest of the file at path (legacy interop).
func CryptoMd5File(ctx context.Context, path string) (string, error) {
	return hashFile(ctx, "crypto.md5_file", md5.New, path)
}
