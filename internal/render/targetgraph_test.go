package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	require.NoError(t, WriteTargetGraphMarkdown(&b, out, nil, nil))
	got := b.String()
	for _, want := range []string{
		"# Targets",
		"## Quick start",
		"## Project: (workspace root)",        // bare "." is never used as a heading
		"### `build`",                         // per-target heading
		"Build the binary.",                   // doc line
		"**Defaults**",                        // base invocation block label
		"magus run build",                     // one canonical form; the root project needs no path
		"**Charms**",                          // charm variants live in their own block
		"magus run build:rw",                  // charm example command
		"mutate in place instead of checking", // the rw charm's gloss
		"**Depends on:**",                     // dependency list header
		"- [`fmt`](#fmt)",                     // each dep links to its own section
		"### `fmt`",
		"## Glossary",
		"**Run order**", // each project's graph renders inline in its section
		"```mermaid",
		"fmt --> build", // run order: the dependency points at the target that needs it
	} {
		assert.Contains(t, got, want, "markdown output missing %q", want)
	}
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
	require.NoError(t, WriteTargetGraphMarkdown(&b, out, nil, nil))
	got := b.String()
	assert.Contains(t, got, "## Project: magus", "heading should use the repo-relative path")
	// The root project's invocation needs no path (it is the workspace root), so it
	// renders as the bare command rather than a noisy trailing-dot form.
	assert.Contains(t, got, "magus run build", "root invocation is the bare command")
	assert.NotContains(t, got, "magus run build .", "root project must not render a trailing-dot path")
	i, j := strings.Index(got, "### `build`"), strings.Index(got, "### `worker`")
	require.GreaterOrEqual(t, i, 0, "primary target should precede the worker:\n%s", got)
	require.GreaterOrEqual(t, j, 0, "primary target should precede the worker:\n%s", got)
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
	require.NoError(t, WriteTargetGraphMarkdown(&plain, out, nil, nil))
	assert.NotContains(t, plain.String(), "## Query first")

	routing := &types.KnowledgeRouting{
		SchemaVersion: 1, NodeCount: 42, EdgeCount: 99,
		Kinds:    []types.KnowledgeRoutingKind{{Kind: "spell", Count: 12, Anchors: []string{"go", "buf"}}},
		Projects: []types.KnowledgeRoutingProject{{Path: "pkg/foo", TargetCount: 3, KeyTargets: []string{"ci"}}},
	}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&b, out, nil, routing))
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

// TestWriteTargetGraphMarkdownNestedInvocation pins the invocation form for a
// nested project: one canonical command that names the project path, so it is
// unambiguous when copy-pasted from the repo root (not the cwd-sensitive bare form).
func TestWriteTargetGraphMarkdownNestedInvocation(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{{
		Path:   "pkg/foo",
		Engine: "buzz",
		Nodes:  []types.TargetGraphNode{{Name: "build"}},
	}}}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&b, out, nil, nil))
	got := b.String()
	assert.Contains(t, got, "magus run build pkg/foo", "nested invocation names its project path")
}

// TestWriteTargetGraphMarkdownInlineGraphs pins the graph layout: there is no
// separate workspace-overview graph, each project section carries its own inline
// graph, and a project-level depends_on (an affected/scheduling concern) is NOT
// drawn — only target-level cross-project edges are. (Cross-target edges
// are covered by TestWriteTargetGraphMarkdownCrossTargetDependencies.)
func TestWriteTargetGraphMarkdownInlineGraphs(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{
		{Path: "api", Engine: "buzz", Nodes: []types.TargetGraphNode{{Name: "build", Dependencies: []string{"fmt"}}, {Name: "fmt"}}},
		{Path: "web", Engine: "buzz", DependsOn: []string{"api"}, Nodes: []types.TargetGraphNode{{Name: "build"}}},
	}}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&b, out, nil, nil))
	got := b.String()
	assert.NotContains(t, got, "## Workspace overview", "the workspace-overview graph should be gone")
	assert.Equal(t, 2, strings.Count(got, "**Run order**"), "want one inline graph per project (2)")
	// Per-project graphs stay flat — no multi-project `p0_`-style prefixes.
	assert.NotContains(t, got, "subgraph p0", "per-project graphs should render flat, without multi-project prefixes")
	assert.NotContains(t, got, "p0_build", "per-project graphs should render flat, without multi-project prefixes")
	// A project-level depends_on (web -> api, no target-level external) is no longer
	// drawn: there is no coarse project box or project→project arrow anywhere.
	for _, bad := range []string{"xproj_self", "xdep_", `-.->|"depends on"|`} {
		assert.NotContains(t, got, bad, "project-level depends_on should not render a coarse cross-project edge")
	}
}

// TestWriteTargetGraphMarkdownDispatch confirms the evaluated dispatch plan
// (defaults block, rendered command, non-default policy) renders when eval is
// supplied, and is omitted otherwise.
func TestWriteTargetGraphMarkdownDispatch(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{{
		Path:   ".",
		Engine: "buzz",
		Nodes:  []types.TargetGraphNode{{Name: "build", Doc: "Build."}, {Name: "gen", Doc: "Generate."}},
	}}}
	eval := map[string]types.EvaluatedTargetEntry{
		".\x00build": {
			Project: ".", Target: "build",
			Sources: []string{"**/*.go", "go.mod"},
			Outputs: []string{"bin/app"},
			Spells: []types.EvaluatedSpellEntry{
				{Name: "go", Command: []string{"go", "build"}},
				{Name: "md", EffectiveClaims: []string{"**/*.md"}},
			},
		},
		".\x00gen": {
			Project: ".", Target: "gen",
			Policy: &types.Target{SkipCache: true, Exclusive: true},
		},
	}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&b, out, eval, nil))
	got := b.String()
	for _, want := range []string{
		"<summary><b>Shared defaults</b>", // collapsed shared block
		"sources  **/*.go, go.mod",
		"outputs  bin/app",
		"md (claims: **/*.md)",
		"**Executes**",           // per-target rendered command, in a code block
		"go build",               // the command itself (no inline backticks)
		"uncached (always runs)", // non-default behavior on gen
		"exclusive (runs alone, no concurrent targets)",
	} {
		assert.Contains(t, got, want, "markdown output missing %q", want)
	}
}

// TestProjectDefaultsDeterministic guards the shared-defaults block against the
// regression where it was rendered from whatever target Go's randomized map
// iteration happened to yield first — which made the block (and so MAGUS.md) flap
// between runs once a project's targets weren't perfectly uniform. With several
// targets in a project, projectDefaults must return the same one every call; the
// old map-ranging version fails this within a handful of iterations.
func TestProjectDefaultsDeterministic(t *testing.T) {
	eval := map[string]types.EvaluatedTargetEntry{
		".\x00build": {Project: ".", Target: "build", Sources: []string{"**/*.go"}},
		".\x00gen":   {Project: ".", Target: "gen"},
		".\x00test":  {Project: ".", Target: "test"},
		".\x00lint":  {Project: ".", Target: "lint"},
	}
	first, ok := projectDefaults(".", eval)
	require.True(t, ok, "expected a default entry for the project")
	for i := 0; i < 100; i++ {
		got, _ := projectDefaults(".", eval)
		require.Equal(t, first.Target, got.Target, "projectDefaults is nondeterministic")
	}
}

// TestWriteTargetGraphMarkdownLegend pins the shared legend: a key that names the
// role colors and the external-project shape, plus the notes for the spell box and
// the dotted cross-project arrow.
func TestWriteTargetGraphMarkdownLegend(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{{
		Path:   ".",
		Engine: "buzz",
		Nodes:  []types.TargetGraphNode{{Name: "build"}},
	}}}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&b, out, nil, nil))
	got := b.String()
	for _, want := range []string{
		"## Reading the graphs",
		`subgraph legend["Legend"]`,
		`lg_anchor("Top-level target")`,
		`lg_target("Target")`,
		`lg_ext[["Other project"]]`,
		`lg_spell{{"Spell"}}`,
		"**Toolchain** graph",
		"**cross-project dependency**",
	} {
		assert.Contains(t, got, want, "legend missing %q", want)
	}
}

// TestWriteTargetGraphMarkdownCrossTargetDependencies pins target-level cross-project dependencies:
// a target with a CrossDependency draws a dotted run-order edge from an external
// [[project:target]] node (it runs first), and the coarse project→project arrow is
// suppressed for any project already covered at target granularity.
func TestWriteTargetGraphMarkdownCrossTargetDependencies(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{{
		Path:      "web",
		Engine:    "buzz",
		DependsOn: []string{"api"}, // also declared project-level (e.g. for affected)
		Nodes: []types.TargetGraphNode{
			{Name: "build", CrossDependencies: []types.CrossTargetRef{{Project: "api", Target: "compile"}}},
		},
	}}}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&b, out, nil, nil))
	got := b.String()
	for _, want := range []string{
		`xt_api_compile[["api:compile"]]`, // external target node
		`xt_api_compile -.-> build`,       // granular dotted edge: external runs first, into the target
	} {
		assert.Contains(t, got, want, "granular cross-target dep missing %q", want)
	}
	// api is covered at target granularity, so the coarse project arrow is gone.
	for _, bad := range []string{"xdep_api", `subgraph xproj_self`, `-.->|"depends on"|`} {
		assert.NotContains(t, got, bad, "coarse project arrow should be suppressed when covered granularly")
	}
}

// TestWriteTargetGraphMarkdownDirection pins that per-project graphs read
// left-to-right.
func TestWriteTargetGraphMarkdownDirection(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{{
		Path:   "app",
		Engine: "buzz",
		Nodes:  []types.TargetGraphNode{{Name: "ci", Dependencies: []string{"build"}}, {Name: "build"}},
	}}}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&b, out, nil, nil))
	got := b.String()
	assert.Contains(t, got, "graph LR", "per-project graph should read left-to-right (graph LR)")
	// Layout spacing rides in the Mermaid frontmatter config (no ELK on GitHub).
	for _, want := range []string{"config:", "nodeSpacing:", "rankSpacing:"} {
		assert.Contains(t, got, want, "per-project graph should carry layout spacing config; missing %q", want)
	}
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

// TestWriteTargetGraphMarkdownToolchain pins the separate Toolchain graph: a
// top-down chart drawing each spell-driving target to the spell it drives, with the
// ops on the edge; consolidated so one spell node serves all its targets.
func TestWriteTargetGraphMarkdownToolchain(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{{
		Path:   ".",
		Engine: "buzz",
		Nodes: []types.TargetGraphNode{
			{Name: "lint", Spells: []types.TargetSpellUse{
				{Spell: "go", Ops: []string{"golangci-lint", "go-vet"}},
				{Spell: "md", Ops: []string{"markdownlint"}},
			}},
			{Name: "test", Spells: []types.TargetSpellUse{{Spell: "go", Ops: []string{"go-test"}}}},
		},
	}}}
	var b bytes.Buffer
	require.NoError(t, WriteTargetGraphMarkdown(&b, out, nil, nil))
	got := b.String()
	for _, want := range []string{
		"**Toolchain**",
		"graph TB", // top-down, unlike the LR run-order graph
		`sp_go{{"go"}}`,
		`sp_md{{"md"}}`,
		`t_lint("lint")`,
		`t_lint -->|"golangci-lint, go-vet"| sp_go`,
		`t_lint -->|"markdownlint"| sp_md`,
		`t_test -->|"go-test"| sp_go`, // the go spell node is shared, not duplicated
	} {
		assert.Contains(t, got, want, "toolchain graph missing %q", want)
	}
	// One shared go spell node, declared once.
	assert.Equal(t, 1, strings.Count(got, `sp_go{{"go"}}`), "go spell node should be declared once")
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
