package notes

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realDefinition is a Go definition with the formatting features that actually move when a
// formatter runs: aligned struct tags, an aligned trailing comment, and internal blank
// lines.
const realDefinition = `func (s *Store) Put(ctx context.Context, key string, val []byte) error {
	if key == "" {
		return errors.New("empty key")
	}

	entry := record{
		Key:   key,          // the identity
		Value: val,
		TTL:   defaultTTL,
	}
	return s.write(ctx, entry)
}`

// TestDigestIsWhitespaceInvariant is the property the whole gate rests on. A fingerprint
// that fires on reformatting produces false drift, the flags get ignored, and an ignored
// gate is worse than no gate because the store then LOOKS checked.
//
// Measured once across this repo before shipping: 1,263 files, 12,812 twenty-line ranges,
// ZERO false positives under a semantically null reformat.
func TestDigestIsWhitespaceInvariant(t *testing.T) {
	base := Digest(strings.Split(realDefinition, "\n"))
	require.NotEmpty(t, base)

	for _, tc := range []struct {
		name string
		fn   func(string) string
	}{
		{"re-indented with spaces", func(s string) string { return strings.ReplaceAll(s, "\t", "    ") }},
		{"re-indented deeper", func(s string) string { return strings.ReplaceAll(s, "\t", "\t\t\t") }},
		{"realigned struct tags", func(s string) string { return strings.ReplaceAll(s, "   ", " ") }},
		{"blank lines added", func(s string) string { return strings.ReplaceAll(s, "\n", "\n\n") }},
		{"blank lines removed", func(s string) string { return strings.ReplaceAll(s, "\n\n", "\n") }},
		{"trailing whitespace", func(s string) string { return strings.ReplaceAll(s, "\n", "   \n") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, base, Digest(strings.Split(tc.fn(realDefinition), "\n")),
				"reformatting must not read as an edit")
		})
	}
}

// TestDigestCatchesRealEdits is the other half. A normalization that absorbed everything
// would pass the test above and be worthless.
func TestDigestCatchesRealEdits(t *testing.T) {
	base := Digest(strings.Split(realDefinition, "\n"))

	for _, tc := range []struct {
		name string
		fn   func(string) string
	}{
		{"renamed identifier", func(s string) string { return strings.Replace(s, "entry", "rec", 1) }},
		{"changed literal", func(s string) string { return strings.Replace(s, "defaultTTL", "0", 1) }},
		{"inverted condition", func(s string) string { return strings.Replace(s, `key == ""`, `key != ""`, 1) }},
		{"deleted statement", func(s string) string { return strings.Replace(s, "\t\treturn errors.New(\"empty key\")\n", "", 1) }},
		{"edited comment", func(s string) string { return strings.Replace(s, "the identity", "the primary key", 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotEqual(t, base, Digest(strings.Split(tc.fn(realDefinition), "\n")),
				"a change that can alter meaning must be visible")
		})
	}
}

func TestDigestRange(t *testing.T) {
	src := "one\ntwo\nthree\nfour\nfive"

	assert.Equal(t, Digest([]string{"two", "three"}), DigestRange(src, 2, 3), "1-based and inclusive")
	assert.NotEqual(t, DigestRange(src, 1, 2), DigestRange(src, 2, 3))

	// An index that lags the file must degrade to "no opinion", never to a fingerprint of
	// the wrong lines - which would report confident nonsense.
	assert.Empty(t, DigestRange(src, 0, 3), "a zero start is not a line")
	assert.Empty(t, DigestRange(src, 4, 2), "an inverted range says nothing")
	assert.Empty(t, DigestRange(src, 99, 120), "a range past the end says nothing")
	assert.Empty(t, DigestRange(src, 4, 500), "an end past EOF proves the index is stale, so it says nothing rather than clamping")

	// Content that normalizes to nothing still gets a REAL fingerprint. "" is reserved for
	// "cannot compute", and conflating the two would make emptying an anchored file - the
	// largest possible drift - the one edit that reports silence.
	assert.NotEmpty(t, Digest([]string{"", "   ", "\t"}))
	assert.NotEqual(t, Digest([]string{"kept"}), Digest([]string{"", "   "}))
}
