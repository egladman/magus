package ledger

import (
	"reflect"
	"strings"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func acceptRow() types.Lease {
	check := types.LeaseCheck{Target: "go::go-test", Project: ".", Args: []string{"-run", "Ledger"}}
	return types.Lease{
		ID:         "harness/ledger-accept",
		OwnedPaths: []string{"internal/ledger", "cmd/magus/ledger.go", "docs/reference/*.md"},
		Check:      &check,
		Validation: check.String(),
		State:      types.StateRunning,
	}
}

func passingReport() Report {
	return Report{
		SchemaVersion: ReportSchemaVersion,
		Lease:         "harness/ledger-accept",
		ChangedPaths:  []string{"internal/ledger/report.go", "cmd/magus/ledger.go"},
		Validation: ReportValidation{
			Command:   "magus run go::go-test . -- -run Ledger",
			OutputRef: "a1b2c3d4",
		},
		UnresolvedRisks: []string{},
	}
}

// passingRun is what the output store recorded for a passing run of acceptRow's own
// validation, which is what an honest report cites. The zero Attempt is a ref the store
// does not hold.
var passingRun = Attempt{Found: true, Project: ".", Target: "go-test", Spell: "go"}

func TestGradeTakesAReportInsideTheBoundary(t *testing.T) {
	t.Parallel()

	assert.Equal(t, Verdict{
		Lease:    "harness/ledger-accept",
		Accepted: true,
		Risks:    []string{},
		Command:  "magus run go::go-test . -- -run Ledger",
	}, Grade(acceptRow(), passingReport(), passingRun, nil))
}

// Every rule reports independently: an orchestrator repartitions for one violation and
// re-runs the check for another, so a verdict that stopped at the first would send it to
// the wrong remedy half the time.
func TestGradeNamesEveryViolation(t *testing.T) {
	t.Parallel()

	rep := passingReport()
	rep.Lease = "harness/other"
	rep.ChangedPaths = append(rep.ChangedPaths, "internal/sessions/store.go")
	rep.Validation.OutputRef = ""

	v := Grade(acceptRow(), rep, Attempt{}, nil)
	assert.False(t, v.Accepted)
	require.Len(t, v.Violations, 3)
	assert.Contains(t, strings.Join(v.Violations, "\n"), "internal/sessions/store.go")
}

// The report the coding-agent persona filed on 2026-09-11 and had ACCEPTED: nothing
// changed, a ref from an unrelated codegen run, a self-asserted pass, and two fields
// nobody asked for. Every one of them is now a named rejection, which is the whole of
// what "grade evidence, not assertions" means.
func TestGradeRefusesTheFabricatedReport(t *testing.T) {
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
	v := Grade(acceptRow(), rep, Attempt{Found: true, Project: ".", Target: "generate"}, nil)
	assert.False(t, v.Accepted)
	require.Len(t, v.Violations, 2)
	assert.Contains(t, v.Violations[0], "no changed paths at all")
	assert.Contains(t, v.Violations[1], "magus run generate .")
	assert.Contains(t, v.Violations[1], "go::go-test")
}

// The stored run's own exit status is the verdict, which is the field a worker no longer
// gets to assert.
func TestGradeReadsTheStoredRunsOutcome(t *testing.T) {
	t.Parallel()

	failed := passingRun
	failed.Failed = true
	v := Grade(acceptRow(), passingReport(), failed, nil)
	assert.False(t, v.Accepted)
	require.Len(t, v.Violations, 1)
	assert.Contains(t, v.Violations[0], "failed")
}

// A row with no check has nothing to bind evidence to, and accepting it anyway is how a
// ref from any run at all passes for evidence.
func TestGradeRefusesARowWithNoCheckToBindTo(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.Check, row.Validation = nil, ""

	v := Grade(row, passingReport(), passingRun, nil)
	assert.False(t, v.Accepted)
	assert.Contains(t, v.Violations[0], "declares no check")
}

// A charm, the binary's spelling and the args after `--` are all ways of running one
// target, so none of them may decide whether the evidence binds.
func TestCheckBindsOnIdentityNotSpelling(t *testing.T) {
	t.Parallel()

	att := Attempt{Found: true, Project: "internal/ledger", Target: "test"}
	for _, line := range []string{
		"magus run test internal/ledger",
		"./magus run test:rw internal/ledger",
		"magus run test internal/ledger -- -run Ledger",
	} {
		c, err := types.ParseLeaseRunLine(line)
		require.NoError(t, err, line)
		assert.True(t, bindsTo(c, att), line)
	}

	c, err := types.ParseLeaseRunLine("magus run test cmd/magus")
	require.NoError(t, err)
	assert.False(t, bindsTo(c, att), "another project is another run")
}

// A directory declaration covers what is under it and a glob covers only what it matches.
// The second half is the one worth pinning: falling back to a glob's literal prefix would
// accept exactly the writes the declaration excludes.
func TestGradeReadsDeclarationsAsWrittenIn(t *testing.T) {
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
			v := Grade(acceptRow(), rep, passingRun, nil)
			assert.Equal(t, tc.want, v.Accepted, v.Violations)
		})
	}
}

// A lease over the whole tree covers every path in it. The declaration cleans to ".", and
// reading that as covering nothing made every reported path a violation.
func TestGradeReadsARootDeclarationAsTheWholeTree(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.OwnedPaths = []string{"."}
	assert.True(t, Grade(row, passingReport(), passingRun, nil).Accepted)

	row.OwnedPaths = []string{""}
	assert.False(t, Grade(row, passingReport(), passingRun, nil).Accepted, "a blank declaration claims nothing")
}

// The deny list is read at acceptance too: a path can be inside the owned lane and still
// be one the row was told to leave alone.
func TestGradeRejectsAForbiddenPath(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.ForbiddenPaths = []string{"internal/ledger/store.go"}
	rep := passingReport()
	rep.ChangedPaths = []string{"internal/ledger/store.go"}

	v := Grade(row, rep, passingRun, nil)
	assert.False(t, v.Accepted)
	require.Len(t, v.Violations, 1)
	assert.Contains(t, v.Violations[0], "forbidden")
}

// A descendant the ledger does not carry is a branch of the plan nobody is tracking,
// which is the fact the field was added to surface.
func TestGradeRejectsADescendantNoRowDeclares(t *testing.T) {
	t.Parallel()

	rep := passingReport()
	rep.Descendants = []string{"harness/ledger-accept/child", "harness/ghost"}
	declared := []types.Lease{{ID: "harness/ledger-accept/child"}}

	v := Grade(acceptRow(), rep, passingRun, declared)
	assert.False(t, v.Accepted)
	require.Len(t, v.Violations, 1)
	assert.Contains(t, v.Violations[0], "harness/ghost")
}

// A row somebody already graded is not re-graded: the second verdict overwrites the first
// without anybody being told the first existed.
func TestGradeRefusesARowThatIsAlreadyClosed(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.State = types.StatePass

	v := Grade(row, passingReport(), passingRun, nil)
	assert.False(t, v.Accepted)
	require.Len(t, v.Violations, 1)
	assert.Contains(t, v.Violations[0], "already pass")
}

// The ref is the whole of the evidence, so a ref that no longer resolves is a rejection
// rather than a detail: the root cannot reopen a run that is not there.
func TestGradeRejectsEvidenceTheStoreDoesNotHold(t *testing.T) {
	t.Parallel()

	v := Grade(acceptRow(), passingReport(), Attempt{}, nil)
	assert.False(t, v.Accepted)
	assert.Contains(t, v.Violations[0], "a1b2c3d4")
}

// A read-only lease has no write set, so any claimed write is a violation of a boundary
// the row declared by being read-only rather than by listing paths.
func TestGradeRefusesWritesFromAReadOnlyLease(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.ReadOnly, row.OwnedPaths = true, nil

	v := Grade(row, passingReport(), passingRun, nil)
	assert.False(t, v.Accepted)
	assert.Contains(t, v.Violations[0], "read-only")
}

// The schema is what a host compiles a worker's response format against, and it is
// generated: a field on one side and not the other means the generator has not been run,
// so a report would validate against a shape the grader does not read.
func TestReportSchemaMatchesTheStruct(t *testing.T) {
	t.Parallel()

	var schema, validation struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal([]byte(ReportSchema), &schema))
	require.NoError(t, json.Unmarshal(schema.Properties["validation"], &validation))

	assert.ElementsMatch(t, jsonFields(Report{}), keys(schema.Properties))
	assert.ElementsMatch(t, jsonFields(ReportValidation{}), keys(validation.Properties))
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
