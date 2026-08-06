package ward

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestRequiredVersion(t *testing.T) {
	t.Run("a satisfied floor passes", func(t *testing.T) {
		assert.Nil(t, RequiredVersion(">= 0.4.0", "v0.4.0"))
		assert.Nil(t, RequiredVersion(">= 0.4.0", "v0.5.1"))
	})

	t.Run("a too-old build is MGS1021 and names both fixes", func(t *testing.T) {
		d := RequiredVersion(">= 0.4.0", "v0.3.0")
		require.NotNil(t, d)
		assert.Equal(t, types.WorkspaceNeedsNewerMagus, d.Code)
		assert.Contains(t, d.Error(), "v0.3.0")
		assert.Contains(t, d.Error(), ">= 0.4.0")
		assert.Contains(t, d.Error(), "self update", "the remedy must name the local fix")
		assert.Contains(t, d.Error(), "CI", "and the CI fix, which is the other half")
	})

	// Three ways to have nothing to compare, all of which must load rather than block.
	t.Run("no floor declared", func(t *testing.T) {
		assert.Nil(t, RequiredVersion("", "v0.1.0"))
	})
	t.Run("no running version supplied", func(t *testing.T) {
		assert.Nil(t, RequiredVersion(">= 99.0.0", ""), "a library caller has no version to be too old")
	})
	t.Run("an unparsable running version", func(t *testing.T) {
		assert.Nil(t, RequiredVersion(">= 0.4.0", "some-fork-build"),
			"a version this ward cannot read is not the workspace's fault; stranding the user is worse")
	})

	// A dev build is typically NEWER than any release, so blocking it against a floor
	// the working tree itself just raised would be exactly backwards.
	t.Run("an unstamped dev build is never too old", func(t *testing.T) {
		assert.Nil(t, RequiredVersion(">= 99.0.0", DevVersion))
	})

	// A prerelease of the version that satisfies the floor satisfies it. Semver
	// constraints otherwise exclude every prerelease, so v0.4.0-rc1 would be told to
	// upgrade to 0.4.0 - which it already effectively is.
	t.Run("a prerelease of the required version satisfies it", func(t *testing.T) {
		assert.Nil(t, RequiredVersion(">= 0.4.0", "v0.4.0-rc1"))
	})
	t.Run("a prerelease still below the floor does not", func(t *testing.T) {
		assert.NotNil(t, RequiredVersion(">= 0.4.0", "v0.3.0-rc1"))
	})

	// A floor nobody can parse protects nobody, and a silent pass would let a typo
	// read as "no floor declared".
	t.Run("a malformed constraint is reported, not ignored", func(t *testing.T) {
		d := RequiredVersion("at least 0.4", "v0.1.0")
		require.NotNil(t, d)
		assert.Equal(t, types.WorkspaceNeedsNewerMagus, d.Code)
		assert.Contains(t, d.Error(), "not a valid semver constraint")
	})
}

// A source build must satisfy any floor, including one naming a release that does not
// exist yet. This is what makes a floor armable the moment the feature lands rather than
// only after it ships - and shipping is exactly when the floor stops mattering.
func TestRequiredVersion_SourceBuildIsNotBlocked(t *testing.T) {
	for _, running := range []string{
		"v0.3.0-286-ga899daa3",       // git describe: 286 commits past v0.3.0
		"v0.3.0-286-ga899daa3-dirty", // ... with uncommitted changes
		"unknown",                    // unstamped
		"",                           // no version supplied at all
	} {
		t.Run(running, func(t *testing.T) {
			assert.Nil(t, RequiredVersion(">= 99.0.0", running),
				"a dev build is compiled from the workspace it runs, so it cannot lack a feature that workspace uses")
		})
	}
}

// The binary a floor exists to catch: a real release, built elsewhere and shipped, that
// predates the feature the magusfile needs. Without the floor this is the build that
// fails somewhere unrelated - or, for a silently-ignored argument, hangs.
func TestRequiredVersion_ShippedReleaseBelowTheFloorIsRefused(t *testing.T) {
	err := RequiredVersion(">= 0.4.0", "v0.3.1")
	require.Error(t, err)
	assert.ErrorIs(t, err, types.WorkspaceNeedsNewerMagus)
	assert.Contains(t, err.Error(), "v0.3.1", "the message names the build that is too old")
}
