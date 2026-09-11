package main

import (
	"strings"
	"testing"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func leaseRow(id, parent string) types.Job {
	return types.Job{ID: id, Parent: parent, State: types.StateRunning, Model: "standard"}
}

func TestLedgerTreeOrderNestsChildrenUnderTheirParent(t *testing.T) {
	t.Parallel()

	got := jobTreeOrder([]types.Job{
		leaseRow("plan", ""),
		leaseRow("plan/core", "plan"),
		leaseRow("other", ""),
		leaseRow("plan/core/deep", "plan/core"),
	})

	assert.Equal(t, []jobTreeLine{
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

	rows := []types.Job{
		leaseRow("orphan", "cleared-parent"),
		leaseRow("a", "b"),
		leaseRow("b", "a"),
	}
	got := jobTreeOrder(rows)

	require.Len(t, got, len(rows))
	for _, r := range got {
		assert.Zero(t, r.depth)
	}
}

func TestPrintLedgerTreeRendersOverlaps(t *testing.T) {
	t.Parallel()

	parent := leaseRow("plan", "")
	parent.WritePaths = []string{"internal/ledger"}
	parent.Validation = "magus run test internal/ledger"
	child := leaseRow("plan/cli", "plan")
	child.WritePaths = []string{"internal/ledger/store.go"}

	var out strings.Builder
	printJobTree(&out, types.NewJobList([]types.Job{parent, child}))
	got := out.String()

	assert.Contains(t, got, "JOB")
	assert.Contains(t, got, "\n  plan/cli", "a child is indented under its parent")
	assert.Contains(t, got, "magus run test internal/ledger")
	assert.Contains(t, got, "overlaps")
	assert.Contains(t, got, "plan and plan/cli claim common ground")
}

func TestPrintLedgerTreeSaysWhereAnEmptyPlanComesFrom(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	printJobTree(&out, types.NewJobList(nil))
	assert.Contains(t, out.String(), "magus_job")
}

// Explain resolves a bare name fuzzily, which is right for a person typing
// `magus explain build` and wrong for evidence: asked for "cmd/magus" it once answered
// target:.:release-sign, and a blast radius from an unrelated node is worse than silence.
func TestLeaseGraphEvidenceTakesOnlyAnExactNode(t *testing.T) {
	t.Parallel()

	g := knowledge.NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "dir:cmd/magus", Kind: types.KindDir, Label: "cmd/magus"})
	g.AddNode(types.KnowledgeNode{ID: "file:internal/ledger/brief.go", Kind: types.KindFile, Label: "brief.go"})

	got, ok := pathEvidence(g, "cmd/magus")
	require.True(t, ok)
	assert.Equal(t, job.TermsEvidence{Path: "cmd/magus", Node: "dir:cmd/magus"}, got)
	// "brief.go" resolves to file:internal/ledger/brief.go by name. It is a match and not
	// evidence: the lease declared a path at the workspace root, and this node is not it.
	_, ok = pathEvidence(g, "brief.go")
	assert.False(t, ok, "a fuzzy match is not evidence")
}

// The gate reached indirectly buys the same seven concurrent pipelines as the gate named
// outright, and it is the likelier mistake once the obvious spelling is refused.
func TestChainToGateFollowsComposites(t *testing.T) {
	t.Parallel()

	deps := map[string][]string{
		"preflight": {"format", "verify"},
		"verify":    {"ci"},
		"ci":        {"test", "lint"},
		"test":      nil,
		"format":    nil,
	}

	assert.Equal(t, []string{"preflight", "verify", "ci"}, chainToGate("preflight", deps))
	assert.Nil(t, chainToGate("test", deps))
	// Every bare word of a validation field reaches here, project paths included.
	assert.Nil(t, chainToGate("internal/ledger", deps))
}

// `magus describe graph` reports a cycle rather than rejecting it, so one reaches this
// walk. It has to terminate on its own rather than trust the graph to be acyclic.
func TestChainToGateTerminatesOnACycle(t *testing.T) {
	t.Parallel()

	assert.Nil(t, chainToGate("a", map[string][]string{"a": {"b"}, "b": {"a"}}))
}

// The person's two spellings are one declaration: a row typed as flags and the same row
// piped in as a record reach the store identically, or the CLI has quietly grown a
// second vocabulary.
func TestRegisterFromFlagsAndFromStdinAgree(t *testing.T) {
	t.Parallel()

	flags := forkFlags{
		goal:       "the store is the enforcement point",
		parent:     "adjacency",
		checkpoint: "cf5509d09",
		writePaths: pathList{"internal/ledger", "types/lease.go"},
		denyPaths:  pathList{"MAGUS.md"},
		readPaths:  pathList{"internal/trail"},
		dependsOn:  pathList{"adj/guard"},
		check:      "test internal/ledger",
		model:      "principal",
	}
	piped, err := job.DecodeDeclaration(strings.NewReader(`{
	  "schema_version": 3,
	  "id": "adj/store",
	  "parent": "adjacency",
	  "goal": "the store is the enforcement point",
	  "checkpoint": "cf5509d09",
	  "write_paths": ["internal/ledger", "types/lease.go"],
	  "deny_paths": ["MAGUS.md"],
	  "read_paths": ["internal/trail"],
	  "depends_on": ["adj/guard"],
	  "validation": "magus run test internal/ledger",
	  "model": "principal",
	  "state": "declared"
	}`))
	require.NoError(t, err)

	fromFlags := flags.row("adj/store")
	require.NoError(t, fromFlags.Validate())

	var a, b types.Job
	fromFlags.Apply(&a)
	piped.Apply(&b)
	assert.Equal(t, a, b)
}

// A path flag takes both spellings, and refuses the empty segment a trailing comma
// leaves: a lane that silently shrank is the failure --skip already refuses.
func TestRegisterPathFlagsTakeRepeatsAndCommas(t *testing.T) {
	t.Parallel()

	var repeated, combined pathList
	require.NoError(t, repeated.Set("internal/ledger"))
	require.NoError(t, repeated.Set("types/lease.go"))
	require.NoError(t, combined.Set("internal/ledger, types/lease.go"))
	assert.Equal(t, repeated, combined)

	var refused pathList
	require.Error(t, refused.Set("internal/ledger,"))
	require.Error(t, refused.Set(""))
}
