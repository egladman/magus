package bindings

import (
	"context"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCryptoBytes exercises registerCryptoBytes from Buzz the way a spell uses it:
// HMAC against a known vector, the byte-list output chaining as the next key, and
// base64 round-tripping. The chain test reproduces AWS's published SigV4
// signing-key derivation vector, proving the str→bytes→bytes→hex path is
// byte-exact and AWS-correct (not merely self-consistent). These primitives are
// merged onto the bare `crypto` module, so the test imports them that way.
func TestCryptoBytes(t *testing.T) {
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	sess.SetSyntheticModule("crypto", registerCryptoBytes())

	const src = `
import "crypto" as xc;

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
