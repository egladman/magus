package flagged

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type Meta struct {
	Kind string
	Rev  int
}

type Result struct {
	Reason string
	Path   string
	OK     bool
	Items  []string
	Body   []byte
	Meta   Meta
}

func Make() Result { return Result{} }

func MakePtr() *Result { return &Result{} }

func Fill(r *Result) {}

func TestContains(t *testing.T) {
	got := Make()
	assert.Contains(t, got.Reason, "last updated") // want `got is checked in part \(Contains on got.Reason\): build the whole expected Result and compare got with Equal, making any time, id or path in it deterministic \(a fixed clock, seeded ids, t.TempDir in the expectation\)`
}

func TestContainsf(t *testing.T) {
	got := Make()
	assert.Containsf(t, got.Reason, "last updated", "reason of %s", "x") // want `got is checked in part \(Containsf on got.Reason\)`
}

func TestNotContains(t *testing.T) {
	got := Make()
	require.NotContains(t, got.Items, "x") // want `got is checked in part \(NotContains on got.Items\)`
}

func TestTrueOnPrefix(t *testing.T) {
	got := Make()
	assert.True(t, strings.HasPrefix(got.Path, "/tmp")) // want `got is checked in part \(True on got.Path\)`
}

func TestFalseOnField(t *testing.T) {
	got := Make()
	require.False(t, got.OK) // want `got is checked in part \(False on got.OK\)`
}

func TestRegexp(t *testing.T) {
	got := Make()
	assert.Regexp(t, `^last`, got.Reason) // want `got is checked in part \(Regexp on got.Reason\)`
}

func TestNotRegexp(t *testing.T) {
	got := Make()
	assert.NotRegexp(t, `^last`, got.Reason) // want `got is checked in part \(NotRegexp on got.Reason\)`
}

func TestLenThenElements(t *testing.T) {
	got := Make()
	require.Len(t, got.Items, 2)       // want `got is checked in part \(Len on got.Items\)`
	assert.Equal(t, "a", got.Items[0]) // want `got is checked in part \(Equal on got.Items\)`
}

func TestSubset(t *testing.T) {
	got := Make()
	assert.Subset(t, got.Items, []string{"a"}) // want `got is checked in part \(Subset on got.Items\)`
}

func TestNotSubset(t *testing.T) {
	got := Make()
	assert.NotSubset(t, got.Items, []string{"a"}) // want `got is checked in part \(NotSubset on got.Items\)`
}

func TestStringBuiltFromField(t *testing.T) {
	got := Make()
	assert.Contains(t, string(got.Body), "ok")               // want `got is checked in part \(Contains on got.Body\)`
	assert.Contains(t, fmt.Sprintf("%d", got.Meta.Rev), "1") // want `got is checked in part \(Contains on got.Meta.Rev\): build the whole expected Result and compare got with Equal`
}

func TestFieldComparedWhole(t *testing.T) {
	got := Make()
	assert.Contains(t, got.Meta.Kind, "k")     // want `got is checked in part \(Contains on got.Meta.Kind\)`
	assert.Equal(t, Meta{Kind: "k"}, got.Meta) // want `got is checked in part \(Equal on got.Meta\)`
}

func TestEqualOnLength(t *testing.T) {
	got := Make()
	assert.Equal(t, 2, len(got.Items)) // want `got is checked in part \(Equal on got.Items\)`
}

func TestLoneEqual(t *testing.T) {
	got := Make()
	assert.Equal(t, "ok", got.Reason) // want `got is checked in part \(Equal on got.Reason\)`
}

func TestLoneHandRolled(t *testing.T) {
	got := Make()
	if got.Reason != "ok" { // want `got is checked in part \(!= on got.Reason\)`
		t.Fatalf("Reason = %q", got.Reason)
	}
}

func TestSomeFields(t *testing.T) {
	got := Make()
	assert.Equal(t, "ok", got.Reason) // want `got is checked in part \(Equal on got.Reason, got.Path\)`
	assert.Equal(t, "/p", got.Path)
}

func TestPointerResult(t *testing.T) {
	got := MakePtr()
	assert.True(t, got.OK) // want `got is checked in part \(True on got.OK\)`
}

func TestAssertionMethod(t *testing.T) {
	got := Make()
	a := assert.New(t)
	a.Contains(got.Reason, "x") // want `got is checked in part \(Contains on got.Reason\)`
}

func TestHandedByAddress(t *testing.T) {
	got := Result{}
	Fill(&got)
	assert.Contains(t, got.Reason, "x") // want `got is checked in part \(Contains on got.Reason\)`
}

func TestSubtest(t *testing.T) {
	got := Make()
	t.Run("reason", func(t *testing.T) {
		assert.Contains(t, got.Reason, "x") // want `got is checked in part \(Contains on got.Reason\)`
	})
}

func checkReason(t *testing.T, got Result) {
	t.Helper()
	assert.Contains(t, got.Reason, "x") // want `got is checked in part \(Contains on got.Reason\)`
}
