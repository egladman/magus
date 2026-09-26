package magus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// TestSizedRuns: a sized gate's plain steps run together, and each narrowed step runs on
// its own, carrying the narrowing for its op.
func TestSizedRuns(t *testing.T) {
	rep := types.RiskReport{Gate: []types.RiskGateStep{
		{Target: "lint", Projects: []string{".", "docs"}},
		{Target: "test", Projects: []string{"."}, Op: "go::go-test", Packages: []string{"fx/a", "fx/b"}},
		{Target: "build", Projects: []string{"."}},
	}}
	runs := sizedRuns(rep)
	require.Len(t, runs, 2)
	assert.Equal(t, []types.Target{{Path: ".", Name: "lint"}, {Path: "docs", Name: "lint"}, {Path: ".", Name: "build"}}, runs[0].targets)
	assert.Nil(t, runs[0].narrowing)
	assert.Equal(t, []types.Target{{Path: ".", Name: "test"}}, runs[1].targets)
	require.NotNil(t, runs[1].narrowing)
	assert.Equal(t, project.OpNarrowing{Op: "go-test", Bin: "go"}, project.OpNarrowing{Op: runs[1].narrowing.Op, Bin: runs[1].narrowing.Bin})
	assert.Empty(t, sizedRuns(types.RiskReport{}))
}

// TestAssessChangeTiersFromDeclarations: prose tiers from what the workspace declares.
// Markdown nothing in ci's chain reads needs no gate; markdown a chain target reads is
// its input, so the gate runs that reader; a magusfile edit can move the graph itself.
func TestAssessChangeTiersFromDeclarations(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz": `export fun render(ctx: magus\Context, args: [str]) > void {
    ctx.readsFiles("docs/**/*.md");
}
export fun ci(ctx: magus\Context, args: [str]) > void {
    ctx.needs(render);
}
`,
		"docs/guide.md": "# guide\n",
		"notes/v1.md":   "# v1\n",
	})
	m, err := Open(context.Background(), root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	for _, tc := range []struct {
		path string
		tier types.RiskTier
		gate []string
	}{
		{"notes/v1.md", types.RiskTrivial, []string{}},
		{"docs/guide.md", types.RiskScoped, []string{"magus run render . --no-default-charms"}},
		{"magusfile.buzz", types.RiskFull, []string{"magus run ci . --no-default-charms"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rep, err := m.AssessChange(context.Background(), types.TargetCI, AssessOptions{ChangedPaths: []string{tc.path}})
			require.NoError(t, err)
			assert.Equal(t, tc.tier, rep.Tier, rep.Lines())
			assert.Equal(t, []string{"."}, rep.Affected)
			assert.Equal(t, tc.gate, rep.Commands())
		})
	}
}
