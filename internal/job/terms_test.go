package job

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func briefRow() types.Job {
	return types.Job{
		ID:     "harness/ledger-per-repo",
		Parent: "harness",
		Criteria: "Move the lease ledger to the per-repository state dir.\n" +
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
func TestTermsRendersTheGolden(t *testing.T) {
	t.Parallel()

	b := NewTerms(briefRow(), TermsFacts{
		Evidence: []TermsEvidence{{Path: "internal/ledger", Node: "dir:internal/ledger", BlastRadius: 4}},
	})
	want, err := os.ReadFile(filepath.Join("testdata", "brief.golden"))
	require.NoError(t, err)
	assert.Equal(t, string(want), b.String())
}

// A cold graph says so once. Silence would read as "nothing depends on any of this",
// which is the opposite of what magus knows.
func TestTermsNamesAColdGraphOnce(t *testing.T) {
	t.Parallel()

	b := NewTerms(briefRow(), TermsFacts{GraphCold: true})
	assert.Equal(t, 1, strings.Count(b.String(), "the knowledge graph is cold"))
}

// A workspace that would not load says so too, for the same reason: a worker in a tree
// whose magusfile is mid-edit still needs the row it was handed.
func TestTermsNamesAWorkspaceThatWouldNotLoad(t *testing.T) {
	t.Parallel()

	b := NewTerms(briefRow(), TermsFacts{WorkspaceCold: true})
	assert.Contains(t, b.String(), "this workspace would not load")
	assert.Contains(t, b.String(), briefRow().Criteria, "the row is rendered whatever the workspace does")
}

// The unrun gates are a section of the terms, after the regenerated outputs, and a row with
// none renders as it did before the section existed.
func TestTermsRendersUnrunGatesAsASection(t *testing.T) {
	t.Parallel()

	bare := NewTerms(briefRow(), TermsFacts{}).String()
	assert.NotContains(t, bare, unrunHeading)

	gate := UnrunGate{Target: "lint", Project: ".", Source: "**/*.go", Goal: unrunGoal(".", "lint")}
	got := NewTerms(briefRow(), TermsFacts{UnrunGates: []UnrunGate{gate}}).String()
	assert.Contains(t, got, "\n"+unrunHeading+"\n  lint . (reads **/*.go)\n  goals to add: ")
	assert.Less(t, strings.Index(got, unrunHeading), strings.Index(got, "the only check you run"),
		"the section sits among the facts, ahead of the check")
}

// Nothing outside the fixed order reaches the worker, which is how the gate stops leaking
// into a brief: `ci` is not rendered because nothing renders it.
func TestTermsRendersOnlyTheRow(t *testing.T) {
	t.Parallel()

	row := briefRow()
	row.Criteria, row.DependsOn, row.DenyPaths = "", nil, nil
	got := NewTerms(row, TermsFacts{}).String()

	assert.NotContains(t, got, "goal")
	assert.NotContains(t, got, "depends on")
	assert.NotContains(t, got, "deny paths")
	assert.Contains(t, got, "the only check you run")
	assert.Contains(t, got, row.Validation)
}
