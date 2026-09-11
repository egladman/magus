package ledger

import (
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A field the grader never reads looks to its author exactly like one that was taken into
// account, which is how a report carrying `confidence: high` read as thorough.
func TestDecodeRejectsFieldsNobodyAskedFor(t *testing.T) {
	t.Parallel()

	_, err := DecodeReport(strings.NewReader(
		`{"schema_version":1,"changed_paths":["a.go"],"validation":{"command":"c","output_ref":"r"},` +
			`"unresolved_risks":[],"confidence":"high"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "confidence")
}

// The version is read before anything else, so a sender one release ahead is told what
// this magus accepts rather than watching a field it was told to send be called unknown.
func TestDecodeNamesTheVersionsItAccepts(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"a version from the future": `{"schema_version":2,"changed_paths":[],"validation":{"command":"c","output_ref":"r"},"unresolved_risks":[],"next_field":1}`,
		"no version at all":         `{"changed_paths":[],"validation":{"command":"c","output_ref":"r"},"unresolved_risks":[]}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := DecodeReport(strings.NewReader(raw))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "version 1")
			assert.NotContains(t, err.Error(), "next_field", "the version is the reason, not the field")
		})
	}
}

func TestDecodeReportReadsAWellFormedOne(t *testing.T) {
	t.Parallel()

	rep, err := DecodeReport(strings.NewReader(
		`{"schema_version":1,"lease":"adj/store","changed_paths":["internal/ledger/store.go"],` +
			`"validation":{"command":"magus run test internal/ledger","output_ref":"out1a2b"},` +
			`"unresolved_risks":["the archive is never read back"]}`))
	require.NoError(t, err)
	assert.Equal(t, "adj/store", rep.Lease)
	assert.Equal(t, "out1a2b", rep.Validation.OutputRef)
	assert.Len(t, rep.UnresolvedRisks, 1)
}

// A row a client sends carries what it DECLARES. The store's own fields are not on the
// input type at all, which is what makes sending one an error instead of a lie recorded.
func TestDecodeRowRefusesTheStoresOwnFields(t *testing.T) {
	t.Parallel()

	for _, field := range []string{`"created":1`, `"registered_by":{"session":"someone"}`, `"releases":[]`} {
		_, err := DecodeRow(strings.NewReader(`{"schema_version":1,"id":"adj/store",` + field + `}`))
		require.Error(t, err, field)
	}
}

func TestDecodeRowValidatesWhatItRead(t *testing.T) {
	t.Parallel()

	_, err := DecodeRow(strings.NewReader(`{"schema_version":1,"id":"adj store"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a lease id")

	_, err = DecodeRow(strings.NewReader(`{"schema_version":1,"id":"adj/store","state":"done"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no_return")

	row, err := DecodeRow(strings.NewReader(
		`{"schema_version":1,"id":"adj/store","owned_paths":["internal/ledger"],"state":"declared"}`))
	require.NoError(t, err)
	assert.Equal(t, types.StateDeclared, row.State)
}

// An empty stdin is the shape of a pipeline that produced nothing, and reading it as an
// empty record would record a row that erases the one it names.
func TestDecodeRefusesAnEmptyInput(t *testing.T) {
	t.Parallel()

	_, err := DecodeRow(strings.NewReader("  \n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

// A declaration REPLACES: an omitted field is cleared, which is what a person typing the
// row means, and the store's own fields survive it.
func TestRowApplyDeclaresRatherThanMerges(t *testing.T) {
	t.Parallel()

	stored := types.Lease{
		ID: "adj/store", Goal: "old", Tier: "principal",
		OwnedPaths: []string{"internal/ledger"},
		Registered: 42,
	}
	Row{ID: "adj/store", Goal: "new"}.Apply(&stored)

	assert.Equal(t, "new", stored.Goal)
	assert.Empty(t, stored.Tier, "an omitted field is cleared")
	assert.Empty(t, stored.OwnedPaths)
	assert.Equal(t, int64(42), stored.Registered, "what the store computed is not the declaration's to drop")
}
