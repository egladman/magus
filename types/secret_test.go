package types

import (
	"bytes"
	"encoding"
	"encoding/gob"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/egladman/magus/internal/json"
)

const probePlaintext = "ghp_probe_token_value"

// TestSecretMasksEveryRenderingPath: delete any single method on Secret and a row fails.
func TestSecretMasksEveryRenderingPath(t *testing.T) {
	v := NewSecret(probePlaintext)
	type holder struct {
		Name  string
		token Secret
	}
	h := holder{Name: "registry", token: v}

	rendered := []struct {
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
		{"in a struct", fmt.Sprintf("%v", struct{ Token Secret }{v})},
		{"in a slice", fmt.Sprintf("%v", []Secret{v})},
		{"in a map", fmt.Sprintf("%v", map[string]Secret{"token": v})},
		{"fmt.Errorf %v", fmt.Errorf("login with %v failed", v).Error()},
		{"wrapped with %w", fmt.Errorf("push: %w", fmt.Errorf("token %s rejected", v)).Error()},
		{"errors.Join", errors.Join(errors.New("first"), fmt.Errorf("token %v", v)).Error()},
		{"recovered panic", recoverPanic(v)},
	}
	for _, tc := range rendered {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotContains(t, tc.got, probePlaintext)
			assert.Contains(t, tc.got, secretMask)
		})
	}

	// fmt calls no method on an unexported field, so there it prints the pointer's address.
	for verb, got := range map[string]string{"%v": fmt.Sprintf("%v", h), "%+v": fmt.Sprintf("%+v", h), "%#v": fmt.Sprintf("%#v", &h)} {
		assert.NotContains(t, got, probePlaintext, "unexported field under %s", verb)
		assert.Contains(t, got, "registry", "unexported field under %s", verb)
	}

	type doc struct {
		Token Secret `json:"token" yaml:"token" toml:"token"`
	}
	encoders := []struct {
		name    string
		marshal func(any) ([]byte, error)
	}{
		{"json", json.Marshal},
		{"yaml.v3", yaml.Marshal},
		{"toml", toml.Marshal},
	}
	for _, enc := range encoders {
		t.Run(enc.name, func(t *testing.T) {
			b, err := enc.marshal(doc{Token: v})
			require.NoError(t, err)
			assert.NotContains(t, string(b), probePlaintext)
			assert.Contains(t, string(b), secretMask)
		})
	}

	for _, handler := range []struct {
		name string
		new  func(*bytes.Buffer) slog.Handler
	}{
		{"slog json", func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) }},
		{"slog text", func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) }},
	} {
		t.Run(handler.name, func(t *testing.T) {
			var buf bytes.Buffer
			slog.New(handler.new(&buf)).InfoContext(t.Context(), "using credential",
				slog.Any("token", v), slog.Any("holder", struct{ Token Secret }{v}))
			assert.NotContains(t, buf.String(), probePlaintext)
			assert.Contains(t, buf.String(), secretMask)
		})
	}

	assert.Equal(t, probePlaintext, v.Reveal())
}

func recoverPanic(v Secret) (got string) {
	defer func() { got = fmt.Sprint(recover()) }()
	panic(v)
}

// TestSecretRefusesDecoding pins the absent decoders: plaintext in a config file or a
// response body must not become a Secret by accident.
func TestSecretRefusesDecoding(t *testing.T) {
	typ := reflect.TypeFor[Secret]()
	assert.False(t, reflect.PointerTo(typ).Implements(reflect.TypeFor[encoding.TextUnmarshaler]()), "Secret must not implement UnmarshalText")
	assert.False(t, reflect.PointerTo(typ).Implements(reflect.TypeFor[interface{ UnmarshalJSON([]byte) error }]()), "Secret must not implement UnmarshalJSON")

	type doc struct {
		Token Secret `json:"token" yaml:"token" toml:"token"`
	}
	var d doc
	require.Error(t, json.Unmarshal([]byte(`{"token":"`+probePlaintext+`"}`), &d))
	require.Error(t, yaml.Unmarshal([]byte("token: "+probePlaintext+"\n"), &d))
	require.Error(t, toml.Unmarshal([]byte(`token = "`+probePlaintext+`"`), &d))
	assert.True(t, d.Token.IsZero())

	require.Error(t, gob.NewEncoder(&bytes.Buffer{}).Encode(doc{Token: NewSecret(probePlaintext)}), "gob must refuse a type with no exported field")
}

// TestSecretZeroAndLen: a caller can ask about a value without revealing it.
func TestSecretZeroAndLen(t *testing.T) {
	assert.True(t, Secret{}.IsZero())
	assert.False(t, NewSecret("x").IsZero())
	assert.Equal(t, 5, NewSecret("abcde").Len())
	assert.Equal(t, NewSecret("a-very-long-credential").String(), NewSecret("short").String())
}
