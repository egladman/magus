package clean

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
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
	OK     bool
	Items  []string
	Meta   Meta
}

func Make() Result { return Result{} }

func MakePtr() *Result { return &Result{} }

func Run() error { return errors.New("x") }

func Text() string { return "" }

var global Result

func TestComparedWhole(t *testing.T) {
	got := Make()
	assert.Contains(t, got.Reason, "x")
	assert.Equal(t, "x", got.Reason)
	assert.Equal(t, Result{Reason: "x"}, got)
}

func TestComparedWholeThroughPointer(t *testing.T) {
	got := MakePtr()
	assert.True(t, got.OK)
	require.Equal(t, &Result{OK: true}, got)
}

func TestDeepEqual(t *testing.T) {
	got := Make()
	assert.Len(t, got.Items, 0)
	if !reflect.DeepEqual(got, Result{}) {
		t.Fatal("mismatch")
	}
}

func TestStructOperator(t *testing.T) {
	m := Make().Meta
	assert.Contains(t, m.Kind, "k")
	if m != (Meta{Kind: "k"}) {
		t.Fatal("mismatch")
	}
}

func TestErrorText(t *testing.T) {
	err := Run()
	require.ErrorContains(t, err, "x")
	require.EqualError(t, err, "x")
	assert.Contains(t, err.Error(), "x")
}

func TestInputLiteral(t *testing.T) {
	in := Result{Reason: "x"}
	assert.Contains(t, in.Reason, "x")
	assert.Equal(t, "x", in.Reason)
}

func TestTableCase(t *testing.T) {
	for _, tt := range []Result{{Reason: "x"}} {
		assert.Contains(t, tt.Reason, "x")
	}
}

func TestCommandLine(t *testing.T) {
	cmd := exec.Command("magus", "run")
	assert.Contains(t, cmd.Args, "run")
}

func TestFileBytes(t *testing.T) {
	b, err := os.ReadFile("x")
	require.NoError(t, err)
	assert.Equal(t, "x", string(b))
}

func TestPlainString(t *testing.T) {
	s := Text()
	assert.Contains(t, s, "x")
	assert.True(t, len(s) > 0)
}

func TestCallResult(t *testing.T) {
	assert.Contains(t, Make().Reason, "x")
}

func TestPackageVariable(t *testing.T) {
	assert.Contains(t, global.Reason, "x")
}
