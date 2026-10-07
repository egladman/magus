package results

import (
	"testing"

	"geo"
	"opaque"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTestifyFunctions(t *testing.T) {
	got := Make()
	assert.Equal(t, "a", got.Name) // want `got is asserted one field at a time \(Name, Count, Tags\): build the whole expected Result and compare it once`
	assert.EqualValues(t, 2, got.Count)
	require.Equal(t, []string{"x"}, got.Tags)
}

func TestAssertionMethods(t *testing.T) {
	got := Make()
	r := require.New(t)
	r.Equal("a", got.Name) // want `got is asserted one field at a time \(Name, Count\)`
	r.Exactly(2, got.Count)
}

func TestHandRolled(t *testing.T) {
	got := Make()
	if got.Name != "a" { // want `got is asserted one field at a time \(Name, Count\)`
		t.Errorf("Name = %q", got.Name)
	}
	if got.Count != 2 {
		t.Fatalf("Count = %d", got.Count)
	}
}

func TestInitDefined(t *testing.T) {
	res := Make()
	if got, want := res.Name, "a"; got != want { // want `res is asserted one field at a time \(Name, Count\)`
		t.Errorf("Name = %q, want %q", got, want)
	}
	if got, want := res.Count, 2; got != want {
		t.Errorf("Count = %d, want %d", got, want)
	}
}

func TestSwappedArguments(t *testing.T) {
	got := Make()
	assert.Equal(t, got.Name, "a") // want `got is asserted one field at a time \(Name, Count\)`
	assert.Equal(t, got.Count, 2)
}

func TestOrChain(t *testing.T) {
	got := Make()
	if got.Name != "a" || got.Count != 2 { // want `got is asserted one field at a time \(Name, Count\)`
		t.Fatal("mismatch")
	}
}

func TestNestedValue(t *testing.T) {
	got := Make()
	assert.Equal(t, "k", got.Meta.Kind) // want `got.Meta is asserted one field at a time \(Kind, Rev\): build the whole expected Meta`
	assert.Equal(t, 1, got.Meta.Rev)
}

func TestExportedOtherPackage(t *testing.T) {
	p := geo.Origin()
	assert.Equal(t, 0, p.X) // want `p is asserted one field at a time \(X, Y\): build the whole expected geo.Point`
	assert.Equal(t, 0, p.Y)
}

func TestUnexportedOwnPackage(t *testing.T) {
	in := MakeInner()
	assert.Equal(t, "a", in.Name) // want `in is asserted one field at a time \(Name, Other\)`
	assert.Equal(t, 1, in.Other)
}

func TestSubtestOfTable(t *testing.T) {
	tests := []struct {
		name  string
		count int
	}{{"a", 1}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Make()
			assert.Equal(t, tt.name, got.Name) // want `got is asserted one field at a time \(Name, Count\)`
			assert.Equal(t, tt.count, got.Count)
		})
	}
}

func TestBothSidesFields(t *testing.T) {
	want, got := Make(), Make()
	assert.Equal(t, want.Name, got.Name) // want `got is asserted one field at a time \(Name, Count\)`
	assert.Equal(t, want.Count, got.Count)
}

func checkResult(t *testing.T, got Result) {
	t.Helper()
	assert.Equal(t, "a", got.Name) // want `got is asserted one field at a time \(Name, Err\)`
	assert.Equal(t, nil, got.Err)
}

func TestGenericInstance(t *testing.T) {
	b := Box[string]{}
	assert.Equal(t, "v", b.Value) // want `b is asserted one field at a time \(Value, Size\): build the whole expected Box\[string\]`
	assert.Equal(t, 1, b.Size)
}

func TestWholeValue(t *testing.T) {
	assert.Equal(t, Result{Name: "a"}, Make())
}

func TestLoneField(t *testing.T) {
	got := Make()
	assert.Equal(t, "a", got.Name)
	require.NoError(t, got.Err)
}

func TestSameFieldTwice(t *testing.T) {
	c := &Counter{}
	assert.Equal(t, 0, c.Count)
	c.Bump()
	assert.Equal(t, 1, c.Count)
}

func TestDifferentValues(t *testing.T) {
	a, b := Make(), Make()
	assert.Equal(t, "a", a.Name)
	assert.Equal(t, 2, b.Count)
}

func TestOpaqueOtherPackage(t *testing.T) {
	tok := opaque.New()
	assert.Equal(t, "id", tok.ID)
	assert.Equal(t, 1, tok.Rev)
}

func TestFuncField(t *testing.T) {
	h := Handle{}
	assert.Equal(t, "a", h.Name)
	assert.Equal(t, nil, h.Close)
}

func TestInterfaceField(t *testing.T) {
	c := Conn{}
	assert.Equal(t, "a", c.Name)
	assert.Equal(t, nil, c.R)
}

func TestLengthChecks(t *testing.T) {
	got := Make()
	assert.Equal(t, 2, len(got.Tags))
	assert.Len(t, got.Items, 1)
	assert.Equal(t, "a", got.Name)
}

func TestLoopOverSlice(t *testing.T) {
	got := Make()
	for _, item := range got.Items {
		assert.Equal(t, "x", item.Name)
		assert.Equal(t, 1, item.Count)
	}
}

func TestTableCaseFields(t *testing.T) {
	tests := []struct {
		wantName  string
		wantCount int
	}{{"a", 1}}
	for _, tt := range tests {
		name, count := "a", 1
		if tt.wantName != name {
			t.Fatal("name")
		}
		if tt.wantCount != count {
			t.Fatal("count")
		}
	}
}

func TestVariableIndex(t *testing.T) {
	all := MakeAll()
	for i := 0; i < len(all); i++ {
		assert.Equal(t, "a", all[i].Name)
		assert.Equal(t, 1, all[i].Count)
	}
}

func TestCallResult(t *testing.T) {
	assert.Equal(t, "a", Make().Name)
	assert.Equal(t, 1, Make().Count)
}

func TestMutatedBetween(t *testing.T) {
	c := &Counter{}
	assert.Equal(t, "", c.Name)
	c.Bump()
	assert.Equal(t, 1, c.Count)
}

func TestReassignedBetween(t *testing.T) {
	got := Make()
	assert.Equal(t, "a", got.Name)
	got = Make()
	assert.Equal(t, 1, got.Count)
}

func TestHandedByPointerBetween(t *testing.T) {
	var got Result
	assert.Equal(t, "", got.Name)
	Fill(&got)
	assert.Equal(t, 1, got.Count)
}

func TestValueReceiverKeepsValue(t *testing.T) {
	c := Counter{}
	assert.Equal(t, "", c.Name) // want `c is asserted one field at a time \(Name, Count\)`
	_ = c.Label()
	Read(Make())
	assert.Equal(t, 0, c.Count)
}

func TestFieldAgainstField(t *testing.T) {
	got := Make()
	assert.Equal(t, got.Count, got.Total)
	assert.Equal(t, "a", got.Name)
}

func TestNotAFailure(t *testing.T) {
	got := Make()
	if got.Name != "a" {
		t.Log("name differs")
	}
	if got.Count != 2 {
		t.Log("count differs")
	}
}

func TestNotEqual(t *testing.T) {
	got := Make()
	assert.NotEqual(t, "a", got.Name)
	assert.NotEqual(t, 2, got.Count)
}

func TestSeparateSubtests(t *testing.T) {
	got := Make()
	t.Run("name", func(t *testing.T) { assert.Equal(t, "a", got.Name) })
	t.Run("count", func(t *testing.T) { assert.Equal(t, 1, got.Count) })
}
