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

func TestUsedSchemaKeysDetectsFromDecodedState(t *testing.T) {
	// Detection reads decoded state, not magusfile source, so `"tools": policy.all`
	// counts exactly like an inline literal. A source scan would miss it.
	projects := []*types.Project{
		{Path: "."},
		{Path: "console", ToolBounds: map[string]spells.VersionBounds{"node": {Below: "25"}}},
		{Path: "evals", NoLanguage: "polyglot harness"},
	}
	assert.Equal(t, []string{"no_language", "tools"}, labels(usedSchemaKeys(projects)))
}

// Per-target policy is the vocabulary that actually grew, and it went uncovered while
// the check looked only at top-level options. `timeout` shipped with no floor and then
// deadlocked a workspace whose binary predated it.
func TestUsedSchemaKeysDetectsGatedTargetPolicy(t *testing.T) {
	projects := []*types.Project{{
		Path: ".",
		TargetPolicies: map[string]types.Target{
			"ci":       {Timeout: "45m"},
			"test":     {RetryOnVolatile: true},
			"lint":     {Exclusive: true},
			"generate": {SkipCache: true},
		},
	}}
	assert.Equal(t,
		[]string{"targets[].retry_on_volatile", "targets[].timeout"},
		labels(usedSchemaKeys(projects)),
		"exclusive and skip_cache predate floors, so they need no coverage")
}

// A qualified label is what tells the two `exclusive` keys apart, and it is how a reader
// finds the declaration the floor is being demanded for.
func TestUsedSchemaKeysQualifiesTargetPolicyLabels(t *testing.T) {
	projects := []*types.Project{{Path: ".", TargetPolicies: map[string]types.Target{"ci": {Timeout: "45m"}}}}
	require.Len(t, usedSchemaKeys(projects), 1)
	assert.Equal(t, "targets[].timeout", usedSchemaKeys(projects)[0].label)
}

func TestUsedSchemaKeysIgnoresUngatedKeys(t *testing.T) {
	// name/sources/spells predate floors and carry no Since, so a workspace using only
	// those needs no required_version and must not be nagged for one.
	projects := []*types.Project{{Path: ".", Name: "magus", Sources: []string{"**/*.go"}}}
	assert.Empty(t, usedSchemaKeys(projects))
}

func labels(keys []gatedKey) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.label)
	}
	return out
}

// TestEveryGatedKeyIsDetectable is the pin that makes a Since column mean something.
//
// A key can carry a floor and still be invisible to the check, which is how three
// 0.5.0-gated options sat undetected while the check reported "no version-gated keys in
// use". Declaring the floor and detecting the key are two edits, and only one of them
// fails loudly when it is skipped.
func TestEveryGatedKeyIsDetectable(t *testing.T) {
	// One project exercising every gated key at once: whatever usedSchemaKeys can see,
	// it sees here.
	all := []*types.Project{{
		Path:                ".",
		NoLanguage:          "polyglot harness",
		ToolBounds:          map[string]spells.VersionBounds{"node": {Below: "25"}},
		ReviewRequired:      []string{"internal/secret/**"},
		GateLowRiskDeclared: true,
		GateInheritOff:      true,
		TargetPolicies:      map[string]types.Target{"ci": {Timeout: "45m", RetryOnVolatile: true}},
	}}
	detected := labels(usedSchemaKeys(all))

	for _, o := range types.ProjectOptions {
		if o.Since == "" {
			continue
		}
		assert.Contains(t, detected, o.Key,
			"magus.project key %q declares a floor that doctor cannot detect; add a line to usedSchemaKeys", o.Key)
	}
	for _, o := range types.TargetPolicyOptions {
		if o.Since == "" {
			continue
		}
		assert.Contains(t, detected, "targets[]."+o.Key,
			"target policy key %q declares a floor that doctor cannot detect; add a line to usedSchemaKeys", o.Key)
	}
}

func TestTargetPolicySinceCoversTheKeysThatGrew(t *testing.T) {
	for _, key := range []string{"timeout", "retry_on_volatile"} {
		since, ok := types.TargetPolicySince(key)
		assert.True(t, ok)
		assert.NotEmpty(t, since, "%q postdates the first release and must declare a floor", key)
	}

	since, ok := types.TargetPolicySince("skip_cache")
	assert.True(t, ok)
	assert.Empty(t, since, "a key that predates floors carries no Since")

	_, ok = types.TargetPolicySince("quantum_flux")
	assert.False(t, ok)
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
