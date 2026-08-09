package registry

import (
	"os"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuzzProducerMatchesThisReader is the seam that moving the aggregator to Buzz
// created: the file is now written by tools/registry.buzz and read by this package,
// in two different languages, with no compiler between them. The fixture is real
// output from that script, so a field renamed on either side fails here.
//
// Regenerate it with:
//
//	REGISTRY_MODE=build REGISTRY_UPSTREAM=cmd/magus-utils/testdata/upstream \
//	  REGISTRY_RELEASES=releases REGISTRY_OUT=internal/registry/testdata \
//	  REGISTRY_EXPIRES=2027-01-01T00:00:00Z REGISTRY_NO_SIGN=1 magus buzz tools/registry.buzz
func TestBuzzProducerMatchesThisReader(t *testing.T) {
	raw, err := os.ReadFile("testdata/buzz-built.json")
	require.NoError(t, err)

	var reg Registry
	require.NoError(t, json.Unmarshal(raw, &reg))

	assert.Equal(t, SchemaVersion, reg.SchemaVersion)
	assert.False(t, reg.GeneratedAt.IsZero(), "generated_at must parse as a time, not just be present")
	assert.Equal(t, "2027-01-01T00:00:00Z", reg.ExpiresAt)
	assert.Equal(t, "endoflife.date", reg.Sources["eol"].Name)

	require.Contains(t, reg.EOL, "nodejs", "keyed by upstream product slug")
	node := reg.EOL["nodejs"]
	assert.NotEmpty(t, node.Label)
	require.NotEmpty(t, node.Cycles)
	assert.NotEmpty(t, node.Cycles[0].Cycle)

	require.NotEmpty(t, reg.Inputs, "the file states what it was built from")
	assert.Len(t, reg.Inputs[0].SHA256, 64)

	require.NotEmpty(t, reg.Releases, "one file, two categories")
	assert.NotEmpty(t, reg.Releases[0].Version)
	assert.NotEmpty(t, reg.Releases[0].Artifacts[0].SHA256, "artifacts stay pinned")
}
