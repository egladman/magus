package std

import (
	"context"
	"crypto/ed25519"
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
	"strings"

	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module crypto -lang buzz -out ../internal/interp/bindings/gen/crypto.go

func init() { Register(Crypto) }

// Crypto is the "crypto" host module: content digests for checksum manifests
// (SHA256SUMS for release assets), Ed25519 signing, and HMAC. Not a general
// crypto toolkit - there is no encryption. SHA-256/512 are the strong defaults;
// SHA-1 and MD5 exist for interop with legacy checksums and are not
// collision-resistant - never use them for anything security-relevant.
//
// The HMAC and byte-list methods moved here from
// internal/interp/bindings/crypto_bytes.go once the descriptor gained a byte-list
// type tag. They worked as undeclared companions but were invisible to
// `magus describe modules`, the knowledge graph and the docs, and untyped in the
// checker - reachable only by knowing they existed.
//
// Signing is here so a magusfile can publish a signed artifact without a Go program
// in the middle. Two things about its shape:
//
// The ALGORITHM is a parameter rather than part of the method name, so a second
// algorithm is a case in signAlgorithm rather than four more methods. The
// sign/verify/public_key methods below declare alg's Arg.Enum as "SignAlgorithm";
// checkAlg is the runtime half, rejecting anything but SignEd25519 by name.
//
// The KEY is named by environment variable rather than passed as a value: a key
// that never becomes a Buzz string cannot be interpolated into a log line, an
// error, or a captured run output.
var Crypto = Module{
	Name: "crypto",
	Doc:  "Content digests (SHA-256/512; SHA-1 and MD5 for legacy-checksum interop) and Ed25519 signing.",
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
			Doc:     "Return the lowercase hex SHA-1 digest of data. For interop with legacy/git checksums only - SHA-1 is not collision-resistant; use sha256 for anything security-relevant.",
			Args:    []Arg{{Name: "data", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoSha1Hex,
		},
		{
			Name:    "sha1_file",
			Doc:     "Return the lowercase hex SHA-1 digest of the file at path. For interop with legacy/git checksums only - SHA-1 is not collision-resistant; use sha256 for anything security-relevant.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoSha1File,
		},
		{
			Name: "sign",
			Doc: "Sign data with the private key in the named environment variable and return the lowercase hex signature. alg is \"ed25519\". " +
				"The key is NAMED, never passed: a value that never enters Buzz cannot be interpolated into a log.",
			Args:    []Arg{{Name: "alg", Type: TypeString, Enum: "SignAlgorithm"}, {Name: "data", Type: TypeString}, {Name: "key_env", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoSign,
		},
		{
			Name:    "sign_file",
			Doc:     "Sign the file at path, write the detached signature to path + \".sig\", and return the lowercase hex signature. alg is \"ed25519\".",
			Args:    []Arg{{Name: "alg", Type: TypeString, Enum: "SignAlgorithm"}, {Name: "path", Type: TypeString}, {Name: "key_env", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoSignFile,
		},
		{
			Name:    "verify",
			Doc:     "Report whether sig_hex is a valid signature over data for the hex public key pub_hex. alg is \"ed25519\".",
			Args:    []Arg{{Name: "alg", Type: TypeString, Enum: "SignAlgorithm"}, {Name: "data", Type: TypeString}, {Name: "sig_hex", Type: TypeString}, {Name: "pub_hex", Type: TypeString}},
			Returns: []Ret{{Type: TypeBool}},
			Impl:    CryptoVerify,
		},
		{
			Name:    "public_key",
			Doc:     "Return the lowercase hex PUBLIC key for the private key in the named environment variable, so a publisher can print what its readers must pin. alg is \"ed25519\".",
			Args:    []Arg{{Name: "alg", Type: TypeString, Enum: "SignAlgorithm"}, {Name: "key_env", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoPublicKey,
		},
		{
			Name:    "md5_hex",
			Doc:     "Return the lowercase hex MD5 digest of data. For interop with legacy checksum manifests only - MD5 is broken; use sha256 for anything security-relevant.",
			Args:    []Arg{{Name: "data", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoMd5Hex,
		},
		{
			Name:    "md5_file",
			Doc:     "Return the lowercase hex MD5 digest of the file at path. For interop with legacy checksum manifests only - MD5 is broken; use sha256 for anything security-relevant.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoMd5File,
		},
		{
			Name: "hmac_sha256",
			Doc:  "Return the raw HMAC-SHA256 of data keyed by key, as a BYTE LIST. key and data may each be a str or a byte list, which is what lets one result key the next call - the shape an AWS SigV4 signing chain needs (kDate to kRegion to kService to kSigning). Use hmac_sha256_hex for the final signature you actually send.",
			Args: []Arg{
				{Name: "key", Type: TypeByteSlice},
				{Name: "data", Type: TypeByteSlice},
			},
			Returns: []Ret{{Type: TypeByteSlice}},
			Impl:    CryptoHmacSha256,
		},
		{
			Name: "hmac_sha256_hex",
			Doc:  "Return the lowercase hex HMAC-SHA256 of data keyed by key - the form a signature header carries, once the signing key has been derived with hmac_sha256.",
			Args: []Arg{
				{Name: "key", Type: TypeByteSlice},
				{Name: "data", Type: TypeByteSlice},
			},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoHmacSha256Hex,
		},
		{
			Name:    "base64_encode_bytes",
			Doc:     "Encode raw bytes as standard (padded) base64. The byte-list counterpart to encoding/base64's encode, for data that came from another byte-level call and must not round-trip through a rune-oriented str.",
			Args:    []Arg{{Name: "data", Type: TypeByteSlice}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    CryptoBase64EncodeBytes,
		},
		{
			Name:    "base64_decode_bytes",
			Doc:     "Decode standard (padded) base64 into a byte list; errors on invalid input. Returns bytes rather than a str so arbitrary binary survives - a decoded key or archive would not.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeByteSlice}},
			Impl:    CryptoBase64DecodeBytes,
		},
	},
}

// CryptoHmacSha256 returns the raw HMAC-SHA256 of data keyed by key.
//
// Bytes in and bytes out, which is the whole point: the result keys the next call
// in a signing chain, and a str would not survive the round trip. It moved here
// from internal/interp/bindings/crypto_bytes.go once a byte-list type tag existed
// to declare it with - as an undeclared companion it worked but was invisible to
// `magus describe modules`, the knowledge graph and the docs, and untyped in the
// checker.
func CryptoHmacSha256(_ context.Context, key, data []byte) ([]byte, error) {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write(data) // hash.Hash.Write never returns an error
	return m.Sum(nil), nil
}

// CryptoHmacSha256Hex returns the hex HMAC-SHA256 of data keyed by key.
func CryptoHmacSha256Hex(ctx context.Context, key, data []byte) (string, error) {
	sum, err := CryptoHmacSha256(ctx, key, data)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sum), nil
}

// CryptoBase64EncodeBytes encodes raw bytes as standard padded base64.
func CryptoBase64EncodeBytes(_ context.Context, data []byte) (string, error) {
	return base64.StdEncoding.EncodeToString(data), nil
}

// CryptoBase64DecodeBytes decodes standard padded base64 into raw bytes.
func CryptoBase64DecodeBytes(_ context.Context, s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("crypto.base64_decode_bytes: %w", err)
	}
	return b, nil
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
	path = resolvePath(ctx, path)
	if p := sandbox.FromContext(ctx); p != nil {
		if err := p.CheckReadCtx(ctx, path); err != nil {
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

// SignEd25519 is the only algorithm signing accepts today.
const SignEd25519 = "ed25519"

// checkAlg rejects an unknown algorithm by NAME, listing what is accepted. A typo
// in a string parameter is the cost of not having an enum here, so the error has to
// carry what an enum would have offered as completion.
func checkAlg(alg string) error {
	if alg != SignEd25519 {
		return fmt.Errorf("crypto: unknown signing algorithm %q (want %q)", alg, SignEd25519)
	}
	return nil
}

// signingKey reads the private key named by keyEnv for the given algorithm.
//
// Taking the NAME rather than the key is the whole point of this shape. magus
// captures the output of every run, and a magusfile that had to hold a signing key
// in a variable would be one interpolation away from writing it into a log that
// outlives the build. The key is read here, used here, and never crosses into Buzz.
func signingKey(alg, keyEnv string) (ed25519.PrivateKey, error) {
	if err := checkAlg(alg); err != nil {
		return nil, err
	}
	if keyEnv == "" {
		return nil, fmt.Errorf("crypto: key_env is required (name the variable holding the key, not the key)")
	}
	keyHex := os.Getenv(keyEnv)
	if keyHex == "" {
		return nil, fmt.Errorf("crypto: %s is not set", keyEnv)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(keyHex))
	if err != nil {
		// Deliberately does not echo the value: a decode error on a secret must not
		// print the secret back.
		return nil, fmt.Errorf("crypto: %s is not valid hex", keyEnv)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("crypto: %s must be %d bytes (%d hex chars), got %d bytes",
			keyEnv, ed25519.PrivateKeySize, ed25519.PrivateKeySize*2, len(raw))
	}
	return ed25519.PrivateKey(raw), nil
}

// CryptoSign signs data with the key named by keyEnv, returning hex.
func CryptoSign(_ context.Context, alg, data, keyEnv string) (string, error) {
	key, err := signingKey(alg, keyEnv)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(ed25519.Sign(key, []byte(data))), nil
}

// CryptoSignFile signs the file at path and writes path + ".sig".
//
// The signature is written as RAW bytes, not hex: that is the format every reader
// of a magus signature already expects, from the installer's openssl pkeyutl to
// internal/selfupdate. The hex return is for printing.
func CryptoSignFile(ctx context.Context, alg, path, keyEnv string) (string, error) {
	key, err := signingKey(alg, keyEnv)
	if err != nil {
		return "", err
	}
	full := resolvePath(ctx, path)
	// Read check first, tracing gate second: reads are left alone even in a dry
	// run (types/trace.go), so a sandbox denial is still reported rather than
	// silently skipped by the trace stub.
	if err := checkRead(ctx, full); err != nil {
		return "", err
	}
	if types.Tracing(ctx) {
		return "", nil
	}
	if err := checkWrite(ctx, full+".sig"); err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("crypto.sign_file: read %s: %w", path, err)
	}
	sig := ed25519.Sign(key, data)
	if err := os.WriteFile(full+".sig", sig, 0o644); err != nil {
		return "", fmt.Errorf("crypto.sign_file: write %s.sig: %w", path, err)
	}
	return hex.EncodeToString(sig), nil
}

// CryptoVerify reports whether sigHex verifies data under pubHex.
func CryptoVerify(_ context.Context, alg, data, sigHex, pubHex string) (bool, error) {
	if err := checkAlg(alg); err != nil {
		return false, err
	}
	sig, err := hex.DecodeString(strings.TrimSpace(sigHex))
	if err != nil {
		return false, fmt.Errorf("crypto.verify: signature is not hex: %w", err)
	}
	pub, err := hex.DecodeString(strings.TrimSpace(pubHex))
	if err != nil {
		return false, fmt.Errorf("crypto.verify: public key is not hex: %w", err)
	}
	// ed25519.Verify panics on a wrong-length key, so the length is checked rather
	// than discovered.
	if len(pub) != ed25519.PublicKeySize {
		return false, fmt.Errorf("crypto.verify: public key is %d bytes, want %d", len(pub), ed25519.PublicKeySize)
	}
	return ed25519.Verify(ed25519.PublicKey(pub), []byte(data), sig), nil
}

// CryptoPublicKey returns the hex public half of the key named by keyEnv.
func CryptoPublicKey(_ context.Context, alg, keyEnv string) (string, error) {
	key, err := signingKey(alg, keyEnv)
	if err != nil {
		return "", err
	}
	pub, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		return "", fmt.Errorf("crypto: %s did not yield an ed25519 public half", keyEnv)
	}
	return hex.EncodeToString(pub), nil
}
