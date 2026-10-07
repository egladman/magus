package complete

import (
	"sort"
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

func TestReadInSubtest(t *testing.T) {
	var s Span
	assert.Equal(t, 0, s.Start) // want `s is asserted one field at a time \(Start, End\)`
	t.Run("read", func(t *testing.T) { _ = s.End })
	assert.Equal(t, 0, s.End)
}

func TestChangedInSubtest(t *testing.T) {
	var s Span
	assert.Equal(t, 0, s.Start)
	t.Run("grow", func(t *testing.T) { s.End = 2 })
	assert.Equal(t, 2, s.End)
}

func TestChangedByClosureCalledLater(t *testing.T) {
	var s Span
	grow := func() { s.End++ }
	assert.Equal(t, 0, s.Start)
	grow()
	assert.Equal(t, 1, s.End)
}

func TestChangedThroughCapturedAlias(t *testing.T) {
	var s Span
	var p *Span
	alias := func() { p = &s }
	alias()
	assert.Equal(t, 0, s.Start)
	p.End = 1
	assert.Equal(t, 1, s.End)
}

func TestVariableIndexAssigned(t *testing.T) {
	spans := make([]Span, 2)
	i := 0
	assert.Equal(t, 0, spans[0].Start)
	spans[i] = Span{End: 1}
	assert.Equal(t, 1, spans[0].End)
}

func TestVariableIndexMethod(t *testing.T) {
	counters := make([]Counter, 2)
	i := 0
	assert.Equal(t, "", counters[0].Name)
	counters[i].Bump()
	assert.Equal(t, 1, counters[0].Count)
}

func TestVariableIndexAddress(t *testing.T) {
	spans := make([]Span, 2)
	i := 0
	assert.Equal(t, 0, spans[0].Start)
	Fill(&spans[i])
	assert.Equal(t, 1, spans[0].End)
}

func TestWantOnLeft(t *testing.T) {
	want := struct{ Start, End int }{1, 2}
	var got Span
	if want.Start != got.Start {
		t.Errorf("Start = %d", got.Start)
	}
	if want.End != got.End {
		t.Errorf("End = %d", got.End)
	}
}

func TestSwappedAgainstAnotherType(t *testing.T) {
	want := struct{ Start, End int }{1, 2}
	var got Span
	assert.Equal(t, got.Start, want.Start)
	assert.Equal(t, got.End, want.End)
}

func TestAgainstSameType(t *testing.T) {
	want, got := Span{1, 2}, Span{}
	assert.Equal(t, want.Start, got.Start) // want `got is asserted one field at a time \(Start, End\)`
	assert.Equal(t, want.End, got.End)
}

func TestChangedThroughAddressAlias(t *testing.T) {
	var s Span
	p := &s
	assert.Equal(t, 0, s.Start)
	p.End = 1
	assert.Equal(t, 1, s.End)
}

func TestChangedThroughSharedSlice(t *testing.T) {
	var l List
	names := l.Names
	assert.Equal(t, 0, l.Size)
	names[0] = "x"
	assert.Equal(t, []string{"x"}, l.Names)
}

func TestCopyIsNotAnAlias(t *testing.T) {
	var s Span
	c := s
	assert.Equal(t, 0, s.Start) // want `s is asserted one field at a time \(Start, End\)`
	c.End = 1
	assert.Equal(t, 0, s.End)
}

func TestAliasPointerHandedOn(t *testing.T) {
	p := NewSpan()
	assert.Equal(t, 0, p.Start)
	Grow(p)
	assert.Equal(t, 1, p.End)
}

func TestDefinedPointerHandedOn(t *testing.T) {
	r := NewRef()
	assert.Equal(t, 0, r.Start)
	Stretch(r)
	assert.Equal(t, 1, r.End)
}

func TestSortedInPlace(t *testing.T) {
	var l List
	assert.Equal(t, []string{"b", "a"}, l.Names)
	sort.Strings(l.Names)
	assert.Equal(t, 2, l.Size)
}

func TestMapChangedInPlace(t *testing.T) {
	idx := Index{}
	assert.Equal(t, 0, idx["a"].Start)
	Put(idx)
	assert.Equal(t, 1, idx["a"].End)
}

func TestMapEntryDeleted(t *testing.T) {
	idx := Index{}
	assert.Equal(t, 0, idx["a"].Start)
	delete(idx, "a")
	assert.Equal(t, 0, idx["a"].End)
}
