// Package extracrypto is an optional Buzz module ("magus/extra/crypto") that adds
// the keyed-hash and base64 primitives needed to sign API requests (e.g. AWS
// SigV4) from a spell — the byte-level companion to the digest-only std crypto
// module, in the same hand-written, one-VM style as extra/http.
//
// Binary values cross the boundary as a Buzz [int] byte list — the representation
// Buffer.write consumes and Buffer.toList produces — so a script never has to
// smuggle raw bytes through a rune-oriented string. Where an input is textual (an
// HMAC message, a key seed) a plain str is also accepted and taken as its bytes,
// so the common case (`hmacSha256("AWS4"+secret, datestamp)`) stays readable while
// the chained outputs remain pure byte lists.
package extracrypto

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	buzz "github.com/egladman/gopherbuzz"
)

// Register builds the "magus/extra/crypto" module map. The host installs it with
// sess.SetSyntheticModule("magus/extra/crypto", Register(ctx, sess)) so a script
// reaches it via `import "magus/extra/crypto"`.
func Register(_ context.Context, _ *buzz.Session) buzz.Value {
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
