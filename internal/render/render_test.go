package render

import (
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/types"
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
	if err := WriteGraphDOT(&b, singleOutput()); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, `"api";`) {
		t.Errorf("expected node declaration; got:\n%s", got)
	}
	if !strings.HasPrefix(got, "digraph magus {") {
		t.Errorf("expected digraph header; got:\n%s", got)
	}
}

func TestWriteGraphDOT_Linear(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := WriteGraphDOT(&b, linearOutput()); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, `"api" -> "internal/db";`) {
		t.Errorf("missing edge api->internal/db; got:\n%s", got)
	}
	if !strings.Contains(got, `"internal/db" -> "internal/util";`) {
		t.Errorf("missing edge internal/db->internal/util; got:\n%s", got)
	}
}

func TestWriteGraphDOT_Diamond(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := WriteGraphDOT(&b, diamondOutput()); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, `"lib/auth" -> "lib/crypto";`) {
		t.Errorf("missing auth->crypto edge; got:\n%s", got)
	}
	if !strings.Contains(got, `"lib/http" -> "lib/crypto";`) {
		t.Errorf("missing http->crypto edge; got:\n%s", got)
	}
}

func TestWriteGraphDOT_Empty(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := WriteGraphDOT(&b, emptyOutput()); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.HasPrefix(got, "digraph magus {") {
		t.Errorf("expected digraph header on empty graph; got:\n%s", got)
	}
	if strings.Contains(got, "->") {
		t.Errorf("unexpected edge in empty graph; got:\n%s", got)
	}
}

func TestWriteGraphMermaid_Linear(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := WriteGraphMermaid(&b, linearOutput()); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, "---\ntitle:") {
		t.Errorf("missing frontmatter; got:\n%s", got)
	}
	if !strings.Contains(got, "graph TD") {
		t.Errorf("missing 'graph TD' header; got:\n%s", got)
	}
	if !strings.Contains(got, "subgraph spell_") {
		t.Errorf("missing subgraph; got:\n%s", got)
	}
	for _, label := range []string{`"api"`, `"internal/db"`, `"internal/util"`} {
		if !strings.Contains(got, label) {
			t.Errorf("missing label %s; got:\n%s", label, got)
		}
	}
	if !strings.Contains(got, "-->") {
		t.Errorf("missing edges; got:\n%s", got)
	}
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
	if err := WriteGraphMermaid(&b, out); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if strings.Contains(got, "foo/bar[") || strings.Contains(got, "baz-qux[") {
		t.Errorf("unsafe characters in Mermaid IDs; got:\n%s", got)
	}
	if !strings.Contains(got, `"foo/bar"`) {
		t.Errorf("label should preserve original path; got:\n%s", got)
	}
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
	if err := WriteGraphMermaid(&b, out); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	// Both nodes should appear with distinct IDs (one will be foo_bar, the other foo_bar_1).
	if strings.Count(got, `"foo/bar"`) < 1 || strings.Count(got, `"foo_bar"`) < 1 {
		t.Errorf("expected both node labels; got:\n%s", got)
	}
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
	if err := WriteGraphMermaid(&b, out); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, sg := range []string{"subgraph spell_go", "subgraph spell_rust", "subgraph spell_typescript"} {
		if !strings.Contains(got, sg) {
			t.Errorf("missing %q; got:\n%s", sg, got)
		}
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
	if err := WriteGraphMermaid(&b, out); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, `|"rust"|`) {
		t.Errorf("expected cross-spell edge label |\"rust\"|; got:\n%s", got)
	}
	// Same-spell edge (api→util) must be a plain -->, not labeled.
	if strings.Count(got, " --> ") < 1 {
		t.Errorf("expected at least one unlabeled edge; got:\n%s", got)
	}
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
	if err := WriteGraphMermaid(&b, out); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, "classDef root") {
		t.Errorf("expected 'classDef root'; got:\n%s", got)
	}
	if !strings.Contains(got, "root") || !strings.Contains(got, "api") {
		t.Errorf("expected root class assignment for api; got:\n%s", got)
	}
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
	if err := WriteGraphMermaid(&b, out); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	// Exclusive node uses hexagon syntax {{...}}
	if !strings.Contains(got, `{{"api"}}`) {
		t.Errorf("expected hexagon shape for exclusive node; got:\n%s", got)
	}
	// Non-exclusive node uses rectangle [...]
	if !strings.Contains(got, `["util"]`) {
		t.Errorf("expected rectangle shape for non-exclusive node; got:\n%s", got)
	}
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
	if err := WriteGraphMermaid(&b, out); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, "click ") {
		t.Errorf("expected click handler; got:\n%s", got)
	}
	if !strings.Contains(got, `"file:///abs/path/api"`) {
		t.Errorf("expected file:// URL; got:\n%s", got)
	}
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
	if err := WriteGraphMermaid(&b, out); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, "BR=12") {
		t.Errorf("expected BR=12 in label; got:\n%s", got)
	}
	if strings.Contains(got, "BR=0") {
		t.Errorf("unexpected BR=0 label; got:\n%s", got)
	}
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
	if err := WriteGraphMermaid(&b, out); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{"~450ms", "~2.3s", "~1m20s"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output; got:\n%s", want, got)
		}
	}
	// The "none" node must not get any duration label.
	if strings.Contains(got, `"none<br/>`) {
		t.Errorf("unexpected duration label for DurationMs=0; got:\n%s", got)
	}
}

func TestFormatDur(t *testing.T) {
	t.Parallel()
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0ms"},
		{450 * time.Millisecond, "450ms"},
		{999 * time.Millisecond, "999ms"},
		{1000 * time.Millisecond, "1s"},
		{2300 * time.Millisecond, "2.3s"},
		{59999 * time.Millisecond, "60s"},
		{60000 * time.Millisecond, "1m"},
		{80000 * time.Millisecond, "1m20s"},
	}
	for _, tc := range cases {
		got := FormatDur(tc.d)
		if got != tc.want {
			t.Errorf("FormatDur(%v) = %q; want %q", tc.d, got, tc.want)
		}
	}
}

func TestWriteGraphMermaid_Empty(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := WriteGraphMermaid(&b, emptyOutput()); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, "graph TD") {
		t.Errorf("expected 'graph TD' even for empty graph; got:\n%s", got)
	}
}

func TestWriteGraphMermaid_Determinism(t *testing.T) {
	t.Parallel()
	out := diamondOutput()
	var b1, b2 strings.Builder
	if err := WriteGraphMermaid(&b1, out); err != nil {
		t.Fatal(err)
	}
	if err := WriteGraphMermaid(&b2, out); err != nil {
		t.Fatal(err)
	}
	if b1.String() != b2.String() {
		t.Errorf("WriteGraphMermaid is not deterministic:\nfirst:\n%s\nsecond:\n%s", b1.String(), b2.String())
	}
}
