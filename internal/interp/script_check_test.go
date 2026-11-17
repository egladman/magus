package interp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
)

// A checker error carries the code in Code; a resolution failure renders it into Msg.
// Either names the handle, so the note can list the spells that resolve.
func TestUnresolvedSpellHandleReadsEitherPosition(t *testing.T) {
	handle, ok := unresolvedSpellHandle(buzz.Diagnostic{Code: unresolvedImportCode, Msg: `cannot resolve import "magus/spell/gooo"`})
	require.True(t, ok)
	assert.Equal(t, "gooo", handle)

	handle, ok = unresolvedSpellHandle(buzz.Diagnostic{Msg: unresolvedImportCode + `: cannot resolve import "magus/spell/gooo"`})
	require.True(t, ok)
	assert.Equal(t, "gooo", handle)

	_, ok = unresolvedSpellHandle(buzz.Diagnostic{Code: unresolvedImportCode, Msg: `cannot resolve import "lib"`})
	assert.False(t, ok, "only a spell import names a handle")
}
