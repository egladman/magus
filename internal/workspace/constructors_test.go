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
	assert.Equal(t, []types.Glob{{Pattern: "dist/**"}, {Pattern: "bin/**"}}, p.Outputs)
}

// TestWithOutputsIsOneDeclaration pins that a call's exclusions narrow its own globs and
// no earlier call's.
func TestWithOutputsIsOneDeclaration(t *testing.T) {
	p := &types.Project{Path: "."}
	require.NoError(t, WithOutputs("gen/runtime.go")(p))
	require.NoError(t, WithOutputs("!gen/runtime.go", "gen/*.go")(p))
	assert.Equal(t, []types.Glob{
		{Pattern: "gen/runtime.go"},
		{Pattern: "gen/*.go", Except: []string{"gen/runtime.go"}},
	}, p.Outputs)
	assert.True(t, types.MatchGlobs(p.Outputs, "gen/runtime.go"), "the first call still declares it")

	err := WithOutputs("!gen/fs.go")(p)
	require.ErrorContains(t, err, `exclusion "!gen/fs.go" has no glob to narrow`)
}

// TestPlainGlobKeysRefuseAnExclusion covers the keys whose globs take no exclusions: read
// literally, "!x" would match nothing while the author believes it carves x out.
func TestPlainGlobKeysRefuseAnExclusion(t *testing.T) {
	for name, opt := range map[string]ProjectOption{
		"review_required": WithReviewRequired("src/**", "!src/gen/**"),
		"gate_low_risk":   WithGateLowRisk("!docs/**"),
		"merge_low_risk":  WithMergeLowRisk("!x.go"),
	} {
		err := opt(&types.Project{Path: "."})
		require.ErrorContains(t, err, name+" takes no exclusions", name)
	}
}

// TestWithSources pins the STORED form. A glob is cleaned where it is written, so one
// glob is one string however it was spelled, and a glob reaching into a sibling tree
// keeps the reaching spelling that types.RootGlob resolves against this project.
func TestWithSources(t *testing.T) {
	p := &types.Project{Path: "docs"}
	opt := WithSources("./guides/**", "../proto/**/*.proto")
	require.NoError(t, opt(p))
	assert.Equal(t, []types.Glob{{Pattern: "guides/**"}, {Pattern: "../proto/**/*.proto"}}, p.Sources)
	assert.Equal(t, "proto/**/*.proto", p.Sources[1].Root(p.Path).Pattern,
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
