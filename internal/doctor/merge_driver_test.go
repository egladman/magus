package doctor

import (
	"os/exec"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckOwedRegeneration: an empty record passes, and a recorded one fails naming
// the run that settles it, so a stale generated file is visible before it is committed.
func TestCheckOwedRegeneration(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v\n%s", err, out)
	}
	r := &runner{ws: rootStubWorkspace{root: root}}

	assert.Equal(t, types.Check{Name: "owed-regeneration", Status: types.CheckOK,
		Message: "no merge left a regeneration owed"}, r.checkOwedRegeneration())

	ok, err := vcs.RecordOwedRegeneration(t.Context(), root, vcs.OwedRegeneration{Project: ".", Target: "generate", Paths: []string{"MAGUS.md"}})
	require.NoError(t, err)
	require.True(t, ok)
	path, err := vcs.OwedRegenerationPath(t.Context(), root)
	require.NoError(t, err)

	assert.Equal(t, types.Check{
		Name:    "owed-regeneration",
		Status:  types.CheckFail,
		Message: "1 regeneration(s) a merge kept one side for have not run, so those generated files are stale",
		Details: []string{
			hint.Run.With("generate:rw", ".") + " (1 kept file(s): MAGUS.md)",
			"settle them with `" + hint.JobRun.With("regenerate-owed") + "`, or `magus server regenerate-owed` without a daemon",
			"record: " + path,
		},
	}, r.checkOwedRegeneration())
}

// The registration is a command line, not a path, and vcs/git.go quotes the executable
// when it contains a space. Reading it back wrong would probe the wrong thing and report
// a working driver as broken, or the reverse.
func TestDriverExecutableUnwrapsTheRegistration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		registered string
		want       string
	}{
		{"a plain path", "/usr/local/bin/magus vcs merge-driver %O %A %B %L %P", "/usr/local/bin/magus"},
		{"a quoted path with a space", `"/Users/a b/magus" vcs merge-driver %O %A`, "/Users/a b/magus"},
		{"a bare name from PATH", "magus vcs merge-driver %O", "magus"},
		{"no arguments at all", "/usr/local/bin/magus", "/usr/local/bin/magus"},
		{"an unterminated quote is not an executable", `"/Users/a b/magus vcs merge-driver`, ""},
		{"nothing registered", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, driverExecutable(tc.registered))
		})
	}
}

// A failing magus prints its diagnostic first and its usage after, and the diagnostic is
// the part that names the cause.
func TestFirstLineKeepsTheDiagnostic(t *testing.T) {
	assert.Equal(t, "[error] unknown option \"timeout\"",
		firstLine("\n[error] unknown option \"timeout\"\nUsage: magus ...\n"))
	assert.Empty(t, firstLine("   \n  "))
}
