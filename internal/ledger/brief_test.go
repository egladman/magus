package ledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func briefRow() types.Lease {
	return types.Lease{
		ID:     "harness/ledger-per-repo",
		Parent: "harness",
		Goal: "Move the lease ledger to the per-repository state dir.\n" +
			"Done when a row put from one checkout is listed from a second worktree of the same repo.",
		Checkpoint: "cf5509d09",
		WritePaths: []string{"internal/ledger", "cmd/magus/ledger.go"},
		DenyPaths:  []string{"MAGUS.md", "docs/gen"},
		DependsOn:  []string{"harness/session-load"},
		Model:      "principal",
		Validation: "magus run test internal/ledger",
		State:      types.StateDeclared,
	}
}

// The brief is only worth building if two renders of one row agree byte for byte, so the
// golden IS the contract.
func TestBriefRendersTheGolden(t *testing.T) {
	t.Parallel()

	b := NewBrief(briefRow(), BriefFacts{
		Evidence: []BriefEvidence{{Path: "internal/ledger", Node: "dir:internal/ledger", BlastRadius: 4}},
	})
	want, err := os.ReadFile(filepath.Join("testdata", "brief.golden"))
	require.NoError(t, err)
	assert.Equal(t, string(want), b.String())
}

// A cold graph says so once. Silence would read as "nothing depends on any of this",
// which is the opposite of what magus knows.
func TestBriefNamesAColdGraphOnce(t *testing.T) {
	t.Parallel()

	b := NewBrief(briefRow(), BriefFacts{GraphCold: true})
	assert.Equal(t, 1, strings.Count(b.String(), "the knowledge graph is cold"))
}

// A workspace that would not load says so too, for the same reason: a worker in a tree
// whose magusfile is mid-edit still needs the row it was handed.
func TestBriefNamesAWorkspaceThatWouldNotLoad(t *testing.T) {
	t.Parallel()

	b := NewBrief(briefRow(), BriefFacts{WorkspaceCold: true})
	assert.Contains(t, b.String(), "this workspace would not load")
	assert.Contains(t, b.String(), briefRow().Goal, "the row is rendered whatever the workspace does")
}

// Nothing outside the fixed order reaches the worker, which is how the gate stops leaking
// into a brief: `ci` is not rendered because nothing renders it.
func TestBriefRendersOnlyTheRow(t *testing.T) {
	t.Parallel()

	row := briefRow()
	row.Goal, row.DependsOn, row.DenyPaths = "", nil, nil
	got := NewBrief(row, BriefFacts{}).String()

	assert.NotContains(t, got, "goal")
	assert.NotContains(t, got, "depends on")
	assert.NotContains(t, got, "deny paths")
	assert.Contains(t, got, "validation, the only check you run")
	assert.Contains(t, got, row.Validation)
}

// The bootstrap is commands and their reasons, and nothing else. A rules block here was
// read once and ignored; the guard says the rules when a command meets one.
func TestBriefBootstrapIsCommands(t *testing.T) {
	t.Parallel()

	b := NewBrief(briefRow(), BriefFacts{})
	require.Len(t, b.Bootstrap, 3)
	for _, step := range b.Bootstrap {
		assert.NotEmpty(t, step.Run)
		assert.NotEmpty(t, step.Why)
	}
	assert.Equal(t, "magus session lease harness/ledger-per-repo", b.Bootstrap[1].Run)
	assert.Contains(t, b.String(), "magus vcs checkpoint -o name")

	for _, gone := range []string{"skills to load", "Do not commit", "Never `magus affected ci`"} {
		assert.NotContains(t, b.String(), gone, "the record carries no instruction blocks")
	}
}
