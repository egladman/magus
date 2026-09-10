package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sameStepFixture is the 2026-09-10 gate stall as a workspace declares it: `ci` composes a
// generator that writes Go files and a badge that reads every Go file. ordered says whether
// the badge declares the ctx.needs edge that was the fix.
func sameStepFixture(ordered bool) *types.Project {
	var badgeChain []types.ChainStep
	if ordered {
		badgeChain = []types.ChainStep{{Target: "generate"}}
	}
	return &types.Project{
		Path: ".", Name: "root",
		TargetChains: map[string][]types.ChainStep{
			"ci":             {{Target: "generate"}, {Target: "coverage-badge"}},
			"generate":       {{Target: "mocks-generate"}},
			"coverage-badge": badgeChain,
		},
		TargetInputs: map[string][]types.InputRef{
			"coverage-badge": {{Glob: "**/*.go"}},
		},
		TargetOutputs: map[string][]types.OutputRef{
			"mocks-generate": {{Glob: "**/gen/mocks/*.go"}},
			"coverage-badge": {{Glob: "assets/coverage.svg"}},
		},
	}
}

// TestSameStepWritesCheck is MGS4008 standing still. A reader that shares a step with the
// writer of the files it reads has to be met at `magus doctor`, where the fix is a line in
// a magusfile, rather than at a gate twenty minutes in.
func TestSameStepWritesCheck(t *testing.T) {
	// A runner with no workspace, not a nil one: the check resolves cross-project chain
	// steps through r.ws, and a fixture that never exercises that would hide the deref.
	// It does have a root, holding one file both globs match: the check refuses only an
	// overlap a file on disk witnesses, so a fixture with no tree would read as clean.
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gen", "mocks"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gen", "mocks", "store.go"), []byte("package mocks\n"), 0o644))
	r := &runner{root: root}

	t.Run("an unordered reader fails", func(t *testing.T) {
		got := r.checkSameStepWrites([]*types.Project{sameStepFixture(false)})
		assert.Equal(t, types.DoctorFail, got.Status)
		require.Len(t, got.Details, 1)
		assert.Equal(t,
			`root: ci runs . coverage-badge, which reads "**/*.go", alongside . mocks-generate, which writes "**/gen/mocks/*.go", and needs neither from the other (refused at run time)`,
			got.Details[0])
		assert.Contains(t, got.Message, "MGS4008", "the reader gets somewhere to look it up")
	})

	t.Run("the ctx.needs edge silences it", func(t *testing.T) {
		got := r.checkSameStepWrites([]*types.Project{sameStepFixture(true)})
		assert.Equal(t, types.DoctorCheck{
			Name:    "same-step-writes",
			Status:  types.DoctorOK,
			Message: "no composed target runs a reader and a writer of the same files unordered",
		}, got)
	})

	t.Run("a baseline-fallback reader says nothing", func(t *testing.T) {
		p := sameStepFixture(false)
		// No ctx.readsFiles: the badge falls back to the project's source baseline, a
		// whole-project over-approximation, and an overlap through a guess is not a
		// finding a FAIL may rest on.
		p.TargetInputs = nil
		assert.Equal(t, types.DoctorOK, r.checkSameStepWrites([]*types.Project{p}).Status)
	})

	t.Run("a project with no composed target is not a project with no answer", func(t *testing.T) {
		got := r.checkSameStepWrites([]*types.Project{{Path: "docs", Name: "docs"}})
		assert.Equal(t, types.DoctorOK, got.Status)
	})
}

// TestSameStepWritesCheckIsRegistered pins the wiring, not the predicate: a check nothing
// runs reports on nothing, and the failure mode is silence.
func TestSameStepWritesCheckIsRegistered(t *testing.T) {
	var def *checkDef
	for i := range allChecks {
		if allChecks[i].Name == "same-step-writes" {
			def = &allChecks[i]
		}
	}
	require.NotNil(t, def)
	assert.Equal(t, types.UnorderedSameStepWrite, def.Code)
	assert.True(t, def.NeedsWorkspace, "it reads declarations, which only exist once the workspace loads")
}
