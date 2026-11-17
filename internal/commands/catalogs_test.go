package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/diagnostics"
)

// Every catalog the binary ships registers without a clash, so two built-in prefixes that
// collide fail here rather than at a user's startup.
func TestRegisterDiagnosticCatalogsReservesEveryShippedPrefix(t *testing.T) {
	require.NoError(t, RegisterDiagnosticCatalogs())
	require.NoError(t, RegisterDiagnosticCatalogs(), "registering again is a no-op")
	assert.Subset(t, diagnostics.ReservedPrefixes(), []string{"BZZ", "MGS"})
}
