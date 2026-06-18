package render

import (
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func diamondOutput() types.GraphOutput {
	return types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "api", SpellName: "go", Children: []string{"lib/auth", "lib/http"}},
			{Path: "lib/auth", SpellName: "go", Children: []string{"lib/crypto"}},
			{Path: "lib/http", SpellName: "go", Children: []string{"lib/crypto"}},
			{Path: "lib/crypto", SpellName: "go", Children: []string{}},
		},
	}
}

func linearOutput() types.GraphOutput {
	return types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "api", SpellName: "go", Children: []string{"internal/db"}},
			{Path: "internal/db", SpellName: "go", Children: []string{"internal/util"}},
			{Path: "internal/util", SpellName: "go", Children: []string{}},
		},
	}
}

func singleOutput() types.GraphOutput {
	return types.GraphOutput{
		Direction: "downstream",
		Nodes:     []types.Node{{Path: "api", SpellName: "go", Children: []string{}}},
	}
}

func emptyOutput() types.GraphOutput {
	return types.GraphOutput{Direction: "downstream", Nodes: nil}
}

func TestWriteGraphDOT_Single(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	require.NoError(t, WriteGraphDOT(&b, singleOutput()))
	got := b.String()
	assert.Contains(t, got, `"api";`, "expected node declaration")
	assert.True(t, strings.HasPrefix(got, "digraph magus {"), "expected digraph header; got:\n%s", got)
}

func TestWriteGraphDOT_Linear(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	require.NoError(t, WriteGraphDOT(&b, linearOutput()))
	got := b.String()
	assert.Contains(t, got, `"api" -> "internal/db";`, "missing edge api->internal/db")
	assert.Contains(t, got, `"internal/db" -> "internal/util";`, "missing edge internal/db->internal/util")
}

func TestWriteGraphDOT_Diamond(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	require.NoError(t, WriteGraphDOT(&b, diamondOutput()))
	got := b.String()
	assert.Contains(t, got, `"lib/auth" -> "lib/crypto";`, "missing auth->crypto edge")
	assert.Contains(t, got, `"lib/http" -> "lib/crypto";`, "missing http->crypto edge")
}

func TestWriteGraphDOT_Empty(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	require.NoError(t, WriteGraphDOT(&b, emptyOutput()))
	got := b.String()
	assert.True(t, strings.HasPrefix(got, "digraph magus {"), "expected digraph header on empty graph; got:\n%s", got)
	assert.NotContains(t, got, "->", "unexpected edge in empty graph")
}

func TestWriteGraphMermaid_Linear(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, linearOutput()))
	got := b.String()
	assert.Contains(t, got, "---\ntitle:", "missing frontmatter")
	assert.Contains(t, got, "graph TD", "missing 'graph TD' header")
	assert.Contains(t, got, "subgraph spell_", "missing subgraph")
	for _, label := range []string{`"api"`, `"internal/db"`, `"internal/util"`} {
		assert.Contains(t, got, label, "missing label %s", label)
	}
	assert.Contains(t, got, "-->", "missing edges")
}

func TestWriteGraphMermaid_PathEscaping(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "foo/bar", Children: []string{"baz-qux"}},
			{Path: "baz-qux", Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	assert.NotContains(t, got, "foo/bar[", "unsafe characters in Mermaid IDs")
	assert.NotContains(t, got, "baz-qux[", "unsafe characters in Mermaid IDs")
	assert.Contains(t, got, `"foo/bar"`, "label should preserve original path")
}

func TestWriteGraphMermaid_IDCollision(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "foo/bar", Children: []string{}},
			{Path: "foo_bar", Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	// Both nodes should appear with distinct IDs (one will be foo_bar, the other foo_bar_1).
	assert.Contains(t, got, `"foo/bar"`, "expected both node labels")
	assert.Contains(t, got, `"foo_bar"`, "expected both node labels")
}

func TestWriteGraphMermaid_Subgraphs(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "api", SpellName: "go", Children: []string{"engine"}},
			{Path: "engine", SpellName: "rust", Children: []string{}},
			{Path: "web", SpellName: "typescript", Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	for _, sg := range []string{"subgraph spell_go", "subgraph spell_rust", "subgraph spell_typescript"} {
		assert.Contains(t, got, sg)
	}
}

func TestWriteGraphMermaid_CrossSpellEdgeLabel(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "api", SpellName: "go", Children: []string{"engine", "util"}},
			{Path: "engine", SpellName: "rust", Children: []string{}},
			{Path: "util", SpellName: "go", Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	assert.Contains(t, got, `|"rust"|`, `expected cross-spell edge label |"rust"|`)
	// Same-spell edge (api→util) must be a plain -->, not labeled.
	assert.GreaterOrEqual(t, strings.Count(got, " --> "), 1, "expected at least one unlabeled edge")
}

func TestWriteGraphMermaid_RootHighlight(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Roots:     []string{"api"},
		Nodes: []types.Node{
			{Path: "api", SpellName: "go", Children: []string{"util"}},
			{Path: "util", SpellName: "go", Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	assert.Contains(t, got, "classDef root", "expected 'classDef root'")
	assert.Contains(t, got, "root", "expected root class assignment for api")
	assert.Contains(t, got, "api", "expected root class assignment for api")
}

func TestWriteGraphMermaid_Exclusive(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "api", SpellName: "go", Exclusive: true, Children: []string{"util"}},
			{Path: "util", SpellName: "go", Exclusive: false, Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	// Exclusive node uses hexagon syntax {{...}}
	assert.Contains(t, got, `{{"api"}}`, "expected hexagon shape for exclusive node")
	// Non-exclusive node uses rectangle [...]
	assert.Contains(t, got, `["util"]`, "expected rectangle shape for non-exclusive node")
}

func TestWriteGraphMermaid_ClickHandler(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "api", SpellName: "go", Dir: "/abs/path/api", Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	assert.Contains(t, got, "click ", "expected click handler")
	assert.Contains(t, got, `"file:///abs/path/api"`, "expected file:// URL")
}

func TestWriteGraphMermaid_BlastRadius(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "api", SpellName: "go", BlastRadius: 12, Children: []string{"util"}},
			{Path: "util", SpellName: "go", BlastRadius: 0, Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	assert.Contains(t, got, "BR=12", "expected BR=12 in label")
	assert.NotContains(t, got, "BR=0", "unexpected BR=0 label")
}

func TestWriteGraphMermaid_Duration(t *testing.T) {
	t.Parallel()
	out := types.GraphOutput{
		Direction: "downstream",
		Nodes: []types.Node{
			{Path: "fast", SpellName: "go", DurationMs: 450, Children: []string{}},
			{Path: "mid", SpellName: "go", DurationMs: 2300, Children: []string{}},
			{Path: "slow", SpellName: "go", DurationMs: 80000, Children: []string{}},
			{Path: "none", SpellName: "go", DurationMs: 0, Children: []string{}},
		},
	}
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, out))
	got := b.String()
	for _, want := range []string{"~450ms", "~2.3s", "~1m20s"} {
		assert.Contains(t, got, want)
	}
	// The "none" node must not get any duration label.
	assert.NotContains(t, got, `"none<br/>`, "unexpected duration label for DurationMs=0")
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "0ms", FormatDuration(0))
	assert.Equal(t, "450ms", FormatDuration(450*time.Millisecond))
	assert.Equal(t, "999ms", FormatDuration(999*time.Millisecond))
	assert.Equal(t, "1s", FormatDuration(1000*time.Millisecond))
	assert.Equal(t, "2.3s", FormatDuration(2300*time.Millisecond))
	assert.Equal(t, "60s", FormatDuration(59999*time.Millisecond))
	assert.Equal(t, "1m", FormatDuration(60000*time.Millisecond))
	assert.Equal(t, "1m20s", FormatDuration(80000*time.Millisecond))
}

func TestWriteGraphMermaid_Empty(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	require.NoError(t, WriteGraphMermaid(&b, emptyOutput()))
	got := b.String()
	assert.Contains(t, got, "graph TD", "expected 'graph TD' even for empty graph")
}

func TestWriteGraphMermaid_Determinism(t *testing.T) {
	t.Parallel()
	out := diamondOutput()
	var b1, b2 strings.Builder
	require.NoError(t, WriteGraphMermaid(&b1, out))
	require.NoError(t, WriteGraphMermaid(&b2, out))
	assert.Equal(t, b1.String(), b2.String(), "WriteGraphMermaid is not deterministic")
}
