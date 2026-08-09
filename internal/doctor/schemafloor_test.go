package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// floorCheck runs checkSchemaFloor against a workspace declaring required_version and
// one project using a version-gated key ("tools", gated at 0.4.0).
func floorCheck(t *testing.T, requiredVersion string) types.DoctorCheck {
	t.Helper()
	dir := t.TempDir()
	yaml := "concurrency: 2\n"
	if requiredVersion != "" {
		yaml += "required_version: \"" + requiredVersion + "\"\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus.yaml"), []byte(yaml), 0o600))

	gated := &types.Project{Path: ".", Dir: dir,
		ToolBounds: map[string]spells.VersionBounds{"node": {Min: "22"}}}
	return (&runner{root: dir}).checkSchemaFloor([]*types.Project{gated})
}

// A floor set INSIDE the previous series is the case this check exists for and the one
// it used to miss. "tools" needs 0.4.0; ">= 0.3.5" admits 0.3.5 through 0.3.9, none of
// which can load the file. The old probe asked only whether 0.3.0 was admitted - it is
// not - and reported OK.
//
// The consequence is not a cosmetic warning. An unknown magus.project key aborts
// workspace load, which takes down every command including the one that would build a
// magus new enough to read the file.
func TestSchemaFloorCatchesAFloorInsideThePreviousSeries(t *testing.T) {
	got := floorCheck(t, ">= 0.3.5")
	assert.Equal(t, types.DoctorAdvice, got.Status)
	assert.Contains(t, got.Message, "admits a magus older than 0.4.0")
	assert.Contains(t, got.Details, "tools (needs >= 0.4.0)")
}

func TestSchemaFloorAcceptsAFloorThatCoversTheKeysInUse(t *testing.T) {
	assert.Equal(t, types.DoctorOK, floorCheck(t, ">= 0.4.0").Status)
	// Higher than needed is still covered: the question is whether the floor is high
	// enough, never whether it is exactly right.
	assert.Equal(t, types.DoctorOK, floorCheck(t, ">= 0.5.0").Status)
}

func TestSchemaFloorFlagsAFloorBelowTheKeysInUse(t *testing.T) {
	got := floorCheck(t, ">= 0.1.0")
	assert.Equal(t, types.DoctorAdvice, got.Status)
	assert.Contains(t, got.Message, "raise it to")
}

// No floor at all is advice naming the one to add, not silence.
func TestSchemaFloorAdvisesWhenNoFloorIsDeclared(t *testing.T) {
	got := floorCheck(t, "")
	assert.Equal(t, types.DoctorAdvice, got.Status)
	assert.Contains(t, got.Message, `required_version: ">= 0.4.0"`)
}

// A constraint this check cannot evaluate is reported as such rather than silently
// passing - an unparseable floor is not a covered one.
func TestSchemaFloorReportsAnUnevaluableConstraint(t *testing.T) {
	got := floorCheck(t, "not-a-constraint")
	assert.Equal(t, types.DoctorAdvice, got.Status)
	assert.Contains(t, got.Message, "not a constraint this check can evaluate")
}

// A workspace using no gated key needs no floor, whatever it declares.
func TestSchemaFloorIsQuietWithoutGatedKeys(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus.yaml"), []byte("concurrency: 2\n"), 0o600))
	got := (&runner{root: dir}).checkSchemaFloor([]*types.Project{{Path: ".", Dir: dir}})
	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Contains(t, got.Message, "no version-gated magusfile keys")
}
