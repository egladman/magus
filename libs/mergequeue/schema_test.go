package mergequeue

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue/types"
)

func TestAClaimIsBoundedAndCutAtARuneBoundary(t *testing.T) {
	assert.Equal(t, "short", claim("short"))
	got := claim(strings.Repeat("\xc3\xa9", types.MaxClaim))
	assert.LessOrEqual(t, len(got), types.MaxClaim)
	assert.True(t, utf8.ValidString(got), "no rune is split")
	assert.True(t, strings.HasSuffix(got, "\n[cut]"), got[len(got)-10:])
}

// A kick read from a plan or a verdict says what the queue checked; the verdict's own
// words, which a job running the change's code could have written, never become markup.
func TestAReadKickIsWordedByTheQueue(t *testing.T) {
	c := change("1", "a")
	hostile := "@team [click](https://evil.example) <!-- `"
	for name, tc := range map[string]struct {
		v    types.Verdict
		want types.Kick
	}{
		"a conflict": {
			v:    types.Verdict{Change: c, Code: types.CodeKickConflict, Reason: hostile, Report: hostile},
			want: types.Kick{Code: types.CodeKickConflict, Report: conflictReport("main", c.Head)},
		},
		"a fork": {
			v:    types.Verdict{Change: types.Change{ID: "1", Head: c.Head, Fork: true}, Code: types.CodeKickRefused, Reason: hostile, Report: hostile},
			want: types.Kick{Code: types.CodeKickRefused, Report: forkReport},
		},
		"a red gate": {
			v: types.Verdict{Change: c, Code: types.CodeKickRed, Onto: head("o"), After: "7", Reason: hostile, Report: hostile},
			want: types.Kick{Code: types.CodeKickRed, Claim: hostile,
				Report: "The merge queue built this change at `" + c.Head[:12] + "` onto the candidate of #7 (`" + head("o")[:12] + "`), and the gate failed on it and passed without it.\n"},
		},
		"a red gate naming nothing it was built onto": {
			v:    types.Verdict{Change: c, Code: types.CodeKickRed, Reason: hostile, Report: hostile},
			want: types.Kick{Code: types.CodeKickRed, Claim: hostile, Report: "The merge queue cannot merge this change at `" + c.Head[:12] + "`.\n"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, kickOf("main", tc.v))
		})
	}
}

func TestPlanRoundTrips(t *testing.T) {
	p := types.Plan{Schema: types.SchemaPlan, Base: "main", BaseCommit: base, Depth: 3,
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
	require.NoError(t, WritePlan(&buf, types.Plan{Base: "main", BaseCommit: base, Depth: 1}))
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
