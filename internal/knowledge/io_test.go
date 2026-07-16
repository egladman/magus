package knowledge

import (
	"fmt"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

// TestAssembleIO: a target's declared outputs/inputs become produces/consumes edges to
// the file and doc nodes they match; a project-relative glob is resolved against the
// project path; an output with no matching node contributes nothing; and a glob broad
// enough to exceed maxIOFanout is dropped whole (the god-node guard) rather than fanned
// out.
func TestAssembleIO(t *testing.T) {
	pathToNode := map[string]string{
		"docs/spells/go.md": "doc:docs/spells/go.md",
		"docs/spells/md.md": "doc:docs/spells/md.md",
		"docs/src/foo.go":   "file:docs/src/foo.go",
	}
	// Enough markdown to push a `**/*.md` glob past the fan-out cap.
	for i := 0; i < maxIOFanout+5; i++ {
		p := fmt.Sprintf("docs/wide/p%02d.md", i)
		pathToNode[p] = "doc:" + p
	}

	projects := []types.TargetGraphProject{{
		Path: "docs",
		Nodes: []types.TargetGraphNode{
			{Name: "gen", Outputs: []string{"spells/*.md"}, Inputs: []string{"src/foo.go"}},
			{Name: "ghost", Outputs: []string{"nowhere/*.md"}}, // matches no node
			{Name: "wide", Outputs: []string{"**/*.md"}},       // too broad -> guard drops it
		},
	}}
	out := mergeAll([]Shard{assembleIO(projects, pathToNode)}).Output()

	// A specific output glob links exactly the docs it matches; an input links the file.
	assert.True(t, hasEdge(out, "target:docs:gen", "doc:docs/spells/go.md", types.RelationProduces))
	assert.True(t, hasEdge(out, "target:docs:gen", "doc:docs/spells/md.md", types.RelationProduces))
	assert.True(t, hasEdge(out, "target:docs:gen", "file:docs/src/foo.go", types.RelationConsumes))

	// A glob matching no node contributes no phantom edge.
	for _, e := range out.Links {
		assert.NotEqual(t, "target:docs:ghost", e.Source, "an unmatched output glob mints no edge")
	}

	// The over-broad `**/*.md` glob is dropped whole - no produces edges from `wide`.
	for _, e := range out.Links {
		assert.NotEqual(t, "target:docs:wide", e.Source, "a glob over the fan-out cap is dropped, not fanned out")
	}
}

// TestRoleFromRel pins the universal, workspace-agnostic filename conventions the doc
// role is derived from - never a magus-specific name.
func TestRoleFromRel(t *testing.T) {
	cases := map[string]string{
		"README.md":                      "readme",
		"docs/getting-started/readme.md": "readme",
		"AGENTS.md":                      "agent",
		"CLAUDE.md":                      "agent",
		".claude/skills/x/SKILL.md":      "skill",
		"CHANGELOG.md":                   "changelog",
		"CONTRIBUTING.md":                "contributing",
		"LICENSE.md":                     "license",
		"docs/cache.md":                  "doc",
		"some/nested/design-notes.md":    "doc",
	}
	for rel, want := range cases {
		assert.Equal(t, want, roleFromRel(rel), "role of %s", rel)
	}
}

// TestSkipDocWalkDir: the doc walk descends into meaningful hidden dirs (.claude, .github)
// and skips only genuine noise.
func TestSkipDocWalkDir(t *testing.T) {
	for _, name := range []string{".git", ".magus", "node_modules", "vendor", "gen", "target", "dist"} {
		assert.True(t, skipDocWalkDir("/repo/"+name, name), "%s is skipped", name)
	}
	for _, name := range []string{".claude", ".github", "docs", "cmd"} {
		assert.False(t, skipDocWalkDir("/repo/"+name, name), "%s is descended", name)
	}
}
