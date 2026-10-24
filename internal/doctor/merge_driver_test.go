package doctor

import (
	"fmt"
	"os/exec"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckOwedRegeneration: an empty record passes, and a recorded one fails naming
// the run that settles it, so a stale generated file is visible before it is committed.
func TestCheckOwedRegeneration(t *testing.T) {
	root := testkit.NewRepo(t, nil).Root()
	r := &runner{ws: rootedWorkspace{root: root}}

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
			fmt.Sprintf("settle them with `%s`, which runs them, stages the result and clears the record", hint.VCSResolve.String()),
			"record: " + path,
		},
	}, r.checkOwedRegeneration())
}

// rootedDetectingWorkspace resolves its VCS by detection, which registeredMergeDriver needs.
type rootedDetectingWorkspace struct{ rootedWorkspace }

func (rootedDetectingWorkspace) VCSOptions() types.VCSOptions { return types.VCSOptions{} }

// A merge driver registered without the diff drivers fails the check naming each missing
// piece and the command that rewires them; once installed, the same registration passes.
// The driver is `true`, so the load probe succeeds and only the diff drivers are in question.
func TestCheckMergeDriverLoadsReportsMissingDiffDrivers(t *testing.T) {
	trueExe, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true executable")
	}
	repo := testkit.NewRepo(t, nil)
	root := repo.Root()
	register := func() { repo.Git("config", "merge.magus.driver", trueExe+" vcs merge-driver %O %A %B %L %P") }
	register()
	r := &runner{ws: rootedDetectingWorkspace{rootedWorkspace{root: root}}}

	assert.Equal(t, types.Check{
		Name:   "merge-driver-loads-workspace",
		Status: types.CheckFail,
		Message: "the merge driver is registered without the diff drivers installed beside it, so " +
			"hunk headers name no declaration and a footprint cannot say what a change touched",
		Details: []string{
			".gitattributes: *.go diff=golang",
			".gitattributes: *.py diff=python",
			".gitattributes: *.rs diff=rust",
			".gitattributes: *.md diff=markdown",
			".gitattributes: *.ts diff=typescript",
			".gitattributes: *.tsx diff=typescript",
			".gitattributes: *.buzz diff=buzz",
			".gitattributes: *.yaml diff=yaml",
			".gitattributes: *.yml diff=yaml",
			"git config diff.golang.xfuncname",
			"git config diff.typescript.xfuncname",
			"git config diff.buzz.xfuncname",
			"git config diff.yaml.xfuncname",
			fmt.Sprintf("install them with `%s`", hint.Init.With("--vcs", "git")),
		},
	}, r.checkMergeDriverLoads())

	installer, ok := vcs.Installer("git")
	require.True(t, ok)
	require.NoError(t, installer.InstallMergeDriver(t.Context(), root, types.MergeDriverGlobs{}))
	register() // the install points the driver at a real magus; the probe needs `true`
	assert.Equal(t, types.Check{Name: "merge-driver-loads-workspace", Status: types.CheckOK,
		Message: "the registered merge driver loads this workspace"}, r.checkMergeDriverLoads())
}

// With no VCS resolving, the remedy says so; it never names a guessed backend.
func TestInstallRemedyNamesNoGuessedBackend(t *testing.T) {
	r := &runner{ws: rootedDetectingWorkspace{rootedWorkspace{root: t.TempDir()}}}
	got := r.installRemedy()
	assert.Contains(t, got, "no VCS resolved")
	assert.NotContains(t, got, "--vcs")
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
