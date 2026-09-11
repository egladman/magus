package job

import (
	"slices"
	"strings"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// compat: see the legacy fields on Declaration. A client one release behind still registers, and
// one that names a lane twice is refused rather than silently taking either spelling.
func TestDecodeDeclarationReadsALaneUnderItsOldName(t *testing.T) {
	t.Parallel()

	row, err := DecodeDeclaration(strings.NewReader(
		`{"schema_version":1,"id":"adj/ledger","owned_paths":["internal/ledger"],` +
			`"forbidden_paths":["MAGUS.md"],"focus":["internal/hint"],"tier":"principal"}`))
	require.NoError(t, err)
	require.Equal(t, Declaration{
		SchemaVersion: 1,
		ID:            "adj/ledger",
		WritePaths:    []string{"internal/ledger"},
		DenyPaths:     []string{"MAGUS.md"},
		ReadPaths:     []string{"internal/hint"},
		Model:         "principal",
	}, row)
}

func TestDecodeDeclarationRefusesALaneSpelledBothWays(t *testing.T) {
	t.Parallel()

	_, err := DecodeDeclaration(strings.NewReader(
		`{"schema_version":1,"id":"adj/ledger","owned_paths":["a"],"write_paths":["b"]}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owned_paths")
}

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
func TestDecodeDeclarationRefusesTheStoresOwnFields(t *testing.T) {
	t.Parallel()

	for _, field := range []string{`"created":1`, `"registered_by":{"session":"someone"}`, `"releases":[]`} {
		_, err := DecodeDeclaration(strings.NewReader(`{"schema_version":1,"id":"adj/store",` + field + `}`))
		require.Error(t, err, field)
	}
}

func TestDecodeDeclarationValidatesWhatItRead(t *testing.T) {
	t.Parallel()

	_, err := DecodeDeclaration(strings.NewReader(`{"schema_version":1,"id":"adj store"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a lease id")

	_, err = DecodeDeclaration(strings.NewReader(`{"schema_version":1,"id":"adj/store","state":"done"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no_return")

	row, err := DecodeDeclaration(strings.NewReader(
		`{"schema_version":1,"id":"adj/store","write_paths":["internal/ledger"],"state":"declared"}`))
	require.NoError(t, err)
	assert.Equal(t, types.StateDeclared, row.State)
}

// An empty stdin is the shape of a pipeline that produced nothing, and reading it as an
// empty record would record a row that erases the one it names.
func TestDecodeRefusesAnEmptyInput(t *testing.T) {
	t.Parallel()

	_, err := DecodeDeclaration(strings.NewReader("  \n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

// A declaration REPLACES: an omitted field is cleared, which is what a person typing the
// row means, and the store's own fields survive it.
func TestDeclarationApplyDeclaresRatherThanMerges(t *testing.T) {
	t.Parallel()

	stored := types.Job{
		ID: "adj/store", Goal: "old", Model: "principal",
		WritePaths: []string{"internal/ledger"},
		Registered: 42,
	}
	Declaration{ID: "adj/store", Goal: "new"}.Apply(&stored)

	assert.Equal(t, "new", stored.Goal)
	assert.Empty(t, stored.Model, "an omitted field is cleared")
	assert.Empty(t, stored.WritePaths)
	assert.Equal(t, int64(42), stored.Registered, "what the store computed is not the declaration's to drop")
}

// The embedded schema is generated, so a field on one side and not the other means the
// generator has not been run: this is the drift gate a `magus run generate` away from
// green, not a second copy to hand-edit.
func TestDeclarationSchemaMatchesTheStruct(t *testing.T) {
	t.Parallel()

	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal([]byte(DeclarationSchema), &schema))

	assert.ElementsMatch(t, jsonFields(Declaration{}), keys(schema.Properties))
}

// The two write doors accept the same fields or a row declared on one is not the row the
// other would have recorded. ParseMerge is the MCP tool's decoder and Declaration is the CLI's.
func TestDeclarationAndMergeAcceptTheSameFields(t *testing.T) {
	t.Parallel()

	for _, field := range jsonFields(Declaration{}) {
		if field == "schema_version" || field == "id" {
			continue // the envelope and the key, which ParseMerge takes as arguments
		}
		var value any = "declared"
		switch field {
		case "write_paths", "deny_paths", "read_paths", "depends_on",
			"owned_paths", "forbidden_paths", "focus":
			value = []any{"internal/ledger"}
		case "read_only":
			value = true
		case "check":
			value = "test internal/ledger"
		case "validation":
			value = "magus run test internal/ledger"
		}
		_, err := ParseMerge(map[string]any{field: value})
		assert.NoError(t, err, "magus_ledger put rejects %q, which `ledger register` accepts", field)
	}
	accepted := slices.Clone(mergeFields)
	for _, pair := range renamedFields {
		accepted = append(accepted, pair[0])
	}
	assert.ElementsMatch(t, jsonFields(Declaration{})[2:], accepted, "the two doors name one vocabulary")
}
