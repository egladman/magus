package playground

import (
	"encoding/base64"
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
		"large":   strings.Repeat("export fun target(ctx: magus\\Context, args: [str]) > void { magus.log.info(\"hi\"); }\n", 500),
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			enc, err := EncodeShare(src)
			require.NoError(t, err)
			require.True(t, strings.HasPrefix(enc, shareDeflate), "payload should carry the version tag")

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
		"0!!!",      // valid version, invalid base64
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

// The uncompressed variant is a cross-language contract: docs/site/tour.buzz emits
// it from Buzz at render time and docs/src/site/run-example.ts from the browser,
// and neither can call this package. The literal is what those two produce, so a
// change to the format here fails rather than silently orphaning their deep links.
func TestDecodeShare_rawVariantIsTaggedBase64url(t *testing.T) {
	const src = `import "magus";`
	raw := shareRaw + base64.RawURLEncoding.EncodeToString([]byte(src))
	require.Equal(t, "0aW1wb3J0ICJtYWd1cyI7", raw)

	got, ok := DecodeShare(raw)
	require.True(t, ok, "a docs deep link should decode")
	assert.Equal(t, src, got)
}

// Share writes a fragment, boot reads one. They are the two halves of a single
// feature living in two files, and this is the seam where they were allowed to
// drift apart before: a codec change on either side that the other does not follow
// fails here.
func TestShareFragment_roundTripsThroughSourceFromFragment(t *testing.T) {
	const src = "// magusfile — shared by link ✨\nbuzz fibo(20)\n"
	frag, err := ShareFragment(src)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(frag, "#source="), "got %q", frag)

	got, ok := SourceFromFragment(frag)
	require.True(t, ok, "the fragment Share produces must decode on boot")
	assert.Equal(t, src, got)
}

func TestSourceFromFragment(t *testing.T) {
	shared, err := ShareFragment("buzz fibo(20)")
	require.NoError(t, err)
	payload := strings.TrimPrefix(shared, "#source=")

	for name, tc := range map[string]struct {
		hash string
		want string
		ok   bool
	}{
		"share button":      {shared, "buzz fibo(20)", true},
		"no leading hash":   {"source=" + payload, "buzz fibo(20)", true},
		"docs deep link":    {"#source=" + shareRaw + base64.RawURLEncoding.EncodeToString([]byte("buzz fibo(20)")), "buzz fibo(20)", true},
		"among other keys":  {"#tab=console&source=" + payload + "&x=1", "buzz fibo(20)", true},
		"empty":             {"", "", false},
		"hash only":         {"#", "", false},
		"no source key":     {"#tab=console", "", false},
		"key without value": {"#source", "", false},
		"undecodable":       {"#source=1!!!", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := SourceFromFragment(tc.hash)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// The name a save lands under is the label the file bar is already showing, so the
// file the visitor gets is the file the page told them they had. The fallback cases
// are the ones where that label is not a name at all.
func TestDownloadName(t *testing.T) {
	for name, tc := range map[string]struct{ label, want string }{
		"the seeded example": {"magusfile.buzz", "magusfile.buzz"},
		"padded label":       {"  magusfile.buzz\n", "magusfile.buzz"},
		"suffix added":       {"magusfile", "magusfile.buzz"},
		"no label":           {"", "playground.buzz"},
		"blank label":        {"   ", "playground.buzz"},
		"a path":             {"a/b.buzz", "playground.buzz"},
		"a windows path":     {`a\b.buzz`, "playground.buzz"},
		"hidden file":        {".buzz", "playground.buzz"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, DownloadName(tc.label))
		})
	}
}
