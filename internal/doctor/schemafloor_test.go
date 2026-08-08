package doctor

import (
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

func TestUsedProjectOptionsDetectsFromDecodedState(t *testing.T) {
	// Detection reads decoded state, not magusfile source, so `"tools": policy.all`
	// counts exactly like an inline literal. A source scan would miss it.
	projects := []*types.Project{
		{Path: "."},
		{Path: "console", ToolBounds: map[string]spells.VersionBounds{"node": {Below: "25"}}},
		{Path: "evals", NoLanguage: "polyglot harness"},
	}
	assert.Equal(t, []string{"no_language", "tools"}, usedProjectOptions(projects))
}

func TestUsedProjectOptionsIgnoresUngatedKeys(t *testing.T) {
	// name/sources/spells predate floors and carry no Since, so a workspace using only
	// those needs no required_version and must not be nagged for one.
	projects := []*types.Project{{Path: ".", Name: "magus", Sources: []string{"**/*.go"}}}
	assert.Empty(t, usedProjectOptions(projects))
}

func TestProjectOptionSince(t *testing.T) {
	since, ok := types.ProjectOptionSince("tools")
	assert.True(t, ok)
	assert.NotEmpty(t, since, "a version-gated key must carry the release that introduced it")

	since, ok = types.ProjectOptionSince("sources")
	assert.True(t, ok)
	assert.Empty(t, since, "a key that predates floors carries no Since")

	_, ok = types.ProjectOptionSince("quantum_flux")
	assert.False(t, ok)
}

// The engine and the dry-run host must reject against the same set. They were two
// hand-maintained slices and had already drifted - the dry copy was missing a key the
// engine accepted, so a magusfile could pass a real run and fail a preview.
func TestProjectOptionKeysCoverEveryOption(t *testing.T) {
	keys := types.ProjectOptionKeys()
	assert.Len(t, keys, len(types.ProjectOptions))
	assert.Contains(t, keys, "tools")
	assert.Contains(t, keys, "no_language")
}
