package json

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDurationMarshalsAsString(t *testing.T) {
	b, err := Marshal(struct {
		TTL time.Duration `json:"ttl"`
	}{6 * time.Hour})
	require.NoError(t, err)
	assert.JSONEq(t, `{"ttl":"6h0m0s"}`, string(b))
}

func TestMarshalSortsMapKeys(t *testing.T) {
	b, err := Marshal(map[string]int{"b": 1, "a": 2})
	require.NoError(t, err)
	assert.Equal(t, `{"a":2,"b":1}`, string(b))
}

func TestMarshalKeepsNilCollectionsNull(t *testing.T) {
	b, err := Marshal(struct {
		Items []string       `json:"items"`
		Attrs map[string]int `json:"attrs"`
	}{})
	require.NoError(t, err)
	assert.Equal(t, `{"items":null,"attrs":null}`, string(b))
}

// TestMarshalLeavesHTMLSignificantBytesAlone is the assertion the removed v1 fallback
// would have failed, and the one nothing made before: v1's Marshal rewrites <, > and & to
// their \u00XX form and v2 writes them as typed.
//
// It reads as a triviality about three characters. It is not - it is the byte-for-byte
// contract for everything this package writes, and the two spellings are how the v0.4.3
// release index shipped 336 lines of gen/knowledge-graph.json that the next gate read as
// drift with no source change behind it. Every heading in the doc graph carrying a `>` is
// one of these, and docs/gen/public/release/index.json is signed over exactly these bytes.
func TestMarshalLeavesHTMLSignificantBytesAlone(t *testing.T) {
	b, err := Marshal(map[string]string{"label": "magus query output <ref> & more"})
	require.NoError(t, err)
	assert.Equal(t, `{"label":"magus query output <ref> & more"}`, string(b),
		"the encoder must not depend on how the binary was built")
}
