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

func TestWithTarget_CheckClean(t *testing.T) {
	p := &types.Project{Path: "."}
	opt := WithTarget("test", FailOnDrift())
	require.NoError(t, opt(p))
	pol := p.TargetPolicies["test"]
	assert.True(t, pol.FailOnDrift)
}

func TestWithTarget_TrackVolatile(t *testing.T) {
	p := &types.Project{Path: "."}
	opt := WithTarget("build", RetryOnVolatile())
	require.NoError(t, opt(p))
	pol := p.TargetPolicies["build"]
	assert.True(t, pol.RetryOnVolatile)
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
	opt := WithTarget("goBuild", SkipCache())
	require.NoError(t, opt(p))
	assert.True(t, p.TargetPolicies["go-build"].SkipCache)
	assert.NotContains(t, p.TargetPolicies, "goBuild")

	p2 := &types.Project{Path: "."}
	opt2 := WithTarget("go-build", SkipCache())
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

func TestWithClaim(t *testing.T) {
	b := &types.Binding{Name: "myspell"}
	opt := WithClaim("**/*.ts", "**/*.tsx")
	require.NoError(t, opt(b))
	assert.Len(t, b.AddedClaims, 2)
}

func TestWithoutClaim(t *testing.T) {
	b := &types.Binding{Name: "myspell"}
	opt := WithoutClaim("**/*.json")
	require.NoError(t, opt(b))
	assert.Len(t, b.RemovedClaims, 1)
}

func TestWithClaimWeight(t *testing.T) {
	b := &types.Binding{Name: "myspell"}
	opt := WithClaimWeight(10)
	require.NoError(t, opt(b))
	assert.Equal(t, 10, b.ClaimWeight)
}
