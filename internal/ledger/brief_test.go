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
		Checkpoint:     "cf5509d09",
		OwnedPaths:     []string{"internal/ledger", "cmd/magus/ledger.go"},
		ForbiddenPaths: []string{"MAGUS.md", "docs/gen"},
		DependsOn:      []string{"harness/session-load"},
		Tier:           "principal",
		Validation:     "magus run go::go-test . -- -run Ledger ./internal/ledger/",
		State:          types.StateDeclared,
	}
}

// The brief is only worth building if two renders of one row agree byte for byte, so the
// golden IS the contract.
func TestBriefRendersTheGolden(t *testing.T) {
	t.Parallel()

	b := NewBrief(briefRow())
	b.Evidence = []BriefEvidence{
		{Path: "internal/ledger", Node: "dir:internal/ledger", BlastRadius: 4},
	}
	want, err := os.ReadFile(filepath.Join("testdata", "brief.golden"))
	require.NoError(t, err)
	assert.Equal(t, string(want), b.Text())
	assert.Equal(t, b.Text(), b.Text(), "two renders of one row are one brief")
}

// A cold graph says so once. Silence would read as "nothing depends on any of this",
// which is the opposite of what magus knows.
func TestBriefNamesAColdGraphOnce(t *testing.T) {
	t.Parallel()

	b := NewBrief(briefRow())
	b.GraphCold = true
	assert.Equal(t, 1, strings.Count(b.Text(), "the knowledge graph is cold"))
}

// Nothing outside the fixed order reaches the worker, which is how the gate stops leaking
// into a brief: `ci` is not rendered because nothing renders it.
func TestBriefRendersOnlyTheRow(t *testing.T) {
	t.Parallel()

	row := briefRow()
	row.Goal, row.DependsOn, row.ForbiddenPaths = "", nil, nil
	got := NewBrief(row).Text()

	assert.NotContains(t, got, "goal")
	assert.NotContains(t, got, "depends on")
	assert.NotContains(t, got, "forbidden paths")
	assert.Contains(t, got, "validation, the only check you run")
	assert.Contains(t, got, row.Validation)
}

// The bootstrap is commands and their reasons, and nothing else. A rules block here was
// read once and ignored; the guard says the rules when a command meets one.
func TestBriefBootstrapIsCommands(t *testing.T) {
	t.Parallel()

	b := NewBrief(briefRow())
	require.Len(t, b.Bootstrap, 3)
	for _, step := range b.Bootstrap {
		assert.NotEmpty(t, step.Run)
		assert.NotEmpty(t, step.Why)
	}
	assert.Equal(t, "magus session lease harness/ledger-per-repo", b.Bootstrap[1].Run)
	assert.Contains(t, b.Text(), "magus vcs checkpoint -o name")

	for _, gone := range []string{"skills to load", "Do not commit", "Never `magus affected ci`"} {
		assert.NotContains(t, b.Text(), gone, "the record carries no instruction blocks")
	}
}
