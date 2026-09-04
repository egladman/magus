package workspace

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithOutputs(t *testing.T) {
	p := &types.Project{Path: "."}
	opt := WithOutputs("dist/**", "bin/**")
	require.NoError(t, opt(p))
	assert.Equal(t, []string{"dist/**", "bin/**"}, p.Outputs)
}

// TestWithSources pins the STORED form. A glob is cleaned where it is written, so one
// glob is one string however it was spelled, and a glob reaching into a sibling tree
// keeps the reaching spelling that types.RootGlob resolves against this project.
func TestWithSources(t *testing.T) {
	p := &types.Project{Path: "docs"}
	opt := WithSources("./guides/**", "../proto/**/*.proto")
	require.NoError(t, opt(p))
	assert.Equal(t, []string{"guides/**", "../proto/**/*.proto"}, p.Sources)
	assert.Equal(t, "proto/**/*.proto", types.RootGlob(p.Path, p.Sources[1]),
		"the reaching glob roots at the workspace, which is the frame the source walk yields")
}

// TestWithSourcesRejectsAWorkspaceEscape is the one reach that cannot be honored. The
// walk starts at the workspace root, so nothing outside it is ever hashed; a glob that
// still points there is stored as a declaration magus silently ignores, which is the
// failure mode the reaching affordance exists to remove, not to reintroduce.
func TestWithSourcesRejectsAWorkspaceEscape(t *testing.T) {
	p := &types.Project{Path: "docs"}
	err := WithSources("../../elsewhere/**")(p)
	require.Error(t, err)
	assert.ErrorContains(t, err, `source glob "../../elsewhere/**" escapes the workspace root`)
	assert.Empty(t, p.Sources, "a rejected declaration stores nothing")
}

func TestWithExclusive(t *testing.T) {
	p := &types.Project{Path: "."}
	opt := WithExclusive()
	require.NoError(t, opt(p))
	assert.True(t, p.Exclusive)
}

func TestWithWatchIgnore_ValidGlob(t *testing.T) {
	p := &types.Project{Path: "."}
	opt := WithWatchIgnore(IgnoreGlob("**/testdata/**"))
	require.NoError(t, opt(p))
	assert.Len(t, p.WatchIgnores, 1)
}

func TestWithWatchIgnore_ValidRegex(t *testing.T) {
	p := &types.Project{Path: "."}
	opt := WithWatchIgnore(IgnoreRegex(`\.tmp$`))
	require.NoError(t, opt(p))
	assert.Len(t, p.WatchIgnores, 1)
}

func TestWithWatchIgnore_ValidLiteral(t *testing.T) {
	p := &types.Project{Path: "."}
	opt := WithWatchIgnore(IgnoreLiteral("vendor"))
	require.NoError(t, opt(p))
	assert.Len(t, p.WatchIgnores, 1)
}

func TestWithTarget_Drift(t *testing.T) {
	p := &types.Project{Path: "."}
	require.NoError(t, WithTarget("test", Drift(types.DriftWarn, ""))(p))
	assert.Equal(t, types.DriftWarn, p.TargetPolicies["test"].Drift)

	// Off carries its reason, which is the half a bare policy cannot state.
	q := &types.Project{Path: "."}
	require.NoError(t, WithTarget("image", Drift(types.DriftOff, "bakes a build timestamp"))(q))
	assert.Equal(t, types.DriftOff, q.TargetPolicies["image"].Drift)
	assert.Equal(t, "bakes a build timestamp", q.TargetPolicies["image"].DriftReason)
}

func TestWithTarget_TrackVolatile(t *testing.T) {
	p := &types.Project{Path: "."}
	opt := WithTarget("build", RetryOnVolatile("hits a shared broker that drops a connection under load"))
	require.NoError(t, opt(p))
	pol := p.TargetPolicies["build"]
	assert.True(t, pol.RetryOnVolatile)
	assert.Equal(t, "hits a shared broker that drops a connection under load", pol.RetryOnVolatileReason)
}

func TestWithTarget_Slots(t *testing.T) {
	p := &types.Project{Path: "."}
	opt := WithTarget("lint", Slots(4))
	require.NoError(t, opt(p))
	pol := p.TargetPolicies["lint"]
	assert.Equal(t, 4, pol.Slots)
}

func TestWithTarget_NormalizesName(t *testing.T) {
	p := &types.Project{Path: "."}
	// Declared camelCase; a policy lookup under kebab-case (post-A1 CLI/ParseTarget
	// normalization) must find it, and vice versa.
	opt := WithTarget("goBuild", SkipCache("test policy"))
	require.NoError(t, opt(p))
	assert.True(t, p.TargetPolicies["go-build"].SkipCache)
	assert.NotContains(t, p.TargetPolicies, "goBuild")

	p2 := &types.Project{Path: "."}
	opt2 := WithTarget("go-build", SkipCache("test policy"))
	require.NoError(t, opt2(p2))
	assert.True(t, p2.TargetPolicies["go-build"].SkipCache)
}

func TestIgnorePatternConstructors(t *testing.T) {
	glob := IgnoreGlob("**/*.tmp")
	assert.Equal(t, "**/*.tmp", glob.Pattern)

	re := IgnoreRegex(`\.log$`)
	assert.Equal(t, `\.log$`, re.Pattern)

	lit := IgnoreLiteral("node_modules")
	assert.Equal(t, "node_modules", lit.Pattern)
}
