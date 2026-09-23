package mergequeue

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanRoundTrips(t *testing.T) {
	p := Plan{Schema: SchemaPlan, Base: "main", BaseCommit: base, Depth: 3,
		Partitions: [][]Change{{change("1", "a")}},
		Verdicts:   []Verdict{{Change: change("2"), Decision: DecisionWait, Reason: "r"}}}
	var buf bytes.Buffer
	require.NoError(t, WritePlan(&buf, p))
	got, err := ReadPlan(&buf)
	require.NoError(t, err)
	assert.Equal(t, p, got)
}

func TestVerdictRoundTripsWithItsSchemaStamped(t *testing.T) {
	v := Verdict{BaseCommit: base, Change: change("1", "a"), Decision: DecisionLand, Onto: base, Stage: "s", Depth: 1}
	var buf bytes.Buffer
	require.NoError(t, WriteVerdict(&buf, v))
	got, err := ReadVerdict(&buf)
	require.NoError(t, err)
	v.Schema = SchemaVerdict
	assert.Equal(t, v, got)
}

func TestReadChangesRefusesAnotherSchemaAndAChangeWithoutAHead(t *testing.T) {
	_, err := ReadChanges(strings.NewReader(`{"schema": "mergequeue.changes/v2", "base": "main"}`))
	require.ErrorContains(t, err, `want "mergequeue.changes/v1"`)
	_, err = ReadChanges(strings.NewReader(`{"schema": "mergequeue.changes/v1", "base": "main", "changes": [{"id": "1"}]}`))
	require.ErrorContains(t, err, `changes[0]: #1: head "" is not a full commit id`)
	got, err := ReadChanges(strings.NewReader(`{"schema": "mergequeue.changes/v1", "base": "main",
		"changes": [{"id": "1", "head": "` + head("1") + `", "affected": ["a"]}, {"id": "2", "head": "` + head("2") + `", "affected": null}]}`))
	require.NoError(t, err)
	assert.True(t, got.Changes[0].Proven())
	assert.False(t, got.Changes[1].Proven(), "null is unknown, not empty")
}

// An id names a directory the verdicts land in, so ".." once made the stages
// directory's parent the target of a RemoveAll.
func TestReadChangesRefusesIDsThatEscapeTheirDirectory(t *testing.T) {
	for _, id := range []string{"..", ".", "a/b", `a\b`, ".hidden", "-x", ""} {
		_, err := ReadChanges(strings.NewReader(`{"schema": "mergequeue.changes/v1", "base": "main", "changes": [{"id": "` +
			strings.ReplaceAll(id, `\`, `\\`) + `", "head": "` + head("1") + `"}]}`))
		require.ErrorContains(t, err, "change id", id)
	}
}

func TestReadPlanRefusesADuplicateAndAPlanningLand(t *testing.T) {
	dup := Plan{Base: "main", BaseCommit: base, Depth: 1, Partitions: [][]Change{{change("1")}, {change("1")}}}
	var buf bytes.Buffer
	require.NoError(t, WritePlan(&buf, dup))
	_, err := ReadPlan(&buf)
	require.ErrorContains(t, err, `change "1" appears twice`)

	land := Plan{Base: "main", BaseCommit: base, Depth: 1, Verdicts: []Verdict{{Change: change("1"), Decision: DecisionLand}}}
	buf.Reset()
	require.NoError(t, WritePlan(&buf, land))
	_, err = ReadPlan(&buf)
	require.ErrorContains(t, err, `planning decides "land"`)
}

func TestCheckBranchFollowsGitsRules(t *testing.T) {
	for _, ok := range []string{"main", "feat/x", "release-1.2"} {
		assert.NoError(t, CheckBranch(ok), ok)
	}
	for _, bad := range []string{"", "-x", "a..b", "a:b", "a b", "a/", "x.lock", "refs/heads/main", "a/.b", "@"} {
		assert.Error(t, CheckBranch(bad), bad)
	}
}
