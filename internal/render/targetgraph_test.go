package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
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
	if err := WriteTargetGraphMarkdown(&b, out, nil); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{
		"# Targets",
		"## Quick start",
		"## Project: .",
		"### `build`",                         // per-target heading
		"Build the binary.",                   // doc line
		"**Defaults**",                        // base invocation block label
		"magus run build",                     // project-dir form
		"magus run build .",                   // workspace form
		"**Charms**",                          // charm variants live in their own block
		"magus run build:rw",                  // charm example command
		"mutate in place instead of checking", // the rw charm's gloss
		"**Depends on:**",                     // dependency list header
		"- [`fmt`](#fmt)",                     // each dep links to its own section
		"### `fmt`",
		"## Glossary",
		"**Dependency graph**", // each project's graph renders inline in its section
		"```mermaid",
		"build --> fmt",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("markdown output missing %q\n---\n%s", want, got)
		}
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
	if err := WriteTargetGraphMarkdown(&b, out, nil); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, "## Project: magus") {
		t.Errorf("heading should use the repo-relative path:\n%s", got)
	}
	// The invocation example still addresses the project by its workspace path.
	if !strings.Contains(got, "magus run build .") {
		t.Errorf("invocation example should keep the workspace-relative path:\n%s", got)
	}
	if i, j := strings.Index(got, "### `build`"), strings.Index(got, "### `worker`"); i < 0 || j < 0 || i > j {
		t.Errorf("primary target should precede the worker:\n%s", got)
	}
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
	if err := WriteTargetGraphMarkdown(&b, out, nil); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if strings.Contains(got, "## Workspace overview") {
		t.Errorf("the workspace-overview graph should be gone:\n%s", got)
	}
	if n := strings.Count(got, "**Dependency graph**"); n != 2 {
		t.Errorf("want one inline graph per project (2), got %d:\n%s", n, got)
	}
	// Per-project graphs stay flat — no multi-project `p0_`-style prefixes.
	if strings.Contains(got, "subgraph p0") || strings.Contains(got, "p0_build") {
		t.Errorf("per-project graphs should render flat, without multi-project prefixes:\n%s", got)
	}
	// A project-level depends_on (web -> api, no target-level external) is no longer
	// drawn: there is no coarse project box or project→project arrow anywhere.
	for _, bad := range []string{"xproj_self", "xdep_", `-.->|"depends on"|`} {
		if strings.Contains(got, bad) {
			t.Errorf("project-level depends_on should not render a coarse cross-project edge (found %q):\n%s", bad, got)
		}
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
			Policy: &types.TargetPolicy{NoCache: true, Isolated: true},
		},
	}
	var b bytes.Buffer
	if err := WriteTargetGraphMarkdown(&b, out, eval); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{
		"<summary><b>Shared defaults</b>", // collapsed shared block
		"sources  **/*.go, go.mod",
		"outputs  bin/app",
		"md (claims: **/*.md)",
		"**Executes**",           // per-target rendered command, in a code block
		"go build",               // the command itself (no inline backticks)
		"uncached (always runs)", // non-default behavior on gen
		"isolated (runs alone, no concurrent targets)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("markdown output missing %q\n---\n%s", want, got)
		}
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
	if !ok {
		t.Fatal("expected a default entry for the project")
	}
	for i := 0; i < 100; i++ {
		got, _ := projectDefaults(".", eval)
		if got.Target != first.Target {
			t.Fatalf("projectDefaults is nondeterministic: returned %q then %q", first.Target, got.Target)
		}
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
	if err := WriteTargetGraphMarkdown(&b, out, nil); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{
		"## Reading the graphs",
		`subgraph legend["Legend"]`,
		`lg_anchor("Invoke directly")`,
		`lg_ext[["Other project"]]`,
		`lg_spell{{"Spell"}}`,
		"**Toolchain** graph",
		"**cross-project dependency**",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("legend missing %q:\n%s", want, got)
		}
	}
}

// TestWriteTargetGraphMarkdownCrossTargetDependencies pins target-level cross-project dependencies:
// a target with a CrossDependency draws a dotted "needs" edge to an external
// [[project:target]] node, and the coarse project→project arrow is suppressed for
// any project already covered at target granularity.
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
	if err := WriteTargetGraphMarkdown(&b, out, nil); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{
		`xt_api_compile[["api:compile"]]`,    // external target node
		`build -.->|"needs"| xt_api_compile`, // granular dotted edge from the target
	} {
		if !strings.Contains(got, want) {
			t.Errorf("granular cross-target dep missing %q:\n%s", want, got)
		}
	}
	// api is covered at target granularity, so the coarse project arrow is gone.
	for _, bad := range []string{"xdep_api", `subgraph xproj_self`, `-.->|"depends on"|`} {
		if strings.Contains(got, bad) {
			t.Errorf("coarse project arrow should be suppressed when covered granularly (found %q):\n%s", bad, got)
		}
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
	if err := WriteTargetGraphMarkdown(&b, out, nil); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, "graph LR") {
		t.Errorf("per-project graph should read left-to-right (graph LR):\n%s", got)
	}
	// Layout spacing rides in the Mermaid frontmatter config (no ELK on GitHub).
	for _, want := range []string{"config:", "nodeSpacing:", "rankSpacing:"} {
		if !strings.Contains(got, want) {
			t.Errorf("per-project graph should carry layout spacing config; missing %q:\n%s", want, got)
		}
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
	if err := WriteTargetGraphMermaid(&b, out); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	// A single project with no shared-suffix targets renders flat — no project
	// wrapper, no stage box, no id prefix.
	if strings.Contains(got, "subgraph") {
		t.Errorf("single project with no stages should not emit a subgraph:\n%s", got)
	}
	for _, want := range []string{`build("build")`, `fmt("fmt")`, "build --> fmt"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
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
	if err := WriteTargetGraphMermaid(&b, out); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	// Targets are plain nodes; no per-target spell subgraph or spell box survives.
	for _, want := range []string{`lint("lint")`, `noop("noop")`} {
		if !strings.Contains(got, want) {
			t.Errorf("target should be a plain node; missing %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{`subgraph lint`, "lint_s0", "go: golangci-lint"} {
		if strings.Contains(got, bad) {
			t.Errorf("dependency graph should not box spells (found %q):\n%s", bad, got)
		}
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
	if err := WriteTargetGraphMarkdown(&b, out, nil); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{
		"**Toolchain**",
		"graph TB", // top-down, unlike the LR dependency graph
		`sp_go{{"go"}}`,
		`sp_md{{"md"}}`,
		`t_lint("lint")`,
		`t_lint -->|"golangci-lint, go-vet"| sp_go`,
		`t_lint -->|"markdownlint"| sp_md`,
		`t_test -->|"go-test"| sp_go`, // the go spell node is shared, not duplicated
	} {
		if !strings.Contains(got, want) {
			t.Errorf("toolchain graph missing %q:\n%s", want, got)
		}
	}
	// One shared go spell node, declared once.
	if n := strings.Count(got, `sp_go{{"go"}}`); n != 1 {
		t.Errorf("go spell node should be declared once, got %d:\n%s", n, got)
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
	if err := WriteTargetGraphMermaid(&b, out); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, `subgraph stage_generate["generate"]`) {
		t.Errorf("expected a 'generate' stage subgraph:\n%s", got)
	}
	// `release` has a unique suffix, so it must not be boxed.
	if strings.Contains(got, `stage_release`) {
		t.Errorf("singleton 'release' should stay loose:\n%s", got)
	}
	// `generate` and the lone `release` are anchors (nothing depends on them); the
	// `*-generate` workers are leaves.
	for _, want := range []string{
		"classDef anchor",
		"class generate,release anchor",
		"class docs_generate,man_generate leaf",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestWriteTargetGraphMermaidMultiProject(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{
		{Path: "api", Engine: "buzz", Nodes: []types.TargetGraphNode{{Name: "build", Dependencies: []string{"fmt"}}, {Name: "fmt"}}},
		{Path: "web", Engine: "buzz", Nodes: []types.TargetGraphNode{{Name: "build"}}},
		{Path: "legacy", Engine: "lua"}, // no nodes: dropped
	}}
	var b bytes.Buffer
	if err := WriteTargetGraphMermaid(&b, out); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	// Two projects each have a `build`; the prefix keeps them distinct.
	for _, want := range []string{`subgraph p0["api"]`, `subgraph p1["web"]`, "p0_build --> p0_fmt"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "legacy") {
		t.Errorf("empty (lua) project should be dropped:\n%s", got)
	}
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
	if err := WriteTargetGraphDOT(&b, out); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	// Real intra-project edges survive, keyed by DOTID (path:target).
	if want := `"web:build" -> "web:fmt"`; !strings.Contains(got, want) {
		t.Errorf("DOT missing intra-project edge %q:\n%s", want, got)
	}
	// No edge references a subgraph id — that would be a phantom node in flat DOT.
	for _, bad := range []string{`"p0"`, `"p1"`, "-> \"p0\"", "\"p1\" ->"} {
		if strings.Contains(got, bad) {
			t.Errorf("DOT should not reference subgraph id %q (phantom node):\n%s", bad, got)
		}
	}
}
