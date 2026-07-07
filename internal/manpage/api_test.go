package manpage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAPIUpToDate is the drift gate for the committed public-API snapshot: the
// checked-in testdata/api.lock must equal what API emits today, so the public CLI
// API cannot silently change without the diff appearing in a PR. On failure,
// regenerate:
//
//	go generate ./internal/manpage/...
func TestAPIUpToDate(t *testing.T) {
	committed, err := os.ReadFile(filepath.Join("testdata", "api.lock"))
	require.NoError(t, err, "read testdata/api.lock")
	got := strings.Join(API(config.KnownKeys()), "\n") + "\n"
	assert.Equal(t, string(committed), got,
		"internal/manpage/testdata/api.lock is out of date; run: go generate ./internal/manpage/...")
}
