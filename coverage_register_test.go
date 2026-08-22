package magus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestIgnorePatternConstructors(t *testing.T) {
	assert.Equal(t, types.IgnorePattern{Type: types.PatternGlob, Pattern: "**/*.tmp"}, IgnoreGlob("**/*.tmp"))
	assert.Equal(t, types.IgnorePattern{Type: types.PatternRegex, Pattern: `\.swp$`}, IgnoreRegex(`\.swp$`))
	assert.Equal(t, types.IgnorePattern{Type: types.PatternLiteral, Pattern: "node_modules"}, IgnoreLiteral("node_modules"))
}

// TestWithWatchIgnoreReachesTheProject: the patterns are appended at registration
// time, so a workspace that opened successfully has already validated them.
func TestWithWatchIgnoreReachesTheProject(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
	})

	reg := NewWorkspaceRegistry()
	reg.RegisterProject("api", WithWatchIgnore(IgnoreGlob("**/*.tmp"), IgnoreLiteral("node_modules")))

	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	api := m.Get("api")
	require.NotNil(t, api)
	assert.Subset(t, api.WatchIgnores, []types.IgnorePattern{
		{Type: types.PatternGlob, Pattern: "**/*.tmp"},
		{Type: types.PatternLiteral, Pattern: "node_modules"},
	})
}

// TestWithWatchIgnoreRejectsAMalformedRegexAtOpen: a pattern that cannot compile
// would silently match nothing, so it fails the load instead.
func TestWithWatchIgnoreRejectsAMalformedRegexAtOpen(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
	})

	reg := NewWorkspaceRegistry()
	reg.RegisterProject("api", WithWatchIgnore(IgnoreRegex("(unclosed")))

	_, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	assert.Error(t, err, "an ignore pattern that cannot compile must not open the workspace")
}

// TestWorkspaceRegistryRidesTheContext is how an interpreter recovers the registry
// its Open was given: there is no global, so a bare context yields nil rather than
// a default nobody registered into.
func TestWorkspaceRegistryRidesTheContext(t *testing.T) {
	assert.Nil(t, WorkspaceRegistryFromContext(context.Background()))

	reg := NewWorkspaceRegistry()
	ctx := WithWorkspaceRegistryContext(context.Background(), reg)
	assert.Same(t, reg, WorkspaceRegistryFromContext(ctx))
}

// TestWithoutWorkspaceProvidersLeavesMagusfileProjectsAlone: the option suppresses
// magus\workspace.provider, which a tree with none declared cannot show. What it
// must NOT do is change the declared inventory.
func TestWithoutWorkspaceProvidersLeavesMagusfileProjectsAlone(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
	})

	ws, err := Inspect(context.Background(), root, WithoutWorkspaceProviders())
	require.NoError(t, err)

	paths := make([]string, 0, len(ws.All()))
	for _, p := range ws.All() {
		paths = append(paths, p.Path)
	}
	assert.ElementsMatch(t, []string{".", "api"}, paths)
}
