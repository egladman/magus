package doctor

import (
	"regexp"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkNamePattern is the shape a check name has to hold to be worth publishing as an
// identifier: lowercase, digits, and single hyphens. A name with a space or a slash in it
// is one nobody can type back at magus without quoting it.
var checkNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func TestAllChecksAreNamedAndDocumented(t *testing.T) {
	seen := map[string]bool{}
	for _, def := range allChecks {
		assert.Regexp(t, checkNamePattern, def.Name)
		assert.NotEmpty(t, def.Doc, "check %q has no Doc, so --list cannot say what it looks at", def.Name)
		assert.False(t, seen[def.Name], "check name %q is used twice", def.Name)
		seen[def.Name] = true
		require.NotNil(t, def.run, "check %q has no function", def.Name)
		// Unknown is a RESULT, never a declared default: a check whose registry entry
		// claimed it could never look would be a check with no reason to exist.
		assert.Contains(t,
			[]types.Evidence{types.EvidenceMeasured, types.EvidenceDeclared, types.EvidenceInferred},
			def.Evidence,
			"check %q must declare what its verdict rests on", def.Name)
	}
}

// TestChecksMirrorsRegistry pins the listing to the suite. The whole reason the registry
// exists is that `magus doctor --list` and `magus doctor` must describe the same set;
// a check registered but not listed would advertise a name that never appears in a report.
func TestChecksMirrorsRegistry(t *testing.T) {
	info := Checks()
	require.Len(t, info, len(allChecks))
	for i, def := range allChecks {
		assert.Equal(t, def.Name, info[i].Name)
		assert.Equal(t, def.Doc, info[i].Doc)
		assert.Equal(t, string(def.Code), info[i].Code)
		assert.Equal(t, string(def.Evidence), info[i].Evidence)
		assert.Equal(t, def.NeedsWorkspace, info[i].NeedsWorkspace)
	}
}

// TestCheckCodesAreRoutable guards the one field that can name something that does not
// exist: a check citing an MGS code magus does not define would render a docs link into
// a 404, which is the exact defect checkDiagnosticDocs exists to catch in the other
// direction.
func TestCheckCodesAreRoutable(t *testing.T) {
	known := map[types.DiagnosticCode]bool{}
	for _, c := range types.AllDiagnosticCodes() {
		known[c] = true
	}
	for _, def := range allChecks {
		if def.Code == "" {
			continue
		}
		assert.True(t, known[def.Code], "check %q cites unknown code %s", def.Name, def.Code)
	}
}

// TestRunSkipsWorkspaceChecksWhenLoadFailed pins the contract the NeedsWorkspace field
// encodes: a workspace that will not load still gets the host checks answered, and gets
// them without any check dereferencing the workspace that is not there.
func TestRunSkipsWorkspaceChecksWhenLoadFailed(t *testing.T) {
	report := Run(t.Context(), t.TempDir(), nil, assert.AnError)

	var want int
	for _, def := range allChecks {
		if !def.NeedsWorkspace {
			want++
		}
	}
	require.Len(t, report.Checks, want)
	assert.Equal(t, 1, report.Summary.Fail)

	byName := map[string]types.DoctorCheck{}
	for _, c := range report.Checks {
		byName[c.Name] = c
	}
	require.Contains(t, byName, "workspace")
	assert.Equal(t, types.DoctorFail, byName["workspace"].Status)
	assert.NotContains(t, byName, "vcs-base-ref")
}

// TestEveryRunCheckStatesItsEvidence pins the property the whole field exists for: no
// finding reaches a reader without saying what it rests on. A bare Evidence would render
// as an unqualified verdict, which is the state this replaced.
func TestEveryRunCheckStatesItsEvidence(t *testing.T) {
	report := Run(t.Context(), t.TempDir(), nil, assert.AnError)
	for _, c := range report.Checks {
		assert.NotEmpty(t, c.Evidence, "check %q reported no evidence", c.Name)
	}
}

// TestSkippedCheckReportsUnknown pins the half of the summary arithmetic that can be
// reached without a workspace: a check that could not look says so, rather than
// returning the bare DoctorOK the ok count would then absorb.
func TestSkippedCheckReportsUnknown(t *testing.T) {
	got := (&runner{}).checkCITarget(nil)
	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Equal(t, types.EvidenceUnknown, got.Evidence)
}
