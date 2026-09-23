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

func verdicts(p Plan) map[string]Verdict {
	out := map[string]Verdict{}
	for _, v := range p.Verdicts {
		out[v.Change.ID] = v
	}
	return out
}

func TestPlanHoldsTheUnapprovedAndKicksBackForksAndBaseConflicts(t *testing.T) {
	st := newStager(map[string][]string{"1": {"app/a"}, "2": {"lib/b"}, "3": {"web/c"}, "4": {"api/d"}, "5": {"x/e"}})
	st.conflicts[[2]string{"base", "2"}] = []string{"lib/b"}
	moved := head("new")
	host := &readHost{approval: func(c Change) Approval {
		switch c.ID {
		case "1":
			return Approval{Head: c.Head, Reason: "0 of 1 approvals at this commit"}
		case "3":
			return Approval{Approved: true, Head: moved}
		}
		return Approval{Approved: true, Head: c.Head}
	}}
	fork := change("5", "x")
	fork.Fork = true
	in := Changes{Schema: SchemaChanges, Base: "main", Changes: []Change{
		change("1", "app"), change("2", "lib"), change("3", "web"), change("4", "api"), fork,
	}}
	pl := NewPlanner(st)
	pl.Provider, pl.Parallel = host, 4
	p, err := pl.Run(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, base, p.BaseCommit)
	assert.Equal(t, 1, p.Depth, "depth 0 means 1")
	assert.Equal(t, [][]string{{"4"}}, ids(p.Partitions))
	assert.Equal(t, []string{"1", "2", "3", "5"}, idsOf(p.Verdicts), "verdicts keep queue order though admission runs side by side")
	v := verdicts(p)
	assert.Equal(t, Verdict{Change: in.Changes[0], Decision: DecisionWait, Reason: "not approved at " + short(head("1")) + ": 0 of 1 approvals at this commit"}, v["1"])
	assert.Equal(t, DecisionKick, v["2"].Decision)
	assert.Contains(t, v["2"].Report, "`lib/b`")
	assert.Contains(t, v["2"].Report, "abc123 an earlier change")
	assert.Equal(t, moved, v["3"].Change.Head, "the wait is reported on the new head")
	assert.Equal(t, DecisionKick, v["5"].Decision)
	assert.Equal(t, forkReport, v["5"].Report)
	assert.NotContains(t, host.calls, "approval 5", "a fork is refused before anyone is asked about it")
}

func idsOf(vs []Verdict) []string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Change.ID)
	}
	return out
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
		{ID: "1", Head: head("1")}, change("2", "lib"), {ID: "3", Head: head("3")},
	}}
	var events bytes.Buffer
	pl := NewPlanner(st)
	pl.Affected, pl.Depth, pl.Parallel, pl.Events = affected, 2, 4, NewEvents(&events)
	p, err := pl.Run(context.Background(), in)
	require.NoError(t, err)
	slices.Sort(asked)
	assert.Equal(t, []string{"1:app/a.go", "3:magusfile.buzz"}, asked)
	assert.Equal(t, [][]string{{"1", "2", "3"}}, ids(p.Partitions), "an unbounded change overlaps everything")
	assert.Equal(t, []string{"app"}, p.Partitions[0][0].Affected)

	var ev Event
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(events.Bytes()), &ev))
	assert.Equal(t, SchemaEvent, ev.Schema)
	assert.Equal(t, EventPartition, ev.Kind)
	assert.Equal(t, []string{"1", "2", "3"}, ev.Changes)
}

func TestPlanStopsWhenTheAffectedHookFails(t *testing.T) {
	st := newStager(map[string][]string{"1": {"app/a.go"}})
	broken := func(context.Context, Change, []string) ([]string, string, error) {
		return nil, "", errors.New("exit status 2")
	}
	pl := NewPlanner(st)
	pl.Affected = broken
	_, err := pl.Run(context.Background(), Changes{Schema: SchemaChanges, Base: "main", Changes: []Change{{ID: "1", Head: head("1")}}})
	require.EqualError(t, err, "affected set of #1: exit status 2", "misconfiguration is an error, never an unbounded guess")
}

func TestPlanRefusesAProviderThatReportsNoHead(t *testing.T) {
	pl := NewPlanner(newStager(nil))
	pl.Provider = &readHost{approval: func(Change) Approval { return Approval{Approved: true} }}
	_, err := pl.Run(context.Background(), Changes{Schema: SchemaChanges, Base: "main", Changes: []Change{change("1", "a")}})
	require.EqualError(t, err, "approval of #1: the provider reported no head")
}

func TestPlanRefusesInputGitCouldReadAsAnOption(t *testing.T) {
	bad := change("1", "a")
	bad.Branch = "--delete"
	_, err := NewPlanner(newStager(nil)).Run(context.Background(), Changes{Schema: SchemaChanges, Base: "main", Changes: []Change{bad}})
	require.EqualError(t, err, `changes[0]: #1: branch: "--delete" starts with '-'`)
}
