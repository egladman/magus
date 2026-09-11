package ledger

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func acceptRow() types.Lease {
	return types.Lease{
		ID:         "harness/ledger-accept",
		OwnedPaths: []string{"internal/ledger", "cmd/magus/ledger.go", "docs/reference/*.md"},
		Validation: "magus run go::go-test . -- -run Ledger ./internal/ledger/",
		State:      types.StateRunning,
	}
}

func passingReport() Report {
	return Report{
		SchemaVersion: ReportSchemaVersion,
		Lease:         "harness/ledger-accept",
		ChangedPaths:  []string{"internal/ledger/report.go", "cmd/magus/ledger.go"},
		Validation: ReportValidation{
			Command:   "magus run go::go-test . -- -run Ledger ./internal/ledger/",
			OutputRef: "a1b2c3d4",
		},
		UnresolvedRisks: []string{},
	}
}

// found is a store holding a passing run of acceptRow's own validation, which is what an
// honest report cites.
func found(string) (Attempt, error) {
	return Attempt{Found: true, Project: ".", Target: "go-test", Spell: "go"}, nil
}

func missing(string) (Attempt, error) { return Attempt{}, nil }

func TestAcceptTakesAReportInsideTheBoundary(t *testing.T) {
	t.Parallel()

	v, err := Accept(acceptRow(), passingReport(), found)
	require.NoError(t, err)
	assert.Equal(t, Verdict{Lease: "harness/ledger-accept", Accepted: true}, v)
}

// Every rule reports independently: an orchestrator repartitions for one violation and
// re-runs the check for another, so a verdict that stopped at the first would send it to
// the wrong remedy half the time.
func TestAcceptNamesEveryViolation(t *testing.T) {
	t.Parallel()

	rep := passingReport()
	rep.Lease = "harness/other"
	rep.ChangedPaths = append(rep.ChangedPaths, "internal/sessions/store.go")
	rep.Validation.OutputRef = ""

	v, err := Accept(acceptRow(), rep, found)
	require.NoError(t, err)
	assert.False(t, v.Accepted)
	require.Len(t, v.Violations, 3)
	assert.Contains(t, strings.Join(v.Violations, "\n"), "internal/sessions/store.go")
}

// The report the coding-agent persona filed on 2026-09-11 and had ACCEPTED: nothing
// changed, a ref from an unrelated codegen run, a self-asserted pass, and two fields
// nobody asked for. Every one of them is now a named rejection, which is the whole of
// what "grade evidence, not assertions" means.
func TestAcceptRefusesTheFabricatedReport(t *testing.T) {
	t.Parallel()

	raw := `{"schema_version":1,"lease":"harness/ledger-accept","changed_paths":[],` +
		`"validation":{"command":"magus run go::go-test .","output_ref":"deadbeef","passed":true},` +
		`"unresolved_risks":[],"confidence":"high"}`

	_, err := DecodeReport(strings.NewReader(raw))
	require.Error(t, err, "the unknown members alone stop it at the door")
	assert.Contains(t, err.Error(), "passed")

	// Decoded by hand, as the fields it invented were never read anyway: the two rules
	// that would have caught it even in a report shaped correctly.
	rep := passingReport()
	rep.ChangedPaths = nil
	rep.Validation.OutputRef = "deadbeef"
	v, err := Accept(acceptRow(), rep, func(string) (Attempt, error) {
		return Attempt{Found: true, Project: ".", Target: "generate"}, nil
	})
	require.NoError(t, err)
	assert.False(t, v.Accepted)
	require.Len(t, v.Violations, 2)
	assert.Contains(t, v.Violations[0], "no changed paths at all")
	assert.Contains(t, v.Violations[1], "magus run generate .")
	assert.Contains(t, v.Violations[1], "go::go-test")
}

// The stored run's own exit status is the verdict, which is the field a worker no longer
// gets to assert.
func TestAcceptReadsTheStoredRunsOutcome(t *testing.T) {
	t.Parallel()

	v, err := Accept(acceptRow(), passingReport(), func(string) (Attempt, error) {
		return Attempt{Found: true, Project: ".", Target: "go-test", Spell: "go", Failed: true}, nil
	})
	require.NoError(t, err)
	assert.False(t, v.Accepted)
	require.Len(t, v.Violations, 1)
	assert.Contains(t, v.Violations[0], "failed")
}

// A row with no check has nothing to bind evidence to, and accepting it anyway is how a
// ref from any run at all passes for evidence.
func TestAcceptRefusesARowWithNoCheckToBindTo(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.Validation = "make test"

	v, err := Accept(row, passingReport(), found)
	require.NoError(t, err)
	assert.False(t, v.Accepted)
	assert.Contains(t, v.Violations[0], "not a `magus run <target> <project>` line")
}

// A charm, a flag, the binary's spelling and the args after `--` are all ways of running
// one target, so none of them may decide whether the evidence binds.
func TestCheckBindsOnIdentityNotSpelling(t *testing.T) {
	t.Parallel()

	att := Attempt{Found: true, Project: "internal/ledger", Target: "test"}
	for _, validation := range []string{
		"magus run test internal/ledger",
		"./magus run test:rw internal/ledger",
		"magus run test internal/ledger -s -- -run Ledger",
	} {
		c, ok := parseCheck(validation)
		require.True(t, ok, validation)
		assert.True(t, c.matches(att), validation)
	}

	c, ok := parseCheck("magus run test cmd/magus")
	require.True(t, ok)
	assert.False(t, c.matches(att), "another project is another run")
}

// A directory declaration covers what is under it and a glob covers only what it matches.
// The second half is the one worth pinning: falling back to a glob's literal prefix would
// accept exactly the writes the declaration excludes.
func TestAcceptReadsDeclarationsAsWrittenIn(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		path string
		want bool
	}{
		"under a declared directory":  {"internal/ledger/store.go", true},
		"the declared file itself":    {"cmd/magus/ledger.go", true},
		"matching a declared glob":    {"docs/reference/lease.md", true},
		"below a declared glob":       {"docs/reference/manpage/magus-run.md", false},
		"a sibling of a declared dir": {"internal/ledgers/store.go", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rep := passingReport()
			rep.ChangedPaths = []string{tc.path}
			v, err := Accept(acceptRow(), rep, found)
			require.NoError(t, err)
			assert.Equal(t, tc.want, v.Accepted, v.Violations)
		})
	}
}

// The ref is the whole of the evidence, so a ref that no longer resolves is a rejection
// rather than a detail: the root cannot reopen a run that is not there.
func TestAcceptRejectsEvidenceTheStoreDoesNotHold(t *testing.T) {
	t.Parallel()

	v, err := Accept(acceptRow(), passingReport(), missing)
	require.NoError(t, err)
	assert.False(t, v.Accepted)
	assert.Contains(t, v.Violations[0], "a1b2c3d4")
}

// A store that cannot answer is not a worker filing a bad ref. Folding the two together
// would reject an honest report whenever the cache was unreadable.
func TestAcceptSurfacesAStoreThatCannotAnswer(t *testing.T) {
	t.Parallel()

	_, err := Accept(acceptRow(), passingReport(), func(string) (Attempt, error) {
		return Attempt{}, errors.New("cache locked")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cache locked")
}

// A read-only lease has no write set, so any claimed write is a violation of a boundary
// the row declared by being read-only rather than by listing paths.
func TestAcceptRefusesWritesFromAReadOnlyLease(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.ReadOnly, row.OwnedPaths = true, nil

	v, err := Accept(row, passingReport(), found)
	require.NoError(t, err)
	assert.False(t, v.Accepted)
	assert.Contains(t, v.Violations[0], "read-only")
}

// The schema is what a host compiles a worker's response format against, so a field on
// one side and not the other is a report that validates and cannot be graded, or one
// that is graded on a field no worker was asked for.
func TestReportSchemaMatchesTheStruct(t *testing.T) {
	t.Parallel()

	var schema struct {
		Properties  map[string]json.RawMessage `json:"properties"`
		Definitions struct {
			Validation struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"validation"`
		} `json:"definitions"`
	}
	require.NoError(t, json.Unmarshal([]byte(ReportSchema), &schema))

	assert.ElementsMatch(t, jsonFields(Report{}), keys(schema.Properties))
	assert.ElementsMatch(t, jsonFields(ReportValidation{}), keys(schema.Definitions.Validation.Properties))
}

// The row schema is what a person reads before typing `register --stdin`, so the same
// drift rule applies to it: a field on one side and not the other is a row that validates
// and is not stored, or one that is stored and nobody was told to send.
func TestRowSchemaMatchesTheStruct(t *testing.T) {
	t.Parallel()

	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal([]byte(RowSchema), &schema))

	assert.ElementsMatch(t, jsonFields(Row{}), keys(schema.Properties))
}

// The two write doors accept the same fields or a row declared on one is not the row the
// other would have recorded. Merge is the MCP tool's decoder and Row is the CLI's; this
// is what keeps the pair from drifting into two vocabularies.
func TestRowAndMergeAcceptTheSameFields(t *testing.T) {
	t.Parallel()

	for _, field := range jsonFields(Row{}) {
		if field == "schema_version" || field == "id" {
			continue // the envelope and the key, which Merge takes as arguments
		}
		var value any = "declared"
		switch field {
		case "owned_paths", "forbidden_paths", "focus", "depends_on":
			value = []any{"internal/ledger"}
		case "read_only":
			value = true
		}
		_, err := Merge(map[string]any{field: value})
		assert.NoError(t, err, "magus_ledger put rejects %q, which `ledger register` accepts", field)
	}
	assert.ElementsMatch(t, jsonFields(Row{})[2:], mergeFields, "the two doors name one vocabulary")
}

// jsonFields is the wire name of every field a struct serializes, which is the set the
// schema has to describe.
func jsonFields(v any) []string {
	t := reflect.TypeOf(v)
	out := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		out = append(out, name)
	}
	return out
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
