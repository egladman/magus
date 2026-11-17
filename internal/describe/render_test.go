package describe

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

func TestRenderCharmsListSummarizesDeclarations(t *testing.T) {
	t.Parallel()
	charms := []types.CharmEntry{{
		Name: "rw", Builtin: true, Default: true, Doc: "writes in place",
		Declarations: []types.CharmDeclaration{{Project: "api", Target: "lint", Spell: "go", Before: []string{"golangci-lint", "run"}, After: []string{"golangci-lint", "run", "--fix"}}},
	}}
	var w bytes.Buffer
	RenderCharms(&w, charms, false)
	assert.Equal(t, "definition: "+types.CharmDefinition+"\n\n"+
		"charms (1):\n"+
		"  rw  [built-in, workspace default]\n"+
		"    writes in place\n"+
		"    declared by 1 target(s)\n", w.String())
}

func TestRenderCharmsDetailShowsTheArgvEdit(t *testing.T) {
	t.Parallel()
	charms := []types.CharmEntry{{
		Name: "rw",
		Declarations: []types.CharmDeclaration{{Project: "api", Target: "lint", Spell: "go", Before: []string{"run"}, After: []string{"run", "--fix"}}},
	}}
	var w bytes.Buffer
	RenderCharms(&w, charms, true)
	assert.Contains(t, w.String(), "    declared by:\n      api:lint  (spell go)\n        base     run\n        + rw     run --fix\n")
}

func TestRenderCharmsDetailNamesAnUndeclaredCharm(t *testing.T) {
	t.Parallel()
	var w bytes.Buffer
	RenderCharms(&w, []types.CharmEntry{{Name: "gha"}}, true)
	assert.Contains(t, w.String(), "    no target in this workspace declares it\n")
}

func TestRenderGraphMarksCyclesAndUnsupportedEngines(t *testing.T) {
	t.Parallel()
	out := types.TargetGraphOutput{
		Definition: "d",
		Projects: []types.TargetGraphProject{
			{Path: "a", Name: "a", Engine: "buzz", Cycle: []string{"x", "y", "x"}, Nodes: []types.TargetGraphNode{
				{Name: "build", Dependencies: []string{"gen", "fetch"}},
				{Name: "gen"},
			}},
			{Path: "b", Name: "b", Engine: "other"},
		},
	}
	var w bytes.Buffer
	RenderGraph(&w, out)
	got := w.String()
	assert.Contains(t, got, "  CYCLE: x -> y -> x\n")
	assert.Contains(t, got, "  build -> gen, fetch\n  gen\n\n")
	assert.Contains(t, got, "(engine other: graph extraction not yet supported)\n\n")
}

func TestRenderTargetsTagsEachKind(t *testing.T) {
	t.Parallel()
	targets := []types.TargetEntry{
		{Name: "ci", Kind: "canonical"},
		{Name: "go-test", Kind: "spell", Spells: []string{"go", "buf"}},
		{Name: "gen", Kind: "custom", Projects: []string{"a", "b"}},
		{Name: "odd"},
	}
	var w bytes.Buffer
	RenderTargets(&w, targets)
	assert.Equal(t, "definition: "+types.TargetDefinition+"\n\n"+
		"targets (4):\n"+
		"  ci  [canonical - affected/pipeline anchor; composed in the magusfile]\n"+
		"  go-test  [spell: go, buf]\n"+
		"  gen  [custom - projects: a, b]\n"+
		"  odd\n", w.String())
}

func TestRenderWorkspacesDistinguishesTheActiveLink(t *testing.T) {
	t.Parallel()
	out := []types.WorkspaceEntry{{
		Root: "/ws", ProjectCount: 3, VCSBaseRef: "main", Concurrency: 4,
		Workspaces: []types.ListedWorkspace{{Name: "self", Root: "/ws"}, {Name: "peer", Dir: "../peer", Root: "/peer"}},
	}}
	var w bytes.Buffer
	RenderWorkspaces(&w, out)
	assert.Contains(t, w.String(), "workspace: /ws\n  projects: 3\n  vcs base ref: main\n  concurrency: 4\n")
	assert.Contains(t, w.String(), "  workspace self: /ws (this one)\n  workspace peer: ../peer -> /peer\n")
}

func TestRenderFilesPrintsAbsenceOnlyWhenAbsent(t *testing.T) {
	t.Parallel()
	files := []types.FileEntry{
		{Path: "a.go", Role: "source", Exists: true},
		{Path: "gone.go", Role: "unclaimed"},
	}
	var w bytes.Buffer
	RenderFiles(&w, types.NewFileReport(files), files, project.Focus{}, false)
	got := w.String()
	assert.Contains(t, got, "a.go\n  role: source\n\n")
	assert.Contains(t, got, "gone.go\n  role: unclaimed\n  exists: false\n\n")
}

type renderSuite struct{ suite.Suite }

func TestRenderSuite(t *testing.T) { suite.Run(t, new(renderSuite)) }

func (s *renderSuite) TestParseSchemaNamesTheTopLevelProperties() {
	doc, names, err := ParseSchema(`{"properties":{"b":{},"a":{}},"title":"t"}`)
	s.Require().NoError(err)
	s.Equal([]string{"a", "b"}, names)
	s.Equal("t", doc["title"])
}

func (s *renderSuite) TestParseSchemaRefusesNonJSON() {
	_, _, err := ParseSchema("not json")
	s.Error(err)
}
