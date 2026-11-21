package emit

import (
	"strings"
	"testing"

	"github.com/egladman/magus/libs/termkit/screen"
	screenbindings "github.com/egladman/magus/std/termkit/gen/screen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedRecordsRejectUnknownFields(t *testing.T) {
	cases := []struct {
		name   string
		decode func(any) (any, error)
		key    string
		value  any
		want   any
		zero   any
	}{
		{
			name: "theme", decode: func(v any) (any, error) { return screenbindings.DecodeTheme(v) },
			key: "background", value: "#ffffff", want: screen.Theme{Background: "#ffffff"}, zero: screen.Theme{},
		},
		{
			name: "options", decode: func(v any) (any, error) { return screenbindings.DecodeSVGOptions(v) },
			key: "fontSize", value: 18, want: screen.SVGOptions{FontSize: 18}, zero: screen.SVGOptions{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("unknown field", func(t *testing.T) {
				got, err := tc.decode(map[string]any{tc.key: tc.value, "typo": true})
				require.EqualError(t, err, `unknown field "typo"`)
				assert.Equal(t, tc.zero, got)
			})
			t.Run("field casing", func(t *testing.T) {
				key := strings.ToUpper(tc.key[:1]) + tc.key[1:]
				got, err := tc.decode(map[string]any{key: tc.value})
				require.EqualError(t, err, "unknown field "+`"`+key+`"`)
				assert.Equal(t, tc.zero, got)
			})
			t.Run("stable error order", func(t *testing.T) {
				got, err := tc.decode(map[string]any{"zeta": true, "alpha": true})
				require.EqualError(t, err, `unknown field "alpha"`)
				assert.Equal(t, tc.zero, got)
			})
			t.Run("partial record", func(t *testing.T) {
				got, err := tc.decode(map[string]any{tc.key: tc.value})
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
			t.Run("empty record", func(t *testing.T) {
				got, err := tc.decode(map[string]any{})
				require.NoError(t, err)
				assert.Equal(t, tc.zero, got)
			})
		})
	}
}

func TestGeneratedRecordRejectsUnknownNestedFields(t *testing.T) {
	got, err := screenbindings.DecodeSVGOptions(map[string]any{
		"theme": map[string]any{"unexpected": "#ffffff"},
	})

	require.EqualError(t, err, `theme: unknown field "unexpected"`)
	assert.Equal(t, screen.SVGOptions{}, got)
}
