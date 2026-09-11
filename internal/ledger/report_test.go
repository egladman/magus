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
		Lease:        "harness/ledger-accept",
		ChangedPaths: []string{"internal/ledger/report.go", "cmd/magus/ledger.go"},
		Validation: ReportValidation{
			Command:   "magus run go::go-test . -- -run Ledger ./internal/ledger/",
			OutputRef: "a1b2c3d4",
			Passed:    true,
		},
		UnresolvedRisks: []string{},
	}
}

func found(string) (bool, error)   { return true, nil }
func missing(string) (bool, error) { return false, nil }

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
	rep.Validation.Passed = false
	rep.Validation.OutputRef = ""

	v, err := Accept(acceptRow(), rep, found)
	require.NoError(t, err)
	assert.False(t, v.Accepted)
	require.Len(t, v.Violations, 4)
	assert.Contains(t, strings.Join(v.Violations, "\n"), "internal/sessions/store.go")
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

	_, err := Accept(acceptRow(), passingReport(), func(string) (bool, error) {
		return false, errors.New("cache locked")
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
