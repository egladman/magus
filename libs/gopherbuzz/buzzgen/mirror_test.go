package buzzgen

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFieldType_Kinds pins every Go kind the mirror generator accepts, and the
// zero-value default it pairs with. The defaults matter as much as the type
// names: a Buzz object literal is written from them, so a wrong default ships a
// mirror whose fields start life as the wrong value.
func TestFieldType_Kinds(t *testing.T) {
	t.Parallel()

	type inner struct{ A string }

	cases := []struct {
		name     string
		in       reflect.Type
		wantType string
		wantZero string
	}{
		{"string", reflect.TypeOf(""), "str", `""`},
		{"bool", reflect.TypeOf(false), "bool", "false"},
		{"int", reflect.TypeOf(0), "int", "0"},
		{"int64", reflect.TypeOf(int64(0)), "int", "0"},
		{"float64", reflect.TypeOf(0.0), "double", "0.0"},
		{"slice", reflect.TypeOf([]string{}), "[str]", "[]"},
		{"map", reflect.TypeOf(map[string]string{}), "{str: str}", "{}"},
		{"struct", reflect.TypeOf(inner{}), "inner", "inner{}"},
		// A timestamp crosses as RFC3339 text, never as an object.
		{"time", reflect.TypeOf(time.Time{}), "str", `""`},

		// Buzz has no unsigned integer; every width folds onto int.
		{"uint", reflect.TypeOf(uint(0)), "int", "0"},
		{"uint8", reflect.TypeOf(uint8(0)), "int", "0"},
		{"uint64", reflect.TypeOf(uint64(0)), "int", "0"},

		// A pointer is Go's optional, so it must arrive nullable and default to
		// null - not to the pointed-to type's zero, which would assert presence.
		{"pointer to struct", reflect.TypeOf(&inner{}), "inner?", "null"},
		{"pointer to string", reflect.TypeOf(new(string)), "str?", "null"},

		// A fixed-size array mirrors exactly as a slice: Buzz cannot carry the
		// length guarantee, so claiming one would be a lie.
		{"array", reflect.TypeOf([32]byte{}), "[int]", "[]"},

		// A Duration is an Int64, so without its own case it would mirror as a bare
		// int and hand Buzz an unlabelled nanosecond count.
		{"duration", reflect.TypeOf(time.Duration(0)), "str", `""`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotType, gotZero, err := FieldType(tc.in, DefaultOptions())
			require.NoError(t, err)
			assert.Equal(t, tc.wantType, gotType, "type name")
			assert.Equal(t, tc.wantZero, gotZero, "zero value")
		})
	}
}

// TestBuzzType_Unsupported keeps the generator failing loudly on a type it cannot
// represent. Silently emitting something plausible is the failure mode worth
// preventing: the mirror would typecheck and then disagree with the runtime value.
func TestFieldType_Unsupported(t *testing.T) {
	t.Parallel()

	_, _, err := FieldType(reflect.TypeOf(func() {}), DefaultOptions())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Go type")
}

// TestRenderFields_InlinesEmbedded is the regression guard for the shape the Buzz
// mirror and encoding/json must agree on. encoding/json PROMOTES an anonymous
// field's keys to the outer object; if the mirror nested them instead, the same
// Go type would describe two different shapes and a magusfile would look for
// promoted fields one level too deep.
func TestRenderFields_InlinesEmbedded(t *testing.T) {
	t.Parallel()

	type Embedded struct {
		Alpha string
		Beta  int
	}
	type Outer struct {
		Embedded
		Gamma bool
	}

	var b bytes.Buffer
	require.NoError(t, renderFields(&b, reflect.TypeOf(Outer{}), DefaultOptions()))

	got := b.String()
	assert.Contains(t, got, "alpha: str", "embedded field must be promoted, not nested")
	assert.Contains(t, got, "beta: int", "embedded field must be promoted, not nested")
	assert.Contains(t, got, "gamma: bool")
	assert.NotContains(t, got, "embedded:", "the embedded type name must not appear as a field")
}

// TestRenderFields_NamedEmbeddedNests covers the deliberate opt-out: tagging an
// embedded field asks for it to be nested under that name, which is the only way
// to express a nested record when the Go type happens to embed it.
func TestRenderFields_NamedEmbeddedNests(t *testing.T) {
	t.Parallel()

	type Embedded struct{ Alpha string }
	type Outer struct {
		Embedded `buzz:"inner"`
	}

	var b bytes.Buffer
	require.NoError(t, renderFields(&b, reflect.TypeOf(Outer{}), DefaultOptions()))

	got := b.String()
	assert.Contains(t, got, "inner: Embedded", "a tagged embedded field nests under its tag")
	assert.NotContains(t, got, "alpha:", "a tagged embedded field must not also promote")
}

// TestRenderFields_SkipsUnexportedAndOptedOut pins the two existing exclusions, so
// the recursive rewrite cannot quietly start emitting either.
func TestRenderFields_SkipsUnexportedAndOptedOut(t *testing.T) {
	t.Parallel()

	type Outer struct {
		Kept    string
		Dropped string `buzz:"-"`
		hidden  string //nolint:unused // presence is the point: it must not be emitted
	}

	var b bytes.Buffer
	require.NoError(t, renderFields(&b, reflect.TypeOf(Outer{}), DefaultOptions()))

	got := b.String()
	assert.Contains(t, got, "kept: str")
	assert.NotContains(t, got, "dropped")
	assert.NotContains(t, got, "hidden")
}
