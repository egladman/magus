package std

import (
	"context"
	"crypto/hmac"
	"crypto/md5"  //nolint:gosec // G501: MD5 is exposed for interop with legacy checksum manifests, not security.
	"crypto/sha1" //nolint:gosec // G505: SHA-1 is exposed for interop with legacy/git checksums, not security.
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"

	buzz "github.com/egladman/gopherbuzz"
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

// RegisterExtraCrypto builds the "magus/extra/crypto" module map: the keyed-hash
// and base64 primitives needed to sign API requests (e.g. AWS SigV4) from a spell
// — the byte-level companion to the digest-only methods above. Unlike those,
// these can't be expressed as declarative Methods: an input may be a str OR a
// [int] byte list, and an output is a raw byte list that chains as the next
// call's key, so the module is hand-written against the gopherbuzz value API and
// merged into the generated crypto map at bind time. The host installs it with
// sess.SetSyntheticModule("magus/extra/crypto", RegisterExtraCrypto(ctx, sess)) so
// a script reaches it via `import "magus/extra/crypto"`.
//
// Binary values cross the boundary as a Buzz [int] byte list — the representation
// Buffer.write consumes and Buffer.toList produces — so a script never has to
// smuggle raw bytes through a rune-oriented string. Where an input is textual (an
// HMAC message, a key seed) a plain str is also accepted and taken as its bytes,
// so the common case (`hmacSha256("AWS4"+secret, datestamp)`) stays readable while
// the chained outputs remain pure byte lists.
func RegisterExtraCrypto(_ context.Context, _ *buzz.Session) buzz.Value {
	m := buzz.NewMap()

	// hmacSha256(key, data) -> [int]
	// Raw HMAC-SHA256 of data keyed by key, as a byte list. key and data may each
	// be a str (taken as its bytes) or a [int] byte list, so a result chains
	// directly as the key of a further hmacSha256 — the shape AWS SigV4's
	// signing-key derivation needs (kDate→kRegion→kService→kSigning).
	m.MapSet("hmacSha256", buzz.DirectValue("extra/crypto.hmacSha256", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		sum, err := hmacSum(args)
		if err != nil {
			return buzz.Null, err
		}
		return bytesValue(sum), nil
	}))

	// hmacSha256Hex(key, data) -> str
	// Lowercase hex HMAC-SHA256 — for the final signature value, once the signing
	// key has been derived with hmacSha256.
	m.MapSet("hmacSha256Hex", buzz.DirectValue("extra/crypto.hmacSha256Hex", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		sum, err := hmacSum(args)
		if err != nil {
			return buzz.Null, err
		}
		return buzz.StrValue(hex.EncodeToString(sum)), nil
	}))

	// base64Encode(data) -> str
	// Standard (RFC 4648) base64 with padding. data may be a str or a [int] byte list.
	m.MapSet("base64Encode", buzz.DirectValue("extra/crypto.base64Encode", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		b, err := bytesArg(args, 0)
		if err != nil {
			return buzz.Null, fmt.Errorf("extra/crypto.base64Encode: %w", err)
		}
		return buzz.StrValue(base64.StdEncoding.EncodeToString(b)), nil
	}))

	// base64Decode(str) -> [int]
	// Decode standard (RFC 4648) padded base64 into a byte list; errors on invalid input.
	m.MapSet("base64Decode", buzz.DirectValue("extra/crypto.base64Decode", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 1 || !args[0].IsStr() {
			return buzz.Null, fmt.Errorf("extra/crypto.base64Decode: requires a str argument")
		}
		b, err := base64.StdEncoding.DecodeString(args[0].AsString())
		if err != nil {
			return buzz.Null, fmt.Errorf("extra/crypto.base64Decode: %w", err)
		}
		return bytesValue(b), nil
	}))

	return m
}

// hmacSum computes HMAC-SHA256(key, data) from the first two args, each a str or
// a [int] byte list.
func hmacSum(args []buzz.Value) ([]byte, error) {
	key, err := bytesArg(args, 0)
	if err != nil {
		return nil, fmt.Errorf("extra/crypto.hmacSha256: key: %w", err)
	}
	data, err := bytesArg(args, 1)
	if err != nil {
		return nil, fmt.Errorf("extra/crypto.hmacSha256: data: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data) // hash.Hash.Write never errors
	return mac.Sum(nil), nil
}

// bytesArg reads arg i as raw bytes: a str (its bytes) or a [int] byte list.
func bytesArg(args []buzz.Value, i int) ([]byte, error) {
	if i >= len(args) {
		return nil, fmt.Errorf("missing argument %d", i)
	}
	switch v := args[i]; {
	case v.IsStr():
		return []byte(v.AsString()), nil
	case v.IsList():
		items := v.ListItems()
		b := make([]byte, len(items))
		for j, it := range items {
			if !it.IsInt() {
				return nil, fmt.Errorf("byte list must contain ints")
			}
			b[j] = byte(it.AsInt())
		}
		return b, nil
	default:
		return nil, fmt.Errorf("expected a str or [int] byte list")
	}
}

// bytesValue wraps raw bytes as a Buzz [int] byte list (Buffer-compatible).
func bytesValue(b []byte) buzz.Value {
	items := make([]buzz.Value, len(b))
	for i, x := range b {
		items[i] = buzz.IntValue(int64(x))
	}
	return buzz.ListValue(items)
}
