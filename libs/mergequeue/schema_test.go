package mergequeue

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue/types"
)

func TestPlanRoundTrips(t *testing.T) {
	p := types.Plan{Schema: types.SchemaPlan, Base: "main", BaseCommit: base, CommitDate: when, Depth: 3,
		Partitions: [][]types.Change{{change("1", "a")}},
		Verdicts:   []types.Verdict{waiting("2"), {Change: change("3"), Decision: types.DecisionMerged, Reason: "its head is already on main"}},
		Merged:     []types.MergedChange{{ID: "4", Head: head("4"), Commit: head("m4"), Method: types.MethodSquash}},
		Unqueued:   []types.UnqueuedChange{{ID: "5", Head: head("5")}}}
	var buf bytes.Buffer
	require.NoError(t, WritePlan(&buf, p))
	got, err := ReadPlan(&buf)
	require.NoError(t, err)
	assert.Equal(t, p, got)
}

func TestAnEmptyPlanWritesItsPartitionsAsAList(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, WritePlan(&buf, types.Plan{Base: "main", BaseCommit: base, CommitDate: when, Depth: 1}))
	assert.Contains(t, buf.String(), `"partitions": []`)
}

func TestVerdictRoundTripsWithItsSchemaStamped(t *testing.T) {
	v := green("1")
	var buf bytes.Buffer
	require.NoError(t, writeVerdict(&buf, v))
	got, err := readVerdict(&buf)
	require.NoError(t, err)
	v.Schema = types.SchemaVerdict
	assert.Equal(t, v, got)
}

func TestReadChangesRefusesAnotherSchemaAndAChangeWithoutAHead(t *testing.T) {
	_, err := ReadChanges(strings.NewReader(`{"schema": "mergequeue.changes/v2", "base": "main"}`))
	require.ErrorContains(t, err, `want "mergequeue.changes/v1"`)
	_, err = ReadChanges(strings.NewReader(`{"schema": "mergequeue.changes/v1", "base": "main", "changes": [{"id": "1"}]}`))
	require.ErrorContains(t, err, `changes[0]: #1: head "" is not a full commit id`)
	got, err := ReadChanges(strings.NewReader(`{"schema": "mergequeue.changes/v1", "base": "main",
		"changes": [{"id": "1", "head": "` + head("1") + `", "base": "main", "method": "squash", "affected": ["a"]},
		            {"id": "2", "head": "` + head("2") + `", "base": "main", "method": "merge", "affected": null}]}`))
	require.NoError(t, err)
	assert.True(t, proven(got.Changes[0]))
	assert.False(t, proven(got.Changes[1]), "null is unknown, not empty")
	_, err = ReadChanges(strings.NewReader(`{"schema": "mergequeue.changes/v1", "base": "main", "changes": [{"id": "1", "head": "` + head("1") + `", "base": "main"}]}`))
	require.ErrorContains(t, err, `#1: merge method "", want merge, squash or rebase`, "a provider default the queue cannot see is no method")
	_, err = ReadChanges(strings.NewReader(`{not json`))
	require.ErrorContains(t, err, "decode mergequeue.changes/v1")
}

// What a step writes is checked as it is written, where the step can still say so.
func TestWritersRefuseWhatReadersWould(t *testing.T) {
	twin := change("2", "b")
	twin.Head = head("1")
	err := WriteChanges(&bytes.Buffer{}, types.Changes{Base: "main", Changes: []types.Change{change("1", "a"), twin}})
	require.EqualError(t, err, "mergequeue.changes/v1: changes[1]: #1 and #2 share head "+head("1"))
	require.ErrorContains(t, WritePlan(&bytes.Buffer{}, types.Plan{Base: "main", BaseCommit: base}), "mergequeue.plan/v1: depth 0 is below 1")
	noCandidate := green("1")
	noCandidate.CandidateCommit = ""
	require.ErrorContains(t, writeVerdict(&bytes.Buffer{}, noCandidate), "mergequeue.verdict/v1: merge verdict on #1 names no base commit, onto or candidate commit")
}

func TestStackRefsNameEveryChangeAPlannedOneMayCarry(t *testing.T) {
	p := types.Plan{
		Partitions: [][]types.Change{{change("1")}},
		Verdicts:   []types.Verdict{waiting("2"), {Change: change("3"), Decision: types.DecisionMerged}},
		Merged:     []types.MergedChange{{ID: "4", Head: head("4")}},
		Unqueued:   []types.UnqueuedChange{{ID: "5", Head: head("5")}},
	}
	assert.Equal(t, []stackRef{{id: "4", head: head("4")}, {id: "1", head: head("1")}, {id: "2", head: head("2")}, {id: "5", head: head("5"), unqueued: true}},
		stackRefs(p), "a change whose head is on the base is no ref")
}
