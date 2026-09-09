package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func leaseRow(id, parent string) types.Lease {
	return types.Lease{ID: id, Parent: parent, State: types.StateRunning, Tier: "standard"}
}

func TestLedgerTreeOrderNestsChildrenUnderTheirParent(t *testing.T) {
	t.Parallel()

	got := ledgerTreeOrder([]types.Lease{
		leaseRow("plan", ""),
		leaseRow("plan/core", "plan"),
		leaseRow("other", ""),
		leaseRow("plan/core/deep", "plan/core"),
	})

	assert.Equal(t, []ledgerRow{
		{lease: leaseRow("plan", ""), depth: 0},
		{lease: leaseRow("plan/core", "plan"), depth: 1},
		{lease: leaseRow("plan/core/deep", "plan/core"), depth: 2},
		{lease: leaseRow("other", ""), depth: 0},
	}, got)
}

// Every row reaches the reader. A parent that was cleared, and a cycle, both cost their
// rows the indentation and nothing else.
func TestLedgerTreeOrderKeepsUnrootedRows(t *testing.T) {
	t.Parallel()

	rows := []types.Lease{
		leaseRow("orphan", "cleared-parent"),
		leaseRow("a", "b"),
		leaseRow("b", "a"),
	}
	got := ledgerTreeOrder(rows)

	require.Len(t, got, len(rows))
	for _, r := range got {
		assert.Zero(t, r.depth)
	}
}

func TestPrintLedgerTreeRendersOverlaps(t *testing.T) {
	t.Parallel()

	parent := leaseRow("plan", "")
	parent.OwnedPaths = []string{"internal/ledger"}
	parent.Validation = "magus run test internal/ledger"
	child := leaseRow("plan/cli", "plan")
	child.OwnedPaths = []string{"internal/ledger/store.go"}

	var out strings.Builder
	printLedgerTree(&out, types.NewLeaseReport([]types.Lease{parent, child}))
	got := out.String()

	assert.Contains(t, got, "LEASE")
	assert.Contains(t, got, "\n  plan/cli", "a child is indented under its parent")
	assert.Contains(t, got, "magus run test internal/ledger")
	assert.Contains(t, got, "overlaps")
	assert.Contains(t, got, "plan and plan/cli claim common ground")
}

func TestPrintLedgerTreeSaysWhereAnEmptyPlanComesFrom(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	printLedgerTree(&out, types.NewLeaseReport(nil))
	assert.Contains(t, out.String(), "magus_ledger")
}

// Explain resolves a bare name fuzzily, which is right for a person typing
// `magus explain build` and wrong for evidence: asked for "cmd/magus" it once answered
// target:.:release-sign, and a blast radius from an unrelated node is worse than silence.
func TestLeaseGraphEvidenceTakesOnlyAnExactNode(t *testing.T) {
	t.Parallel()

	g := knowledge.NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "dir:cmd/magus", Kind: types.KindDir, Label: "cmd/magus"})
	g.AddNode(types.KnowledgeNode{ID: "file:internal/ledger/brief.go", Kind: types.KindFile, Label: "brief.go"})

	assert.Equal(t, []ledger.BriefEvidence{{Path: "cmd/magus", Node: "dir:cmd/magus"}},
		pathEvidence(g, "cmd/magus"))
	// "brief.go" resolves to file:internal/ledger/brief.go by name. It is a match and not
	// evidence: the lease declared a path at the workspace root, and this node is not it.
	assert.Empty(t, pathEvidence(g, "brief.go"), "a fuzzy match is not evidence")
}

// The footer is the workspace's, including owning none: a workspace with no template
// gets a brief without the fixed blocks rather than an error.
func TestLeaseBriefFooterIsOptional(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	got, err := leaseBriefFooter(root, types.Lease{ID: "u1"})
	require.NoError(t, err)
	assert.Empty(t, got)

	path := filepath.Join(root, filepath.FromSlash(ledger.BriefTemplatePath))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("bind {{.ID}}\n"), 0o644))

	got, err = leaseBriefFooter(root, types.Lease{ID: "u1"})
	require.NoError(t, err)
	assert.Equal(t, "bind u1\n", got)
}

// This repository ships the template the guide documents, so a brief rendered here always
// carries the fixed blocks.
func TestMagusShipsABriefTemplate(t *testing.T) {
	t.Parallel()

	got, err := leaseBriefFooter(filepath.Join("..", ".."), types.Lease{ID: "u1"})
	require.NoError(t, err)
	assert.Contains(t, got, "skills to load")
}
