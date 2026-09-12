package job

import (
	"strings"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// compat: see the legacy fields on types.Declaration. A client one release behind still registers, and
// one that names a lane twice is refused rather than silently taking either spelling.
func TestDecodeDeclarationReadsALaneUnderItsOldName(t *testing.T) {
	t.Parallel()

	row, err := DecodeDeclaration(strings.NewReader(
		`{"schema_version":4,"id":"adj/ledger","owned_paths":["internal/ledger"],` +
			`"forbidden_paths":["MAGUS.md"],"focus":["internal/hint"],"tier":"principal"}`))
	require.NoError(t, err)
	require.Equal(t, types.Declaration{
		SchemaVersion: 4,
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
		`{"schema_version":4,"id":"adj/ledger","owned_paths":["a"],"write_paths":["b"]}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owned_paths")
}

// A field the grader never reads looks to its author exactly like one that was taken into
// account, which is how a report carrying `confidence: high` read as thorough.
func TestDecodeRejectsFieldsNobodyAskedFor(t *testing.T) {
	t.Parallel()

	_, err := DecodeResult(strings.NewReader(
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

			_, err := DecodeResult(strings.NewReader(raw))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "version 1")
			assert.NotContains(t, err.Error(), "next_field", "the version is the reason, not the field")
		})
	}
}

func TestDecodeReportReadsAWellFormedOne(t *testing.T) {
	t.Parallel()

	rep, err := DecodeResult(strings.NewReader(
		`{"schema_version":1,"job":"adj/store","changed_paths":["internal/ledger/store.go"],` +
			`"validation":{"command":"magus run test internal/ledger","output_ref":"out1a2b"},` +
			`"unresolved_risks":["the archive is never read back"]}`))
	require.NoError(t, err)
	assert.Equal(t, "adj/store", rep.Job)
	assert.Equal(t, "out1a2b", rep.Validation.OutputRef)
	assert.Len(t, rep.UnresolvedRisks, 1)
}

// A row a client sends carries what it DECLARES. The store's own fields are not on the
// input type at all, which is what makes sending one an error instead of a lie recorded.
func TestDecodeDeclarationRefusesTheStoresOwnFields(t *testing.T) {
	t.Parallel()

	for _, field := range []struct{ name, member string }{
		{"created", `"created":1`},
		{"registered_by", `"registered_by":{"session":"someone"}`},
		{"releases", `"releases":[]`},
	} {
		_, err := DecodeDeclaration(strings.NewReader(`{"schema_version":4,"id":"adj/store",` + field.member + `}`))
		require.Error(t, err, field.name)
		// The refusal has to NAME the field. An error alone is satisfied by the version
		// check too, so a fixture whose schema_version fell behind would leave this
		// passing while testing nothing it claims to.
		assert.Contains(t, err.Error(), field.name, field.name)
		assert.NotContains(t, err.Error(), "accepts version", field.name)
	}
}

func TestDecodeDeclarationValidatesWhatItRead(t *testing.T) {
	t.Parallel()

	_, err := DecodeDeclaration(strings.NewReader(`{"schema_version":4,"id":"adj store"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a lease id")

	_, err = DecodeDeclaration(strings.NewReader(`{"schema_version":4,"id":"adj/store","state":"done"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no_return")

	row, err := DecodeDeclaration(strings.NewReader(
		`{"schema_version":4,"id":"adj/store","write_paths":["internal/ledger"],"state":"declared"}`))
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
	types.Declaration{ID: "adj/store", Goal: "new"}.Apply(&stored)

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

	assert.ElementsMatch(t, jsonFields(types.Declaration{}), keys(schema.Properties))
}

// The two write doors accept the same CURRENT fields or a row declared on one is not the
// row the other would have recorded. ParseMerge is the MCP tool's decoder and
// types.Declaration is the CLI's. types.Declaration alone still carries the pre-rename
// spellings (owned_paths/forbidden_paths/focus/tier; see types.Declaration.FoldLegacyLanes):
// that compat is its own and was never mirrored into ParseMerge, so it is excluded from
// this comparison rather than asserted as shared vocabulary.
func TestDeclarationAndMergeAcceptTheSameFields(t *testing.T) {
	t.Parallel()

	legacy := map[string]bool{"owned_paths": true, "forbidden_paths": true, "focus": true, "tier": true}
	for _, field := range jsonFields(types.Declaration{}) {
		if field == "schema_version" || field == "id" || legacy[field] {
			continue // the envelope and the key (ParseMerge takes them as arguments), and decode.go's own legacy compat
		}
		var value any = "declared"
		switch field {
		case "write_paths", "deny_paths", "read_paths", "depends_on":
			value = []any{"internal/job"}
		case "read_only":
			value = true
		case "check":
			value = "test internal/job"
		case "validation":
			value = "magus run test internal/job"
		}
		_, err := ParseMerge(map[string]any{field: value})
		assert.NoError(t, err, "magus_job fork rejects %q, which `magus job fork` accepts", field)
	}
	var current []string
	for _, field := range jsonFields(types.Declaration{})[2:] {
		if !legacy[field] {
			current = append(current, field)
		}
	}
	assert.ElementsMatch(t, current, mergeFields, "the two doors name one current vocabulary")
}
