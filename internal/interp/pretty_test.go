package interp_test

import (
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
	// Blank import wires the active backend and host bindings for all interp
	// tests. It registers the backend and host modules before any test runs.
	_ "github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/interp/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prettyString(v engine.Value, opts interp.PrettyOpts) string {
	var sb strings.Builder
	interp.PrettyPrint(&sb, v, opts)
	return sb.String()
}

func TestPrettyPrint_StringValue(t *testing.T) {
	var sb strings.Builder
	interp.PrettyPrint(&sb, engine.StringValue("hello"), interp.PrettyOpts{})
	assert.Contains(t, sb.String(), "hello")
}

func TestPrettyPrint_NilValue(t *testing.T) {
	var sb strings.Builder
	interp.PrettyPrint(&sb, engine.NilValue, interp.PrettyOpts{})
	assert.Contains(t, sb.String(), "nil")
}

func TestPrettyPrint_NumberValue(t *testing.T) {
	var sb strings.Builder
	interp.PrettyPrint(&sb, engine.NumberValue(42), interp.PrettyOpts{})
	assert.Contains(t, sb.String(), "42")
}

func TestPrettyPrint_BoolValue(t *testing.T) {
	var sb strings.Builder
	interp.PrettyPrint(&sb, engine.BoolValue(true), interp.PrettyOpts{})
	assert.Contains(t, sb.String(), "true")
}

func TestPrettyPrint_EmptyTable(t *testing.T) {
	out := prettyString(&fakeTable{}, interp.PrettyOpts{})
	assert.Equal(t, "{}\n", out)
}

func TestPrettyPrint_IdentKeyRendersUnquoted(t *testing.T) {
	tbl := &fakeTable{}
	tbl.RawSetString("name", engine.StringValue("magus"))
	out := prettyString(tbl, interp.PrettyOpts{})
	assert.Contains(t, out, "name = ")
	assert.Contains(t, out, `"magus"`)
	assert.NotContains(t, out, `["name"]`)
}

func TestPrettyPrint_NonIdentStringKeyIsBracketed(t *testing.T) {
	tbl := &fakeTable{}
	tbl.RawSetString("has space", engine.NumberValue(1))
	out := prettyString(tbl, interp.PrettyOpts{})
	assert.Contains(t, out, `["has space"]`)
}

func TestPrettyPrint_NumberKeysSortedBeforeStrings(t *testing.T) {
	tbl := &fakeTable{}
	// Insert out of order to prove the printer sorts numeric then string keys.
	tbl.RawSetString("zeta", engine.StringValue("z"))
	tbl.RawSetInt(2, engine.StringValue("two"))
	tbl.RawSetInt(1, engine.StringValue("one"))
	out := prettyString(tbl, interp.PrettyOpts{})
	i1 := strings.Index(out, "[1]")
	i2 := strings.Index(out, "[2]")
	iz := strings.Index(out, "zeta")
	require.Positive(t, i1)
	require.Positive(t, i2)
	require.Positive(t, iz)
	assert.Less(t, i1, i2, "numeric keys ascend")
	assert.Less(t, i2, iz, "numeric keys precede string keys")
}

func TestPrettyPrint_NestedTable(t *testing.T) {
	inner := &fakeTable{}
	inner.RawSetString("k", engine.NumberValue(1))
	outer := &fakeTable{}
	outer.RawSetString("child", inner)
	out := prettyString(outer, interp.PrettyOpts{})
	assert.Contains(t, out, "child = {")
	assert.Contains(t, out, "k = 1")
}

func TestPrettyPrint_MaxDepthTruncates(t *testing.T) {
	inner := &fakeTable{}
	inner.RawSetString("deep", engine.NumberValue(1))
	outer := &fakeTable{}
	outer.RawSetString("child", inner)
	// MaxDepth 1: the outer table renders, but the nested one is elided.
	out := prettyString(outer, interp.PrettyOpts{MaxDepth: 1})
	assert.Contains(t, out, "{...}")
	assert.NotContains(t, out, "deep")
}

func TestPrettyPrint_CycleDetection(t *testing.T) {
	tbl := &fakeTable{}
	// A table that references itself must render <cycle> rather than recurse.
	tbl.RawSetString("self", tbl)
	out := prettyString(tbl, interp.PrettyOpts{})
	assert.Contains(t, out, "<cycle>")
}

func TestPrettyPrint_FloatFormatting(t *testing.T) {
	out := prettyString(engine.NumberValue(3.5), interp.PrettyOpts{})
	assert.Equal(t, "3.5\n", out)
}

func TestPrettyPrint_IntegralFloatFormatting(t *testing.T) {
	// A whole-valued float prints without a trailing ".0".
	out := prettyString(engine.NumberValue(10), interp.PrettyOpts{})
	assert.Equal(t, "10\n", out)
}

func TestPrettyPrint_NonIdentNumericKeyNotBracketedAsString(t *testing.T) {
	tbl := &fakeTable{}
	tbl.RawSetInt(0, engine.StringValue("first"))
	out := prettyString(tbl, interp.PrettyOpts{})
	assert.Contains(t, out, "[0] = ")
	assert.Contains(t, out, `"first"`)
}

func TestPrettyPrint_CustomIndent(t *testing.T) {
	tbl := &fakeTable{}
	tbl.RawSetString("a", engine.NumberValue(1))
	out := prettyString(tbl, interp.PrettyOpts{Indent: "    "})
	// The custom four-space indent should precede the entry.
	assert.Contains(t, out, "\n    a = 1")
}
