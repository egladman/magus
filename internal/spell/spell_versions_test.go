package spell

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

// TestPrintSpellVersionsSeparatesFailureFromSilence pins the three readings a probe can
// have: a version, a tool that reported none, and a probe that failed. Collapsing any two
// would make a broken toolchain look like a quiet one.
func TestPrintSpellVersionsSeparatesFailureFromSilence(t *testing.T) {
	var empty strings.Builder
	PrintSpellVersions(&empty, nil)
	assert.Empty(t, empty.String())

	var b strings.Builder
	PrintSpellVersions(&b, []types.SpellVersion{
		{Tool: "go", Version: "go1.25.0", CacheKey: "abc123"},
		{Tool: "node", Error: "executable file not found"},
		{Tool: "tsc"},
	})
	out := b.String()

	assert.Contains(t, out, "versions (probed just now, never cached)")
	assert.Contains(t, out, "go1.25.0")
	assert.Contains(t, out, "key: abc123")
	assert.Contains(t, out, "probe failed: executable file not found")
	assert.Contains(t, out, "(no version reported)")
}
