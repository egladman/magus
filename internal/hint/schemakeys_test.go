package hint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckKeysErrorsOnATypoAndIgnoresAKeyFromTheFuture(t *testing.T) {
	t.Parallel()

	known := []string{"skip_cache", "exclusive", "slots"}

	t.Run("a near miss is a typo and stops the load", func(t *testing.T) {
		t.Parallel()
		ignored, err := CheckKeys([]string{"skipcache"}, known, `magus.project: targets["lint"]`)
		require.Error(t, err)
		assert.ErrorContains(t, err, `unknown option "skipcache"`)
		assert.ErrorContains(t, err, `did you mean "skip_cache"`)
		assert.ErrorContains(t, err, "known options: exclusive, skip_cache, slots",
			"the message enumerates the vocabulary in a stable order")
		assert.Empty(t, ignored)
	})

	t.Run("a key close to nothing is ignored rather than fatal", func(t *testing.T) {
		t.Parallel()
		// The regression this whole split exists for: `timeout` reached
		// TargetPolicyOptions after some binaries shipped, and rejecting it there
		// meant no magus command could load the workspace, including the one that
		// would have built a binary new enough to read it.
		ignored, err := CheckKeys([]string{"timeout"}, known, "magus.project")
		require.NoError(t, err)
		assert.Equal(t, []string{"timeout"}, ignored)
	})

	t.Run("recognized keys produce neither", func(t *testing.T) {
		t.Parallel()
		ignored, err := CheckKeys([]string{"slots", "exclusive"}, known, "magus.project")
		require.NoError(t, err)
		assert.Empty(t, ignored)
	})

	t.Run("keys already ignored come back with the typo that stopped the walk", func(t *testing.T) {
		t.Parallel()
		ignored, err := CheckKeys([]string{"quantum_flux", "exclusiv"}, known, "magus.project")
		require.Error(t, err)
		assert.Equal(t, []string{"quantum_flux"}, ignored,
			"a caller must be able to report what it dropped before what it refused")
	})
}

func TestIgnoredKeyAdviceNamesTheUpgradeCommand(t *testing.T) {
	t.Parallel()
	assert.Contains(t, IgnoredKeyAdvice(), SelfUpdate.String())
}
