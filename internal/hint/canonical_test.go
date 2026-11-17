package hint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// TestCanonicalSpellingTeachesCharm covers the one place a run is told it spelled
// a charm non-canonically. Every other path resolves the spelling silently by design (a
// spell arm, RunCI and the cache key all see the canonical form), so if this line is not
// offered, nothing tells anyone which spelling magus will report back.
func TestCanonicalSpellingTeachesCharm(t *testing.T) {
	target, err := types.ParseTarget("format:UPDATE")
	require.NoError(t, err)

	assert.Equal(t, []string{`charm "UPDATE" is canonically "update"`}, CanonicalSpelling(target))
}

func TestCanonicalSpellingIsSilentOnCanonicalInput(t *testing.T) {
	target, err := types.ParseTarget("format:update")
	require.NoError(t, err)

	assert.Empty(t, CanonicalSpelling(target))
}

func TestCanonicalSpellingTeachesTheTargetName(t *testing.T) {
	target := types.Target{Name: "go-build", Declared: "Go_Build"}

	assert.Equal(t, []string{`target "Go_Build" is canonically "go-build"; both work, "go-build" is what magus reports`}, CanonicalSpelling(target))
}
