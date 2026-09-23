package mergequeue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decided(p Plan) map[string]Decided {
	out := map[string]Decided{}
	for _, d := range p.Decided {
		out[d.Change.ID] = d
	}
	return out
}

func TestPlanHoldsTheUnapprovedAndKicksBackForksAndBaseConflicts(t *testing.T) {
	st := newStager(map[string][]string{"1": {"app/a"}, "2": {"lib/b"}, "3": {"web/c"}, "4": {"api/d"}, "5": {"x/e"}})
	st.conflicts[[2]string{"base", "2"}] = []string{"lib/b"}
	host := &readHost{approval: func(c Change) Approval {
		switch c.ID {
		case "1":
			return Approval{Head: c.Head, Required: 1, Reason: "0 of 1 approvals at this commit"}
		case "3":
			return Approval{Approved: true, Head: "sha-new"}
		}
		return Approval{Approved: true, Head: c.Head}
	}}
	fork := change("5", "x")
	fork.Fork = true
	in := Changes{Schema: SchemaChanges, Base: "main", Changes: []Change{
		change("1", "app"), change("2", "lib"), change("3", "web"), change("4", "api"), fork,
	}}
	p, err := (&Planner{Provider: host, Stager: st}).Run(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, "base", p.BaseSHA)
	assert.Equal(t, 1, p.Depth, "depth below 1 means 1")
	assert.Equal(t, [][]string{{"4"}}, ids(p.Partitions))
	d := decided(p)
	assert.Equal(t, Decided{Change: in.Changes[0], Decision: DecisionWait, Reason: "not approved at sha-1: 0 of 1 approvals at this commit"}, d["1"])
	assert.Equal(t, DecisionKick, d["2"].Decision)
	assert.Contains(t, d["2"].Report, "`lib/b`")
	assert.Contains(t, d["2"].Report, "abc123 an earlier change")
	assert.Equal(t, "sha-new", d["3"].Change.Head, "the wait is reported on the new head")
	assert.Equal(t, DecisionKick, d["5"].Decision)
	assert.Equal(t, forkReport, d["5"].Report)
	assert.NotContains(t, host.calls, "approval 5", "a fork is refused before anyone is asked about it")
}

func TestPlanAsksTheAffectedHookOnlyForChangesWithoutASet(t *testing.T) {
	st := newStager(map[string][]string{"1": {"app/a.go"}, "2": {"lib/b.go"}, "3": {"magusfile.buzz"}})
	var (
		mu    sync.Mutex
		asked []string
	)
	affected := func(_ context.Context, c Change, paths []string) ([]string, string, error) {
		mu.Lock()
		asked = append(asked, c.ID+":"+strings.Join(paths, ","))
		mu.Unlock()
		if c.ID == "3" {
			return []string{"."}, "magusfile.buzz changes the declarations", nil
		}
		return []string{strings.Split(paths[0], "/")[0]}, "", nil
	}
	in := Changes{Schema: SchemaChanges, Base: "main", Changes: []Change{
		{ID: "1", Head: "sha-1"}, change("2", "lib"), {ID: "3", Head: "sha-3"},
	}}
	var events bytes.Buffer
	p, err := (&Planner{Stager: st, Affected: affected, Depth: 2, Parallel: 4, Events: NewEvents(&events)}).Run(context.Background(), in)
	require.NoError(t, err)
	slices.Sort(asked)
	assert.Equal(t, []string{"1:app/a.go", "3:magusfile.buzz"}, asked)
	assert.Equal(t, [][]string{{"1", "2", "3"}}, ids(p.Partitions), "an unbounded change overlaps everything")
	assert.Equal(t, []string{"app"}, p.Partitions[0][0].Affected)

	var ev Event
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(events.Bytes()), &ev))
	assert.Equal(t, SchemaEvent, ev.Schema)
	assert.Equal(t, EventPartition, ev.Event)
	assert.Equal(t, []string{"1", "2", "3"}, ev.Changes)
}

func TestPlanStopsWhenTheAffectedHookFails(t *testing.T) {
	st := newStager(map[string][]string{"1": {"app/a.go"}})
	broken := func(context.Context, Change, []string) ([]string, string, error) {
		return nil, "", errors.New("exit status 2")
	}
	_, err := (&Planner{Stager: st, Affected: broken}).Run(context.Background(),
		Changes{Schema: SchemaChanges, Base: "main", Changes: []Change{{ID: "1", Head: "sha-1"}}})
	require.EqualError(t, err, "mergequeue: affected set of #1: exit status 2", "misconfiguration is an error, never an unbounded guess")
}

func TestPlanRoundTripsThroughItsFile(t *testing.T) {
	file := t.TempDir() + "/plan.json"
	p := Plan{Schema: SchemaPlan, Base: "main", BaseSHA: "abc", Depth: 3,
		Partitions: [][]Change{{change("1", "a")}},
		Decided:    []Decided{{Change: change("2"), Decision: DecisionWait, Reason: "r"}}}
	require.NoError(t, WriteJSON(file, p))
	got, err := ReadPlan(file)
	require.NoError(t, err)
	assert.Equal(t, p, got)
}

func TestReadChangesRefusesAnotherSchemaAndAChangeWithoutAHead(t *testing.T) {
	_, err := ReadChanges(strings.NewReader(`{"schema": "mergequeue.changes/v2", "base": "main"}`))
	require.ErrorContains(t, err, `want "mergequeue.changes/v1"`)
	_, err = ReadChanges(strings.NewReader(`{"schema": "mergequeue.changes/v1", "base": "main", "changes": [{"id": "1"}]}`))
	require.ErrorContains(t, err, "changes[0] needs both id and head")
	got, err := ReadChanges(strings.NewReader(`{"schema": "mergequeue.changes/v1", "base": "main",
		"changes": [{"id": "1", "head": "h", "affected": ["a"]}, {"id": "2", "head": "h2", "affected": null}]}`))
	require.NoError(t, err)
	assert.True(t, got.Changes[0].Proven())
	assert.False(t, got.Changes[1].Proven(), "null is unknown, not empty")
}
