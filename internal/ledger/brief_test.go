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
// golden IS the contract. It renders against the workspace's own shipped template, which
// gates that file too: change the blocks and this fails until the golden is refreshed.
func TestBriefRendersTheGolden(t *testing.T) {
	t.Parallel()

	tmpl, err := os.ReadFile(filepath.Join("..", "..", BriefTemplatePath))
	require.NoError(t, err)
	footer, err := RenderBriefFooter(string(tmpl), briefRow())
	require.NoError(t, err)

	b := NewBrief(briefRow())
	b.Evidence = []BriefEvidence{
		{Path: "internal/ledger", Node: "dir:internal/ledger", BlastRadius: 4},
	}
	b.Footer = footer
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

func TestRenderBriefFooterReportsABrokenTemplate(t *testing.T) {
	t.Parallel()

	_, err := RenderBriefFooter("{{.Nope}}", briefRow())
	require.Error(t, err)
	assert.Contains(t, err.Error(), BriefTemplatePath, "the reader who fixes this is editing that file")
}
