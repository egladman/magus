package playground

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeShare_roundTrips(t *testing.T) {
	cases := map[string]string{
		"empty":   "",
		"ascii":   "import \"magus\";\nexport fun ci(ctx: magus\\Context, args: [str]) > void {}\n",
		"unicode": "// magusfile — runs entirely in your browser ✨\nbuzz fibo(20)\n",
		"large":   strings.Repeat("export fun target(ctx: magus\\Context, args: [str]) > void { magus.info(\"hi\"); }\n", 500),
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			enc, err := EncodeShare(src)
			require.NoError(t, err)
			require.True(t, strings.HasPrefix(enc, shareVersion), "payload should carry the version tag")

			got, ok := DecodeShare(enc)
			require.True(t, ok, "a well-formed payload should decode")
			assert.Equal(t, src, got)
		})
	}
}

func TestEncodeShare_isURLFragmentSafe(t *testing.T) {
	// base64url uses only [A-Za-z0-9_-] (no '+', '/', or '=' padding), so the
	// payload can sit in a URL fragment without any percent-escaping.
	enc, err := EncodeShare("buzz fibo(20)")
	require.NoError(t, err)
	for _, r := range enc {
		safe := r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_'
		assert.Truef(t, safe, "payload contains URL-unsafe rune %q", r)
	}
}

func TestDecodeShare_rejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"",          // no version, nothing to decode
		"1",         // version only, empty (and invalid) deflate
		"1!!!",      // valid version, invalid base64
		"2AAAA",     // unknown version
		"AAAA",      // base64 with no version prefix
		"1Zm9vYmFy", // valid base64, but not a DEFLATE stream
	} {
		t.Run(bad, func(t *testing.T) {
			_, ok := DecodeShare(bad)
			assert.False(t, ok, "malformed input should not decode")
		})
	}
}
