package render

import (
	"bytes"
	"net/url"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteTargetGraphMarkdown pins the routing-index shape: a per-project target
// list (name + one-line doc), the pointer to the commands that expand an entry,
// and the deliberate absence of the old per-target dispatch plan and Mermaid graphs.
func TestWriteTargetGraphMarkdown(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{{
		Path:   ".",
		Engine: "buzz",
		Nodes: []types.TargetGraphNode{
			{Name: "build", Doc: "Build the binary.", Dependencies: []string{"fmt"}, Charms: []string{"rw"}},
			{Name: "fmt"},
		},
	}}}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&b, out, nil, "", nil))
	got := b.String()
	for _, want := range []string{
		"# Targets",
		"## Quick start",
		"## Project: (workspace root)", // bare "." is never used as a heading
		"| Target | What it does |",    // the compact per-project list is a table
		"| `build` | Build the binary. |",
		"| `fmt` |",
		"[Glossary](https://eli.gladman.cc/magus/glossary/)", // terms link out to the hosted docs
		"magus describe target <name>",                       // pointer: a target's evaluated plan is one command away
		"magus describe mcp-tools",                           // pointer: the agent tool list
		// Staleness contract (always present, near the header) and the front-loaded
		// "route by question" block with its exact routing commands.
		"If the counts here look stale, trust the live tools (`magus query`) over this file.",
		"## Route by question",
		"| `magus query \"<terms>\"` |",
		"| `magus describe file <path>` |",
		"| `magus affected ci` |",
		"| `magus query output <ref>` |",
	} {
		assert.Contains(t, got, want, "markdown output missing %q", want)
	}
	// Route by question is front-loaded: it precedes Quick start and every project
	// table, so an agent hits the exact command before any bulk.
	assert.Less(t, strings.Index(got, "## Route by question"), strings.Index(got, "## Quick start"),
		"route-by-question should precede Quick start")
	assert.Less(t, strings.Index(got, "## Route by question"), strings.Index(got, "| Target | What it does |"),
		"route-by-question should precede the project tables")
	// No default_charms passed here, so the header carries no default-charms line.
	assert.NotContains(t, got, "Default charms:", "default-charms line must be omitted when none are set")
	// The dispatch plan and the embedded graphs are gone - that bulk is what made
	// the file useless as in-context routing.
	for _, bad := range []string{"```mermaid", "**Run order**", "**Toolchain**", "**Defaults**", "**Charms**", "**Executes**", "Shared defaults", "#data="} {
		assert.NotContains(t, got, bad, "routing index should not carry %q", bad)
	}
}

// TestWriteTargetGraphMarkdownDefaultCharms pins the header's default-charms line:
// it renders (near the header, above the route table) only when the workspace sets
// default charms, and is omitted entirely - no empty section - when none are set.
func TestWriteTargetGraphMarkdownDefaultCharms(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{{
		Path: ".", Engine: "buzz", Nodes: []types.TargetGraphNode{{Name: "build"}},
	}}}

	// With default charms set: the line renders and sits near the header, before the
	// route table.
	var withCharms bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&withCharms, out, nil, "", []string{"rw"}))
	got := withCharms.String()
	assert.Contains(t, got, "Default charms: rw (local runs write; CI strips them with `--no-default-charms`).",
		"default-charms line should render when the workspace sets them")
	assert.Less(t, strings.Index(got, "Default charms:"), strings.Index(got, "## Route by question"),
		"default-charms line should sit near the header, above the route table")

	// With no default charms: the line is omitted entirely, no empty section.
	var noCharms bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&noCharms, out, nil, "", nil))
	assert.NotContains(t, noCharms.String(), "Default charms:",
		"default-charms line must be omitted when none are set")
}

// TestWriteTargetGraphMarkdownHeadingAndOrder pins the two layout refinements: a
// project at the workspace root renders under its repo-relative heading (not the
// ambiguous `.`), and pure workers (no deps, pulled in by others) sort after the
// targets you invoke directly.
func TestWriteTargetGraphMarkdownHeadingAndOrder(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{{
		Path:    ".",
		RelPath: "magus",
		Engine:  "buzz",
		Nodes: []types.TargetGraphNode{
			{Name: "worker"}, // no deps, depended on -> sorts last
			{Name: "build", Dependencies: []string{"worker"}}, // primary
		},
	}}}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&b, out, nil, "", nil))
	got := b.String()
	assert.Contains(t, got, "## Project: magus", "heading should use the repo-relative path")
	i, j := strings.Index(got, "| `build` |"), strings.Index(got, "| `worker` |")
	require.GreaterOrEqual(t, i, 0, "build row should be present:\n%s", got)
	require.GreaterOrEqual(t, j, 0, "worker row should be present:\n%s", got)
	assert.Less(t, i, j, "primary target should precede the worker")
}

// TestWriteTargetGraphMarkdownRouting pins the "query first" section: it renders
// only when routing is supplied, leads with the retrieval verbs, and emits a
// per-kind row (with the query to run) and a per-project row.
func TestWriteTargetGraphMarkdownRouting(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{{
		Path: ".", Engine: "buzz", Nodes: []types.TargetGraphNode{{Name: "build"}},
	}}}

	// Without routing, the section is absent.
	var plain bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&plain, out, nil, "", nil))
	assert.NotContains(t, plain.String(), "## Query first")

	routing := &types.KnowledgeRouting{
		SchemaVersion: 1, NodeCount: 42, EdgeCount: 99,
		Kinds:    []types.KnowledgeRoutingKind{{Kind: "spell", Count: 12, Anchors: []string{"go", "buf"}}},
		Projects: []types.KnowledgeRoutingProject{{Path: "pkg/foo", TargetCount: 3, KeyTargets: []string{"ci"}}},
	}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&b, out, routing, "", nil))
	got := b.String()
	for _, want := range []string{
		"## Query first",
		"42 nodes",
		"magus explain <node>",
		"magus query kind:spell",
		"`go`, `buf`", // anchors as inline code
		"magus query project:pkg/foo",
	} {
		assert.Contains(t, got, want, "routing section missing %q", want)
	}
}

// TestFirstDocLine pins the target-list cell helper: it keeps only the first
// line, trims it, and escapes a pipe so the doc cannot break the Markdown table.
func TestFirstDocLine(t *testing.T) {
	assert.Equal(t, "Build the binary.", firstDocLine("Build the binary."))
	assert.Equal(t, "Build the binary.", firstDocLine("Build the binary.\nmore detail"))
	assert.Equal(t, "", firstDocLine(""))
	assert.Equal(t, `a \| b`, firstDocLine("a | b"))
	assert.Equal(t, "trimmed", firstDocLine("  trimmed  "))
}

func TestWriteTargetGraphMermaidSingleProject(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{{
		Path:   ".",
		Engine: "buzz",
		Nodes: []types.TargetGraphNode{
			{Name: "build", Dependencies: []string{"fmt"}},
			{Name: "fmt"},
		},
	}}}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphMermaid(&b, out))
	got := b.String()
	// A single project with no shared-suffix targets renders flat — no project
	// wrapper, no stage box, no id prefix.
	assert.NotContains(t, got, "subgraph", "single project with no stages should not emit a subgraph")
	for _, want := range []string{`build("build")`, `fmt("fmt")`, "fmt --> build"} {
		assert.Contains(t, got, want, "output missing %q", want)
	}
}

// TestWriteTargetGraphMermaidNoSpellBoxes pins that the dependency graph carries no
// spell boxes: every target — spell-driving or not — is a single node coloured by
// role. Spells live in the separate Toolchain graph instead.
func TestWriteTargetGraphMermaidNoSpellBoxes(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{{
		Path:   ".",
		Engine: "buzz",
		Nodes: []types.TargetGraphNode{
			{Name: "lint", Spells: []types.TargetSpellUse{
				{Spell: "go", Ops: []string{"golangci-lint", "go-vet"}},
				{Spell: "md", Ops: []string{"markdownlint"}},
			}},
			{Name: "noop"},
		},
	}}}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphMermaid(&b, out))
	got := b.String()
	// Targets are plain nodes; no per-target spell subgraph or spell box survives.
	for _, want := range []string{`lint("lint")`, `noop("noop")`} {
		assert.Contains(t, got, want, "target should be a plain node; missing %q", want)
	}
	for _, bad := range []string{`subgraph lint`, "lint_s0", "go: golangci-lint"} {
		assert.NotContains(t, got, bad, "dependency graph should not box spells")
	}
}

// TestWriteTargetGraphMermaidStages pins the pipeline-stage grouping: targets that
// share a trailing `-<segment>` are boxed together; a singleton stays loose.
func TestWriteTargetGraphMermaidStages(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{{
		Path:   ".",
		Engine: "buzz",
		Nodes: []types.TargetGraphNode{
			{Name: "man-generate"},
			{Name: "docs-generate"},
			{Name: "release"},
			{Name: "generate", Dependencies: []string{"man-generate", "docs-generate"}},
		},
	}}}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphMermaid(&b, out))
	got := b.String()
	assert.Contains(t, got, `subgraph stage_generate["generate"]`, "expected a 'generate' stage subgraph")
	// `release` has a unique suffix, so it must not be boxed.
	assert.NotContains(t, got, `stage_release`, "singleton 'release' should stay loose")
	// `generate` and the lone `release` are top-level (nothing depends on them); the
	// `*-generate` workers are plain targets pulled in as dependencies.
	for _, want := range []string{
		"classDef anchor",
		"class generate,release anchor",
		"class docs_generate,man_generate target",
	} {
		assert.Contains(t, got, want, "output missing %q", want)
	}
}

// TestWriteTargetGraphMermaidStageExcludesNonDependency pins that a same-suffix
// target the composite does NOT depend on stays loose instead of being boxed by
// name alone. `generate` depends only on `md-generate`; a standalone `pgo-generate`
// must not land in a `generate` stage (and with one real worker, no box forms).
func TestWriteTargetGraphMermaidStageExcludesNonDependency(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{{
		Path:   ".",
		Engine: "buzz",
		Nodes: []types.TargetGraphNode{
			{Name: "md-generate"},
			{Name: "pgo-generate"},
			{Name: "generate", Dependencies: []string{"md-generate"}},
		},
	}}}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphMermaid(&b, out))
	got := b.String()
	assert.NotContains(t, got, "subgraph stage_generate", "a single real worker should not form a stage box")
	// pgo-generate is not a dependency of generate, so it stays a loose node, not a
	// stage member, and draws no edge into generate.
	assert.NotContains(t, got, "pgo_generate --> generate", "pgo-generate must not edge into generate")
	for _, want := range []string{`pgo_generate("pgo-generate")`, "md_generate --> generate"} {
		assert.Contains(t, got, want, "output missing %q", want)
	}
}

func TestWriteTargetGraphMermaidMultiProject(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{
		{Path: "api", Engine: "buzz", Nodes: []types.TargetGraphNode{{Name: "build", Dependencies: []string{"fmt"}}, {Name: "fmt"}}},
		{Path: "web", Engine: "buzz", Nodes: []types.TargetGraphNode{{Name: "build"}}},
		{Path: "legacy", Engine: "lua"}, // no nodes: dropped
	}}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphMermaid(&b, out))
	got := b.String()
	// Two projects each have a `build`; the prefix keeps them distinct.
	for _, want := range []string{`subgraph p0["api"]`, `subgraph p1["web"]`, "p0_fmt --> p0_build"} {
		assert.Contains(t, got, want, "output missing %q", want)
	}
	assert.NotContains(t, got, "legacy", "empty (lua) project should be dropped")
}

// TestWriteTargetGraphDOTCrossProject pins that DOT — which is flat and has no
// subgraphs — drops cross-project edges rather than emitting an edge to a phantom
// `p0`/`p1` group id. Mermaid keeps those edges; DOT cannot represent them.
func TestWriteTargetGraphDOTCrossProject(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{
		{Path: "api", Engine: "buzz", Nodes: []types.TargetGraphNode{{Name: "build"}}},
		{Path: "web", Engine: "buzz", DependsOn: []string{"api"}, Nodes: []types.TargetGraphNode{{Name: "build", Dependencies: []string{"fmt"}}, {Name: "fmt"}}},
	}}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphDOT(&b, out))
	got := b.String()
	// Real intra-project edges survive, keyed by DOTID (path:target); run order, so
	// the dependency points at the target that needs it.
	assert.Contains(t, got, `"web:fmt" -> "web:build"`, "DOT missing intra-project edge")
	// No edge references a subgraph id — that would be a phantom node in flat DOT.
	for _, bad := range []string{`"p0"`, `"p1"`, "-> \"p0\"", "\"p1\" ->"} {
		assert.NotContains(t, got, bad, "DOT should not reference subgraph id (phantom node)")
	}
}

// TestWriteTargetGraphMarkdownQueryLinks pins that routing query cells are
// plain inline code when explorerURL is empty, and markdown links when it is set.
func TestWriteTargetGraphMarkdownQueryLinks(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{{
		Path: ".", Engine: "buzz", Nodes: []types.TargetGraphNode{{Name: "build"}},
	}}}
	routing := &types.KnowledgeRouting{
		SchemaVersion: 1, NodeCount: 10, EdgeCount: 20,
		Kinds:    []types.KnowledgeRoutingKind{{Kind: "spell", Count: 3, Anchors: []string{"go"}}},
		Projects: []types.KnowledgeRoutingProject{{Path: "pkg/foo", TargetCount: 2, KeyTargets: []string{"ci"}}},
	}

	// Without explorerURL: query cells are plain inline code, no href.
	var plain bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&plain, out, routing, "", nil))
	plainStr := plain.String()
	assert.Contains(t, plainStr, "`magus query kind:spell`", "query cell should be inline code without explorerURL")
	assert.NotContains(t, plainStr, "#q=", "no #q= link without explorerURL")

	// With explorerURL: query cells are links wrapping inline code.
	// Spaces must be %20 (PathEscape), not '+' (QueryEscape): the browser fragment
	// is decoded with decodeURIComponent, which turns %20 back to a space but
	// leaves '+' as a literal plus character, which would corrupt multi-word queries.
	const explorerURL = "https://example.com/graph/"
	var withLink bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&withLink, out, routing, explorerURL, nil))
	linkedStr := withLink.String()
	// url.PathEscape encodes spaces as %20 and slashes as %2F but leaves colons
	// unescaped (colons are valid in a URI path component). decodeURIComponent in
	// the browser handles all three correctly, so the query round-trips cleanly.
	assert.Contains(t, linkedStr, "#q=magus%20query%20kind:spell", "kind query cell should have #q= link with %20 spaces")
	assert.Contains(t, linkedStr, "#q=magus%20query%20project:pkg%2Ffoo", "project query cell should have #q= link with %20 spaces")
	assert.NotContains(t, linkedStr, "#q=magus+query", "query link must not use + encoding (breaks decodeURIComponent in the browser)")
	// The link text is still the inline-code form.
	assert.Contains(t, linkedStr, "[`magus query kind:spell`]", "query link text should be inline code")
}

// TestQueryCellEncodingRoundTrip confirms that queryCell encodes spaces as %20
// so that url.PathUnescape (equivalent to the browser's decodeURIComponent) recovers
// the original query string exactly. The old url.QueryEscape encoded spaces as '+'
// which decodeURIComponent would NOT decode back to a space, corrupting multi-word
// queries like "magus query kind:spell" into "magus+query+kind:spell".
func TestQueryCellEncodingRoundTrip(t *testing.T) {
	queries := []string{
		"magus query kind:spell",
		"magus query project:pkg/foo",
		"magus explain internal/cache",
		"single",
	}
	const explorerURL = "https://example.com/graph/"
	for _, q := range queries {
		cell := queryCell(q, explorerURL)
		// Extract the href from the Markdown link: [...](href)
		start := strings.Index(cell, "](") + 2
		end := strings.LastIndex(cell, ")")
		require.Greater(t, end, start, "queryCell output is not a Markdown link: %s", cell)
		href := cell[start:end]

		// Find the #q= fragment and unescape it as the browser would.
		qIdx := strings.Index(href, "#q=")
		require.GreaterOrEqual(t, qIdx, 0, "no #q= in href: %s", href)
		encoded := href[qIdx+3:]
		decoded, err := url.PathUnescape(encoded)
		require.NoError(t, err, "url.PathUnescape failed for %q", encoded)
		assert.Equal(t, q, decoded, "round-trip failed: encoded %q -> decoded %q (want %q)", encoded, decoded, q)

		// Spaces must be %20, never '+'.
		assert.NotContains(t, encoded, "+", "encoded fragment must not contain '+' (would break decodeURIComponent): %s", encoded)
	}
}

// TestWriteTargetGraphMarkdownRenderDeterministic confirms that two calls to
// WriteTargetGraphMarkdown with the same input produce byte-identical output -
// the property that lets MAGUS.md back a drift gate.
func TestWriteTargetGraphMarkdownRenderDeterministic(t *testing.T) {
	const explorerURL = "https://example.com/graph/"
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{
		{Path: "pkg/foo", Engine: "buzz", Nodes: []types.TargetGraphNode{
			{Name: "build", Doc: "Build it.", Dependencies: []string{"fmt"}},
			{Name: "fmt", Doc: "Format it."},
		}},
	}}
	routing := &types.KnowledgeRouting{
		SchemaVersion: 1, NodeCount: 5, EdgeCount: 7,
		Kinds:    []types.KnowledgeRoutingKind{{Kind: "spell", Count: 2}},
		Projects: []types.KnowledgeRoutingProject{{Path: "pkg/foo", TargetCount: 2}},
	}
	var first, second bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&first, out, routing, explorerURL, []string{"rw"}))
	require.NoError(t, WriteTargetGraphMarkdown(&second, out, routing, explorerURL, []string{"rw"}))
	assert.Equal(t, first.String(), second.String(), "WriteTargetGraphMarkdown output is not deterministic")
}
