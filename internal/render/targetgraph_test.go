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
			{Name: "build", Doc: "Build the binary.", Deps: []string{"fmt"}, Charms: []string{"rw"}},
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
		"## `.`",
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
		"## Dependency graph",
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
			{Name: "build", Deps: []string{"worker"}}, // primary
		},
	}}}
	var b bytes.Buffer
	if err := WriteTargetGraphMarkdown(&b, out, nil); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, "## `magus`") {
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

func TestWriteTargetGraphMermaidSingleProject(t *testing.T) {
	out := types.TargetGraphOutput{Projects: []types.TargetGraphProject{{
		Path:   ".",
		Engine: "buzz",
		Nodes: []types.TargetGraphNode{
			{Name: "build", Deps: []string{"fmt"}},
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
			{Name: "generate", Deps: []string{"man-generate", "docs-generate"}},
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
		{Path: "api", Engine: "buzz", Nodes: []types.TargetGraphNode{{Name: "build", Deps: []string{"fmt"}}, {Name: "fmt"}}},
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
