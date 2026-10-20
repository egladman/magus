package symbols

import (
	"path/filepath"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// occurrenceFixture holds a symbol whose occurrences span two documents and whose display
// name sits in the second, one whose display name sits only outside the workspace, a
// duplicated site, and a local symbol that must never appear.
func occurrenceFixture() *scip.Index {
	def := occAt(10, 5, 3)
	def.SymbolRoles = int32(scip.SymbolRole_Definition)
	other := occAt(4, 1, 5)
	other.Symbol = "scip-go gomod example.com/foo v1 Other#"
	local := occAt(1, 0, 3)
	local.Symbol = "local 3"
	return &scip.Index{Documents: []*scip.Document{
		{RelativePath: "pkg/foo/foo.go", Occurrences: []*scip.Occurrence{occAt(20, 8, 3), def, occAt(20, 2, 3), occAt(20, 2, 3), local}},
		{RelativePath: "pkg/baz/baz.go", Symbols: []*scip.SymbolInformation{{Symbol: monikerV1, DisplayName: "Bar"}},
			Occurrences: []*scip.Occurrence{other, occAt(7, 0, 3)}},
		{RelativePath: "../../../elsewhere/dep.go", Symbols: []*scip.SymbolInformation{{Symbol: "scip-go gomod example.com/foo v1 Other#", DisplayName: "Other"}},
			Occurrences: []*scip.Occurrence{occAt(3, 0, 3)}},
	}}
}

func TestOccurrenceFileRoundTripsOneKeyAtATime(t *testing.T) {
	all, err := IndexOccurrences(t.Context(), occurrenceFixture(), "")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "index.occ")
	require.NoError(t, WriteOccurrenceFile(path, "stamp-1", all))

	for key, want := range all {
		got, err := ReadKeyOccurrences(path, "stamp-1", key)
		require.NoError(t, err)
		assert.Equalf(t, want, got, "occurrences of %s", key)
	}

	missing, err := ReadKeyOccurrences(path, "stamp-1", "gomod example.com/foo Absent#")
	require.NoError(t, err)
	assert.Empty(t, missing.Files)

	_, err = ReadKeyOccurrences(path, "stamp-2", "gomod example.com/foo Bar#")
	assert.ErrorIs(t, err, ErrOccurrenceFileStale, "a file built from another index is never read")
	_, err = ReadKeyOccurrences(filepath.Join(t.TempDir(), "absent.occ"), "stamp-1", "gomod example.com/foo Bar#")
	assert.ErrorIs(t, err, ErrOccurrenceFileStale)
}
