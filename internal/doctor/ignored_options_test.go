package doctor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// A dropped key means the policy it declared is not in force, and several are opt-outs
// whose absence is the permissive answer. Load tolerates it so a magusfile from the
// future cannot deadlock the binary that would build its successor; this is where a
// person finds out it happened.
func TestIgnoredOptionsFailsAndNamesTheKey(t *testing.T) {
	got := (&runner{}).checkIgnoredOptions([]*types.Project{
		{Path: ".", Name: "magus"},
		{Path: "console", Name: "console", IgnoredOptions: []string{"gate_something_new"}},
	})

	assert.Equal(t, types.DoctorFail, got.Status, "the workspace asked for something this magus cannot do")
	require.Len(t, got.Details, 1)
	assert.Contains(t, got.Details[0], "gate_something_new")
	assert.Contains(t, got.Message, "not in force")
	assert.Contains(t, got.Message, "magus self update", "it names the fix")
}

func TestIgnoredOptionsIsQuietWhenEveryKeyWasUnderstood(t *testing.T) {
	got := (&runner{}).checkIgnoredOptions([]*types.Project{{Path: ".", Name: "magus"}})
	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Empty(t, got.Details)
}
