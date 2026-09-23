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
		Verdicts:   []Verdict{waiting("2")}}
	var buf bytes.Buffer
	require.NoError(t, WritePlan(&buf, p))
	got, err := ReadPlan(&buf)
	require.NoError(t, err)
	assert.Equal(t, p, got)
}

func TestVerdictRoundTripsWithItsSchemaStamped(t *testing.T) {
	v := Verdict{BaseCommit: base, Change: change("1", "a"), Decision: DecisionMerge, Onto: base, Candidate: "s", Method: MethodSquash, Depth: 1}
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
		"changes": [{"id": "1", "head": "` + head("1") + `", "method": "squash", "affected": ["a"]},
		            {"id": "2", "head": "` + head("2") + `", "method": "merge", "affected": null}]}`))
	require.NoError(t, err)
	assert.True(t, got.Changes[0].Proven())
	assert.False(t, got.Changes[1].Proven(), "null is unknown, not empty")
	_, err = ReadChanges(strings.NewReader(`{"schema": "mergequeue.changes/v1", "base": "main", "changes": [{"id": "1", "head": "` + head("1") + `"}]}`))
	require.ErrorContains(t, err, `#1: merge method ""; want merge, squash or rebase`, "a provider default the queue cannot see is no method")
}

// A code is how a provider and a workflow act on a verdict, so one that contradicts the
// decision it rides on is refused rather than acted on.
func TestReadVerdictRefusesACodeThatContradictsItsDecision(t *testing.T) {
	for _, v := range []Verdict{
		{Change: change("1"), Decision: DecisionKick, Code: CodeBehind},
		{Change: change("1"), Decision: DecisionWait, Code: CodeRed},
		{Change: change("1"), Decision: DecisionWait},
		{Change: change("1"), Decision: DecisionMerge, Code: CodeRed},
	} {
		var buf bytes.Buffer
		require.NoError(t, WriteVerdict(&buf, v))
		_, err := ReadVerdict(&buf)
		require.ErrorContains(t, err, "carries code", "%s %s", v.Decision, v.Code)
	}
}

func TestReadPlanRefusesAChangeAheadOfWhatItIsStackedOn(t *testing.T) {
	child := change("2", "a")
	child.Below = "1"
	for _, p := range []Plan{
		{Base: "main", BaseCommit: base, Depth: 1, Partitions: [][]Change{{child, change("1", "a")}}},
		{Base: "main", BaseCommit: base, Depth: 1, Partitions: [][]Change{{change("1", "a")}, {child}}},
	} {
		var buf bytes.Buffer
		require.NoError(t, WritePlan(&buf, p))
		_, err := ReadPlan(&buf)
		require.ErrorContains(t, err, "is stacked on #1, which is not ahead of it in its partition")
	}
}

// An id names a directory the verdicts are written to, so ".." would make the
// directory's parent the target of a RemoveAll.
func TestReadChangesRefusesIDsThatEscapeTheirDirectory(t *testing.T) {
	for _, id := range []string{"..", ".", "a/b", `a\b`, ".hidden", "-x", ""} {
		_, err := ReadChanges(strings.NewReader(`{"schema": "mergequeue.changes/v1", "base": "main", "changes": [{"id": "` +
			strings.ReplaceAll(id, `\`, `\\`) + `", "head": "` + head("1") + `"}]}`))
		require.ErrorContains(t, err, "change id", id)
	}
}

func TestReadPlanRefusesADuplicateAndAPlanningMerge(t *testing.T) {
	dup := Plan{Base: "main", BaseCommit: base, Depth: 1, Partitions: [][]Change{{change("1")}, {change("1")}}}
	var buf bytes.Buffer
	require.NoError(t, WritePlan(&buf, dup))
	_, err := ReadPlan(&buf)
	require.ErrorContains(t, err, `change "1" appears twice`)

	merge := Plan{Base: "main", BaseCommit: base, Depth: 1, Verdicts: []Verdict{{Change: change("1"), Decision: DecisionMerge}}}
	buf.Reset()
	require.NoError(t, WritePlan(&buf, merge))
	_, err = ReadPlan(&buf)
	require.ErrorContains(t, err, `planning decides "merge"`)
}

func TestCheckBranchFollowsGitsRules(t *testing.T) {
	for _, ok := range []string{"main", "feat/x", "release-1.2"} {
		assert.NoError(t, CheckBranch(ok), ok)
	}
	for _, bad := range []string{"", "-x", "a..b", "a:b", "a b", "a/", "x.lock", "refs/heads/main", "a/.b", "@"} {
		assert.Error(t, CheckBranch(bad), bad)
	}
}
