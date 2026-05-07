package extracrypto

import (
	"context"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
)

// TestExtraCrypto exercises the module from Buzz the way a spell uses it: HMAC
// against a known vector, the byte-list output chaining as the next key, and
// base64 round-tripping. The chain test reproduces AWS's published SigV4
// signing-key derivation vector, proving the str→bytes→bytes→hex path is
// byte-exact and AWS-correct (not merely self-consistent).
func TestExtraCrypto(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx)
	defer sess.Close()
	sess.SetSyntheticModule("magus/extra/crypto", Register(ctx, sess))

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
    const kDate    = xc.hmacSha256("AWS4wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "20150830");
    const kRegion  = xc.hmacSha256(kDate, "us-east-1");
    const kService = xc.hmacSha256(kRegion, "iam");
    return xc.hmacSha256Hex(kService, "aws4_request");
}

export fun b64() > str { return xc.base64Encode("hello"); }
export fun b64roundtrip() > str { return xc.base64Encode(xc.base64Decode("aGVsbG8=")); }
`
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatalf("exec: %v", err)
	}
	call := func(fn string) string {
		t.Helper()
		rv, err := sess.CallValue(ctx, sess.Exports()[fn], nil)
		if err != nil {
			t.Fatalf("%s: %v", fn, err)
		}
		return rv.AsString()
	}

	if got, want := call("hmacHex"), "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"; got != want {
		t.Errorf("hmacSha256Hex = %q, want %q", got, want)
	}
	if got, want := call("awsSigningKeyHex"), "2c94c0cf5378ada6887f09bb697df8fc0affdb34ba1cdd5bda32b664bd55b73c"; got != want {
		t.Errorf("AWS signing key = %q, want %q (chain corrupted a byte)", got, want)
	}
	if got, want := call("b64"), "aGVsbG8="; got != want {
		t.Errorf("base64Encode = %q, want %q", got, want)
	}
	if got, want := call("b64roundtrip"), "aGVsbG8="; got != want {
		t.Errorf("base64 round-trip = %q, want %q", got, want)
	}
}
