package secret

import (
	"bytes"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	json "github.com/egladman/magus/internal/json"
)

// TestValueMasksEveryRenderingPath is the whole point of the type. Redact-at-write
// compares against what fmt renders while a handler emits what its ENCODER produces, and
// those differ - which is how a []byte attr reached stdout base64'd and decodable. A
// value that masks itself needs no interceptor to guess the encoder.
//
// Delete any single method on Value and the matching row here fails.
func TestValueMasksEveryRenderingPath(t *testing.T) {
	const plaintext = "ghp_probe_token_value"
	v := NewValue(plaintext)

	for _, tc := range []struct {
		name string
		got  string
	}{
		{"String()", v.String()},
		{"fmt %s", fmt.Sprintf("%s", v)},
		{"fmt %v", fmt.Sprintf("%v", v)},
		{"fmt %q", fmt.Sprintf("%q", v)},
		{"fmt %x", fmt.Sprintf("%x", v)},
		{"fmt %#v", fmt.Sprintf("%#v", v)},
		{"fmt %+v", fmt.Sprintf("%+v", v)},
		{"in a struct", fmt.Sprintf("%v", struct{ Token Value }{v})},
		{"in a slice", fmt.Sprintf("%v", []Value{v})},
		{"in a map", fmt.Sprintf("%v", map[string]Value{"token": v})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotContains(t, tc.got, plaintext, "the plaintext survived %s", tc.name)
			assert.Contains(t, tc.got, mask)
		})
	}

	t.Run("json", func(t *testing.T) {
		// The path that defeated the redacting handler: a value whose fmt form looks
		// clean while its marshalled form carries the credential. Through magus's own
		// shared JSON package, which is what actually marshals in production - and which
		// TestProductionCodeUsesSharedJSON requires over encoding/json.
		b, err := json.Marshal(struct {
			Token Value `json:"Token"`
		}{v})
		require.NoError(t, err)
		assert.NotContains(t, string(b), plaintext)
		assert.JSONEq(t, `{"Token":"`+mask+`"}`, string(b))
	})

	t.Run("slog", func(t *testing.T) {
		// Through a REAL handler with no resolver on the context, so nothing but the
		// value itself is doing the masking.
		var buf bytes.Buffer
		slog.New(slog.NewJSONHandler(&buf, nil)).InfoContext(t.Context(), "using credential", "token", v)
		assert.NotContains(t, buf.String(), plaintext)
		assert.Contains(t, buf.String(), mask)
	})

	t.Run("Reveal is the only way out", func(t *testing.T) {
		assert.Equal(t, plaintext, v.Reveal())
	})
}

// TestValueZeroAndLen: a caller can ask about a value without unwrapping it. The
// short-value warning needs the length, and "did anything resolve" needs emptiness.
func TestValueZeroAndLen(t *testing.T) {
	assert.True(t, Value{}.IsZero())
	assert.False(t, NewValue("x").IsZero())
	assert.Equal(t, 5, NewValue("abcde").Len())
	// Len must not be inferable from the mask, which is fixed-width by design.
	assert.Equal(t, NewValue("a-very-long-credential").String(), NewValue("short").String())
}
