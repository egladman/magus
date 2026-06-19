package bindings

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/egladman/gopherbuzz/vm"
)

// registerCryptoBytes builds the byte-level companion to the declarative crypto
// module: the keyed-hash and base64 primitives needed to sign API requests (e.g.
// AWS SigV4) from a spell. They can't be expressed as declarative std.Methods —
// an input may be a str OR a [int] byte list, and an output is a raw byte list
// that chains as the next call's key — so they're hand-written against the
// gopherbuzz value API and merged onto the generated `crypto` module map at bind
// time (see registerHostModules). This is VM glue, which is why it lives here and
// not on the VM-agnostic std surface.
//
// Binary values cross the boundary as a Buzz [int] byte list — the representation
// Buffer.write consumes and Buffer.toList produces — so a script never has to
// smuggle raw bytes through a rune-oriented string. Where an input is textual (an
// HMAC message, a key seed) a plain str is also accepted and taken as its bytes,
// so the common case (`crypto.hmacSha256("AWS4"+secret, datestamp)`) stays
// readable while the chained outputs remain pure byte lists.
func registerCryptoBytes() vm.Value {
	m := vm.NewMap()

	// hmacSha256(key, data) -> [int]
	// Raw HMAC-SHA256 of data keyed by key, as a byte list. key and data may each
	// be a str (taken as its bytes) or a [int] byte list, so a result chains
	// directly as the key of a further hmacSha256 — the shape AWS SigV4's
	// signing-key derivation needs (kDate→kRegion→kService→kSigning).
	m.MapSet("hmacSha256", vm.DirectValue("crypto.hmacSha256", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		sum, err := cryptoHMACSum(args)
		if err != nil {
			return vm.Null, err
		}
		return cryptoBytesValue(sum), nil
	}))

	// hmacSha256Hex(key, data) -> str
	// Lowercase hex HMAC-SHA256 — for the final signature value, once the signing
	// key has been derived with hmacSha256.
	m.MapSet("hmacSha256Hex", vm.DirectValue("crypto.hmacSha256Hex", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		sum, err := cryptoHMACSum(args)
		if err != nil {
			return vm.Null, err
		}
		return vm.StrValue(hex.EncodeToString(sum)), nil
	}))

	// base64Encode(data) -> str
	// Standard (RFC 4648) base64 with padding. data may be a str or a [int] byte list.
	m.MapSet("base64Encode", vm.DirectValue("crypto.base64Encode", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		b, err := cryptoBytesArg(args, 0)
		if err != nil {
			return vm.Null, fmt.Errorf("crypto.base64Encode: %w", err)
		}
		return vm.StrValue(base64.StdEncoding.EncodeToString(b)), nil
	}))

	// base64Decode(str) -> [int]
	// Decode standard (RFC 4648) padded base64 into a byte list; errors on invalid input.
	m.MapSet("base64Decode", vm.DirectValue("crypto.base64Decode", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 1 || !args[0].IsStr() {
			return vm.Null, fmt.Errorf("crypto.base64Decode: requires a str argument")
		}
		b, err := base64.StdEncoding.DecodeString(args[0].AsString())
		if err != nil {
			return vm.Null, fmt.Errorf("crypto.base64Decode: %w", err)
		}
		return cryptoBytesValue(b), nil
	}))

	return m
}

// cryptoHMACSum computes HMAC-SHA256(key, data) from the first two args, each a str or
// a [int] byte list.
func cryptoHMACSum(args []vm.Value) ([]byte, error) {
	key, err := cryptoBytesArg(args, 0)
	if err != nil {
		return nil, fmt.Errorf("crypto.hmacSha256: key: %w", err)
	}
	data, err := cryptoBytesArg(args, 1)
	if err != nil {
		return nil, fmt.Errorf("crypto.hmacSha256: data: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data) // hash.Hash.Write never errors
	return mac.Sum(nil), nil
}

// cryptoBytesArg reads arg i as raw bytes: a str (its bytes) or a [int] byte list.
func cryptoBytesArg(args []vm.Value, i int) ([]byte, error) {
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

// cryptoBytesValue wraps raw bytes as a Buzz [int] byte list (Buffer-compatible).
func cryptoBytesValue(b []byte) vm.Value {
	items := make([]vm.Value, len(b))
	for i, x := range b {
		items[i] = vm.IntValue(int64(x))
	}
	return vm.ListValue(items)
}
