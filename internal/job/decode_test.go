package job

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// declaration renders one input record at the version this magus accepts, with members
// spliced in after it.
//
// The version is READ from the constant rather than typed as a digit. Every fixture below
// asserts something about a record the decoder got far enough to look INSIDE, so a stale
// digit does not fail them honestly: it fails them at the version check, which is a
// different refusal than the one each one names. TestDecodeDeclarationRefusesTheStoresOwnFields
// says so in its own body and then asserts it, and it is the reason this helper exists.
func declaration(members ...string) *strings.Reader {
	body := fmt.Sprintf(`{"schema_version":%d`, types.JobSchemaVersion)
	for _, m := range members {
		body += "," + m
	}
	return strings.NewReader(body + "}")
}

// compat: see the legacy fields on types.Declaration. A client one release behind still registers, and
// one that names a field twice is refused rather than silently taking either spelling.
func TestDecodeDeclarationReadsAFieldUnderItsOldName(t *testing.T) {
	t.Parallel()

	row, err := decodeDeclaration(declaration(
		`"id":"adj/ledger"`, `"owned_paths":["internal/ledger"]`,
		`"forbidden_paths":["MAGUS.md"]`, `"focus":["internal/hint"]`, `"tier":"principal"`))
	require.NoError(t, err)
	require.Equal(t, types.Declaration{
		Schema:     types.Schema{Version: types.JobSchemaVersion},
		ID:         "adj/ledger",
		WritePaths: []string{"internal/ledger"},
		DenyPaths:  []string{"MAGUS.md"},
		ReadPaths:  []string{"internal/hint"},
		Model:      "principal",
	}, row)
}

func TestDecodeDeclarationRefusesAFieldSpelledBothWays(t *testing.T) {
	t.Parallel()

	_, err := decodeDeclaration(declaration(`"id":"adj/ledger"`, `"owned_paths":["a"]`, `"write_paths":["b"]`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owned_paths")
}

// A field the grader never reads looks to its author exactly like one that was taken into
// account, which is how a report carrying `confidence: high` read as thorough.
func TestDecodeRejectsFieldsNobodyAskedFor(t *testing.T) {
	t.Parallel()

	_, err := DecodeResult(strings.NewReader(
		`{"schema_version":2,"changed_paths":["a.go"],"validation":{"command":"c","output_ref":"r"},` +
			`"unresolved_risks":[],"confidence":"high"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "confidence")
}

// The version is read before anything else, so a sender one release ahead is told what
// this magus accepts rather than watching a field it was told to send be called unknown.
func TestDecodeNamesTheVersionsItAccepts(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"a version from the future": `{"schema_version":3,"changed_paths":[],"validation":{"command":"c","output_ref":"r"},"unresolved_risks":[],"next_field":1}`,
		"no version at all":         `{"changed_paths":[],"validation":{"command":"c","output_ref":"r"},"unresolved_risks":[]}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := DecodeResult(strings.NewReader(raw))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "accepts versions 1 through 2")
			assert.NotContains(t, err.Error(), "next_field", "the version is the reason, not the field")
		})
	}
}

// An older sender's record is a subset of the current shape, so it is read, not refused.
func TestDecodeAcceptsAnOlderVersion(t *testing.T) {
	t.Parallel()

	rep, err := DecodeResult(strings.NewReader(
		`{"schema_version":1,"changed_paths":[],"validation":{"command":"c","output_ref":"r"},"unresolved_risks":[]}`))
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Version)

	row, err := decodeDeclaration(strings.NewReader(`{"schema_version":1,"id":"adj/store"}`))
	require.NoError(t, err)
	assert.Equal(t, types.Declaration{Schema: types.Schema{Version: 1}, ID: "adj/store"}, row)
}

// A record demanding a feature this magus lacks is refused by name; one naming a feature
// it has is read.
func TestDecodeRefusesAnUnmetRequirement(t *testing.T) {
	t.Parallel()

	_, err := decodeDeclaration(declaration(`"requires":["never-implemented"]`, `"id":"adj/store"`))
	assert.Equal(t, fmt.Sprintf(`job: the job requires "never-implemented", which this magus (schema %d) lacks, update magus`,
		types.JobSchemaVersion), err.Error())

	row, err := decodeDeclaration(declaration(`"requires":["claim-declarations"]`, `"id":"adj/store"`))
	require.NoError(t, err)
	assert.Equal(t, []string{"claim-declarations"}, row.Requires)
}

func TestDecodeReportReadsAWellFormedOne(t *testing.T) {
	t.Parallel()

	rep, err := DecodeResult(strings.NewReader(
		`{"schema_version":2,"job":"adj/store","changed_paths":["internal/ledger/store.go"],` +
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
		_, err := decodeDeclaration(declaration(`"id":"adj/store"`, field.member))
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

	_, err := decodeDeclaration(declaration(`"id":"adj store"`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a lease id")

	_, err = decodeDeclaration(declaration(`"id":"adj/store"`, `"state":"done"`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no_return")

	row, err := decodeDeclaration(declaration(
		`"id":"adj/store"`, `"write_paths":["internal/ledger"]`, `"state":"declared"`))
	require.NoError(t, err)
	assert.Equal(t, types.StateDeclared, row.State)
}

// `magus job apply -f` reads a manifest the three ways one arrives: one record, an array,
// or one per line. Each record is decoded strictly, and a bad one is named by position.
func TestDecodeDeclarationsReadsEveryShapeAndNamesABadRecord(t *testing.T) {
	t.Parallel()

	a := declaration(`"id":"a"`)
	one, err := io.ReadAll(a)
	require.NoError(t, err)
	two := fmt.Sprintf(`{"schema_version":%d,"id":"b"}`, types.JobSchemaVersion)
	for name, raw := range map[string]string{
		"an array": fmt.Sprintf("[%s,%s]", string(one), two),
		"one per line": fmt.Sprintf(`%s

%s
`, string(one), two),
	} {
		rows, err := DecodeDeclarations(strings.NewReader(raw))
		require.NoError(t, err, name)
		assert.Equal(t, []string{"a", "b"}, []string{rows[0].ID, rows[1].ID}, name)
	}
	rows, err := DecodeDeclarations(strings.NewReader(string(one)))
	require.NoError(t, err)
	assert.Len(t, rows, 1)

	bad := fmt.Sprintf(`{"schema_version":%d,"id":"c","confidence":"high"}`, types.JobSchemaVersion)
	_, err = DecodeDeclarations(strings.NewReader(fmt.Sprintf("%s\n%s\n%s", string(one), two, bad)))
	require.ErrorContains(t, err, "record 3:")
	assert.Contains(t, err.Error(), "confidence")
	_, err = DecodeDeclarations(strings.NewReader(fmt.Sprintf("%s\n%s", string(one), string(one))))
	require.ErrorContains(t, err, "a is record 1 too")
}

// A member added to types.Job alone becomes one apply silently drops. The list pins that,
// so the field's author decides whether the store computes it or a declaration carries it.
func TestStoreStampedIsWhatJobHasAndDeclarationLacks(t *testing.T) {
	t.Parallel()

	assert.ElementsMatch(t, []string{
		"holder", "releases", "unattributed", "entries", "write_proof", "validated", "reported_base",
		"base_verdict", "registered_by", "registered", "checkout_root", "worktree", "end_reason", "created",
		"updated", "deadline", "result", "attempt", "gate_attempts", "last_run", "integration",
		"waiter_evidence", "model_tier", "rework_rounds", "widenings", "claims", "generation", "attempts",
		"prior", "rejection", "paused", "paused_by", "pause_reason", "paused_until",
	}, storeStamped)
}

// The edit loop hands `magus describe job -o json` back to apply. The store's members ride
// along and are ignored; fork's decoder still refuses them (see
// TestDecodeDeclarationRefusesTheStoresOwnFields), and a misspelled member is an error here.
func TestDecodeDeclarationsIgnoresWhatTheStoreStamped(t *testing.T) {
	t.Parallel()

	stored := fmt.Sprintf(`{"schema_version":%d,"id":"a","write_paths":["internal/job"],"state":"declared",`+
		`"created":1,"updated":2,"deadline":3,"registered":4,"registered_by":{"session":"s"},`+
		`"write_proof":{},"reported_base":"abc","base_verdict":"match","checkout_root":"/w",`+
		`"holder":{},"releases":[],"result":null,"model_tier":{"name":"economy","rank":0,"skills":"full","source":"default"}}`, types.JobSchemaVersion)
	rows, err := DecodeDeclarations(strings.NewReader(stored))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, []string{"internal/job"}, rows[0].WritePaths)
	assert.Empty(t, rows[0].State, "the status a described row prints is the store's, and apply never writes one")

	typo := strings.Replace(stored, `"write_paths"`, `"write_pathz"`, 1)
	_, err = DecodeDeclarations(strings.NewReader(typo))
	require.ErrorContains(t, err, "write_pathz")

	_, err = decodeDeclaration(strings.NewReader(stored))
	require.ErrorContains(t, err, "unknown", "fork's decoder stays strict")
}

// `describe job -o json` nests the row under `job` beside what magus derived for it, and
// apply reads that record back as the row alone.
func TestDecodeDeclarationsUnwrapsADescribedRecord(t *testing.T) {
	t.Parallel()

	plain, err := DecodeDeclarations(strings.NewReader(fmt.Sprintf(
		`{"schema_version":%d,"id":"a","write_paths":["internal/job"]}`, types.JobSchemaVersion)))
	require.NoError(t, err)

	described := `{"job":{"id":"a","write_paths":["internal/job"],"state":"declared","created":1},` +
		`"projects":["."],"graph_cold":true,"goals":{"id":"a"}}`
	rows, err := DecodeDeclarations(strings.NewReader(described))
	require.NoError(t, err)
	assert.Equal(t, plain, rows)

	typo := strings.Replace(described, `"projects"`, `"projectz"`, 1)
	_, err = DecodeDeclarations(strings.NewReader(typo))
	require.EqualError(t, err, fmt.Sprintf("record 1: %v", recordInvalid("projectz",
		errors.New("\"projectz\" beside `job` is not a member `describe job -o json` prints")).Err()))
}

// A stored row prints its check twice, as the record and as the rendered line, and a
// declaration naming both is refused; the line is the store's rendering, so it goes.
func TestDecodeDeclarationsDropsTheRenderedLineBesideACheck(t *testing.T) {
	t.Parallel()

	stored := fmt.Sprintf(`{"schema_version":%d,"id":"a","write_paths":["internal/job/store.go"],`+
		`"check":{"target":"go-test","project":"."},"validation":"magus run go-test ."}`, types.JobSchemaVersion)
	rows, err := DecodeDeclarations(strings.NewReader(stored))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].Check)
	assert.Equal(t, "go-test", rows[0].Check.Target)
	assert.Empty(t, rows[0].Validation)

	line := fmt.Sprintf(`{"schema_version":%d,"id":"a","write_paths":["internal/job/store.go"],"validation":"magus run go-test ."}`, types.JobSchemaVersion)
	rows, err = DecodeDeclarations(strings.NewReader(line))
	require.NoError(t, err)
	assert.Equal(t, "magus run go-test .", rows[0].Validation, "a line alone is the declaration's own check")

	_, err = decodeDeclaration(strings.NewReader(stored))
	require.ErrorContains(t, err, "not both", "fork's decoder stays strict")
}

// An empty stdin is the shape of a pipeline that produced nothing, and reading it as an
// empty record would record a row that erases the one it names.
func TestDecodeRefusesAnEmptyInput(t *testing.T) {
	t.Parallel()

	_, err := decodeDeclaration(strings.NewReader("  \n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

// Input that is no record at all is the record's defect; input that cannot be read is not.
func TestReadInputTypesTheRecordsDefect(t *testing.T) {
	t.Parallel()

	for name, in := range map[string]string{"blank": "  \n", "oversized": strings.Repeat("x", maxInputBytes+1)} {
		_, err := readInput(strings.NewReader(in), "job")
		var invalid *invalidInput
		require.ErrorAs(t, err, &invalid, name)
	}

	_, err := readInput(iotest.ErrReader(errors.New("pipe closed")), "job")
	require.ErrorContains(t, err, "job: read the job: pipe closed")
	var invalid *invalidInput
	assert.NotErrorAs(t, err, &invalid)

	_, err = DecodeResult(strings.NewReader(""))
	require.EqualError(t, err, "job: the result is empty")
}

// A declaration REPLACES: an omitted field is cleared, which is what a person typing the
// row means, and the store's own fields survive it.
func TestDeclarationApplyDeclaresRatherThanMerges(t *testing.T) {
	t.Parallel()

	stored := types.Job{
		ID: "adj/store", Criteria: "old", Model: "principal",
		WritePaths: []string{"internal/ledger"},
		Registered: 42,
	}
	types.Declaration{ID: "adj/store", Criteria: "new"}.Apply(&stored)

	// The declaration's own fields are replaced (the omitted Model and WritePaths cleared), and
	// what the store computed is not the declaration's to drop.
	assert.Equal(t, types.Job{ID: "adj/store", Criteria: "new", Registered: 42}, stored)
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

// A goal naming only its kind and subject is the common one, so the schema must not
// require the expect the decoder fills in, and it states the default a reader would get.
func TestGoalExpectIsOptionalInSchemaAndDecoder(t *testing.T) {
	t.Parallel()

	var schema struct {
		Properties struct {
			Goals struct {
				Items struct {
					Required   []string `json:"required"`
					Properties struct {
						Expect struct {
							Description string `json:"description"`
						} `json:"expect"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"goals"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal([]byte(DeclarationSchema), &schema))
	goal := schema.Properties.Goals.Items
	assert.NotContains(t, goal.Required, "expect")
	assert.Contains(t, goal.Properties.Expect.Description, "passed for check")
	assert.Contains(t, goal.Properties.Expect.Description, "changed for paths and symbol")

	decl, err := decodeDeclaration(declaration(`"id":"adj/store"`, `"goals":[`+
		`{"id":"unit","kind":"check","check":{"target":"test"}},`+
		`{"id":"done","kind":"paths","paths":["internal/job"]}]`))
	require.NoError(t, err)
	var row types.Job
	decl.Apply(&row)
	require.Len(t, row.Goals, 2)
	assert.Equal(t, types.ExpectPassed, row.Goals[0].Expect)
	assert.Equal(t, types.ExpectChanged, row.Goals[1].Expect)
}

func TestDecodeDeclarationCarriesBreaksAndRefusesABlankEntry(t *testing.T) {
	t.Parallel()

	decl, err := decodeDeclaration(declaration(`"id":"adj/store"`, `"breaks":["--old-flag","api.Open"]`))
	require.NoError(t, err)
	var row types.Job
	decl.Apply(&row)
	assert.Equal(t, []string{"--old-flag", "api.Open"}, row.Breaks)

	_, err = decodeDeclaration(declaration(`"id":"adj/store"`, `"breaks":["--old-flag",""]`))
	assert.ErrorContains(t, err, "breaks[1] is blank")
}

// The two write doors accept the same CURRENT fields or a row declared on one is not the
// row the other would have recorded. DecodePut is magus\job.put's decoder and
// types.Declaration is the CLI's. types.Declaration alone still carries the pre-rename
// spellings (owned_paths/forbidden_paths/focus/tier; see types.Declaration.FoldLegacyNames):
// that compat is its own and was never mirrored into DecodePut, so it is excluded from
// this comparison rather than asserted as shared vocabulary.
func TestDeclarationAndMergeAcceptTheSameFields(t *testing.T) {
	t.Parallel()

	legacy := map[string]bool{"owned_paths": true, "forbidden_paths": true, "focus": true, "tier": true}
	// The envelope and the key, which a put names outside its map, decode.go's own legacy
	// compat, checkout, soft_paths and integrates, which only a fork makes, and claim, which
	// only apply proves.
	skipped := func(field string) bool {
		return field == "schema_version" || field == "requires" || field == "id" || field == "checkout" || field == "soft_paths" ||
			field == "integrates" || field == "claim" || legacy[field]
	}
	_, err := DecodePut(map[string]any{"checkout": "/tmp/co"})
	require.ErrorContains(t, err, "which only a fork makes")
	_, err = DecodePut(map[string]any{"integrates": []any{"done-a"}})
	require.ErrorContains(t, err, "which only a fork takes")
	_, err = DecodePut(map[string]any{"claim": []any{}})
	require.ErrorContains(t, err, "which only `magus job apply -f <record>` proves")
	for _, field := range jsonFields(types.Declaration{}) {
		if skipped(field) {
			continue
		}
		var value any = "declared"
		switch field {
		case "write_paths", "deny_paths", "read_paths", "depends_on", "breaks":
			value = []any{"internal/job"}
		case "read_only":
			value = true
		case "max_attempts", "max_claims":
			value = float64(3)
		case "weight":
			value = 0.75
		case "timeout":
			value = "30m"
		case "brief_format":
			value = "buzz"
		case "check":
			value = "test internal/job"
		case "validation":
			value = "magus run test internal/job"
		case "goals":
			value = []any{map[string]any{
				"id":    "unit",
				"check": map[string]any{"target": "test", "project": "internal/job"},
			}}
		case "waivers":
			value = []any{map[string]any{
				"code": string(types.WritePathTooBroad), "subject": "internal/**", "reason": "the root regenerates it",
			}}
		}
		_, err = DecodePut(map[string]any{field: value})
		require.NoError(t, err, "magus\\job.put rejects %q, which `magus job fork` accepts", field)
	}
	var current []string
	for _, field := range jsonFields(types.Declaration{}) {
		if !skipped(field) {
			current = append(current, field)
		}
	}
	assert.ElementsMatch(t, current, mergeFields, "the two doors name one current vocabulary")
	// validation is the rendered spelling of check, which JobPut carries typed.
	assert.ElementsMatch(t, append(jsonFields(types.JobPut{}), "validation"), mergeFields,
		"a JobPut field is named by the key its map spells it with")
}

func TestDecodeStdinDeclarationTakesTheCurrentVersionWhenTheRecordNamesNone(t *testing.T) {
	t.Parallel()

	row, defaulted, err := DecodeStdinDeclaration(strings.NewReader(`{"id":"a","read_only":true}`))
	require.NoError(t, err)
	assert.True(t, defaulted, "the caller says it defaulted")
	assert.Equal(t, types.Declaration{Schema: types.Schema{Version: types.JobSchemaVersion}, ID: "a", ReadOnly: true}, row)

	_, defaulted, err = DecodeStdinDeclaration(declaration(`"id":"a"`, `"read_only":true`))
	require.NoError(t, err)
	assert.False(t, defaulted, "a named version is read as named")

	_, _, err = DecodeStdinDeclaration(strings.NewReader(`{"schema_version":0,"id":"a","read_only":true}`))
	require.ErrorIs(t, err, types.JobRecordInvalid, "a version named 0 is no version, and is refused as before")

	_, _, err = DecodeStdinDeclaration(strings.NewReader(`[]`))
	require.Error(t, err, "a record that is not an object is refused, not defaulted")
}
