package job

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func regeneratingWorkspace() Workspace {
	return Workspace{Projects: []*types.Project{{
		Path:          ".",
		TargetInputs:  map[string][]types.InputRef{"diagrams": {{Glob: "internal/ledger/*.go"}}},
		TargetOutputs: map[string][]types.OutputRef{"diagrams": {{Glob: "gen/ledger/*.svg"}}},
		TargetUpdates: map[string][]types.UpdateRef{"fmt": {{Glob: "internal/ledger/*.go"}}},
	}}}
}

func TestWaitNotesARegeneratedOutputInTheDiff(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	rep := passingResult()
	rep.ChangedPaths = append(rep.ChangedPaths, "gen/ledger/a.svg")

	bare := verifyClaim(row, rep, passingRun, nil)
	assert.False(t, bare.Verified)
	assert.Contains(t, strings.Join(bare.Violations, "\n"), "gen/ledger/a.svg")

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal/ledger"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "internal/ledger/report.go"), nil, 0o644))
	seen := claimed(rep)
	seen.Regenerated = RegeneratedDelivered(root, row, seen.Changed, regeneratingWorkspace())
	got := VerifyGates(row, rep, passingRun, nil, nil, seen)
	require.True(t, got.Verified, "%v", got.Violations)
	assert.Contains(t, strings.Join(got.Notes, "\n"), ".:diagrams")
}

func TestWaitStillRefusesAnInPlaceUpdateOutsideTheWritePaths(t *testing.T) {
	t.Parallel()

	row := acceptRow()
	row.Paths.Write = []string{"cmd/magus/ledger.go"}
	rep := passingResult()
	rep.ChangedPaths = []string{"cmd/magus/ledger.go", "internal/ledger/b.go"}
	seen := claimed(rep)
	seen.Regenerated = RegeneratedDelivered(t.TempDir(), row, seen.Changed, Workspace{Projects: []*types.Project{{
		Path:          ".",
		Sources:       []types.Glob{{Pattern: "cmd/magus/*.go"}},
		TargetUpdates: map[string][]types.UpdateRef{"fmt": {{Glob: "internal/ledger/*.go"}}},
	}}})
	got := VerifyGates(row, rep, passingRun, nil, nil, seen)
	assert.False(t, got.Verified)
	assert.Contains(t, strings.Join(got.Violations, "\n"), "internal/ledger/b.go")
}
