package complete

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEveryField(t *testing.T) {
	var s Span
	assert.Equal(t, 1, s.Start) // want `s is asserted one field at a time \(Start, End\): build the whole expected Span`
	require.Equal(t, 2, s.End)
}

func TestEveryFieldThroughPointer(t *testing.T) {
	e := &Entry{}
	if e.Key != "k" { // want `e is asserted one field at a time \(Key, Value, Rev\)`
		t.Errorf("Key = %q", e.Key)
	}
	assert.Equal(t, "v", e.Value)
	assert.Equal(t, 1, e.Rev)
}

func TestSomeFields(t *testing.T) {
	var e Entry
	assert.Equal(t, "k", e.Key)
	assert.Equal(t, "v", e.Value)
}

func TestPromotedFieldsAreNotTheEmbedded(t *testing.T) {
	var w Wrapped
	assert.Equal(t, 1, w.Start)
	assert.Equal(t, "x", w.Label)
}

func TestEmbeddedWhole(t *testing.T) {
	var w Wrapped
	assert.Equal(t, Span{}, w.Span) // want `w is asserted one field at a time \(Span, Label\)`
	assert.Equal(t, "x", w.Label)
}
