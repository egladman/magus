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

func TestPlanHoldsTheUnapprovedAndKicksBackForksAndBaseConflicts(t *testing.T) {
	w := newWorld(t, map[string]string{"app/a": "a\n", "lib/b": "b\n"})
	w.push(w.commit(w.base, "main moves lib/b", map[string]*string{"lib/b": str("main\n")}))
	unapproved := w.open("1", w.commit(w.base, "one", map[string]*string{"app/a": str("1\n")}))
	conflicting := w.open("2", w.commit(w.base, "two", map[string]*string{"lib/b": str("2\n")}))
	moved := w.open("3", w.commit(w.base, "three", map[string]*string{"web/c": str("3\n")}))
	w.open("4", w.commit(w.base, "four", map[string]*string{"api/d": str("4\n")}))
	w.open("5", w.commit(w.base, "five", map[string]*string{"x/e": str("5\n")}), func(c *Change) { c.Fork, c.Branch = true, "" })
	w.p.approvedAt["1"] = w.base
	in := w.changes()
	newHead := w.commit(moved.Head, "three again", map[string]*string{"web/c": str("33\n")})
	w.m.set(branchRef("pr3"), newHead)

	p, err := w.planner(0).Run(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, w.main(), p.BaseCommit)
	assert.Equal(t, 1, p.Depth, "depth 0 means 1")
	assert.Equal(t, [][]string{{"4"}}, ids(p.Partitions))
	assert.Equal(t, []string{"1", "2", "3", "5"}, idsOf(p.Verdicts), "verdicts keep queue order though admission runs side by side")
	v := verdictsByID(p)
	assert.Equal(t, Verdict{Change: unapproved, Decision: DecisionWait, Code: CodeNotApproved,
		Reason: "not approved at " + short(unapproved.Head) + ": 0 of 1 approvals at this commit"}, v["1"])
	assert.Equal(t, DecisionKick, v["2"].Decision)
	assert.Equal(t, CodeConflict, v["2"].Code)
	assert.Equal(t, []string{"lib/b"}, v["2"].Paths)
	assert.Contains(t, v["2"].Report, "`lib/b`")
	assert.Equal(t, []string{short(w.main()) + " main moves lib/b"}, v["2"].With, "the base commits that touched it")
	assert.Equal(t, conflicting.Head, v["2"].Change.Head)
	assert.Equal(t, newHead, v["3"].Change.Head, "the wait is reported on the new head")
	assert.Equal(t, CodeHeadMoved, v["3"].Code)
	assert.Equal(t, CodeRefused, v["5"].Code)
	assert.Equal(t, forkReport, v["5"].Report)
}

func TestPlanAsksTheAffectedHookOnlyForChangesWithoutASet(t *testing.T) {
	w := newWorld(t, map[string]string{"app/a.go": "a\n"})
	w.open("1", w.commit(w.base, "one", map[string]*string{"app/a.go": str("1\n")}), func(c *Change) { c.Affected = nil })
	w.open("2", w.commit(w.base, "two", map[string]*string{"lib/b.go": str("2\n")}), func(c *Change) { c.Affected = []string{"lib"} })
	w.open("3", w.commit(w.base, "three", map[string]*string{"magusfile.buzz": str("3\n")}), func(c *Change) { c.Affected = nil })
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
	var events bytes.Buffer
	pl := w.planner(2)
	pl.Facts, pl.Events = factsFunc(affected), NewEvents(&events)
	p, err := pl.Run(context.Background(), w.changes())
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
	w := newWorld(t, map[string]string{"a": "a\n"})
	w.open("1", w.commit(w.base, "one", map[string]*string{"a": str("1\n")}), func(c *Change) { c.Affected = nil })
	pl := w.planner(1)
	pl.Facts = factsFunc(func(context.Context, Change, []string) ([]string, string, error) {
		return nil, "", errors.New("exit status 2")
	})
	_, err := pl.Run(context.Background(), w.changes())
	require.EqualError(t, err, "affected set of #1 (change 1): exit status 2", "misconfiguration is an error, never an unbounded guess")
}

// noHead is a provider that omits where a change's head is.
type noHead struct{ *modelProvider }

func (n noHead) ApprovalAt(ctx context.Context, c Change, commit string) (Approval, error) {
	a, err := n.modelProvider.ApprovalAt(ctx, c, commit)
	a.Head = ""
	return a, err
}

// noMethod is a provider that omits how a change lands.
type noMethod struct{ *modelProvider }

func (n noMethod) ApprovalAt(ctx context.Context, c Change, commit string) (Approval, error) {
	a, err := n.modelProvider.ApprovalAt(ctx, c, commit)
	a.Method = ""
	return a, err
}

func TestPlanRefusesAProviderThatReportsNoHeadOrNoMethod(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	w.open("1", w.commit(w.base, "one", map[string]*string{"a": str("1\n")}))
	pl := w.planner(1)
	pl.Provider = noHead{w.p}
	_, err := pl.Run(context.Background(), w.changes())
	require.EqualError(t, err, "approval of #1 (change 1): the provider reported no head")
	pl.Provider = noMethod{w.p}
	_, err = pl.Run(context.Background(), w.changes())
	require.EqualError(t, err, `approval of #1 (change 1): the provider reported base "main" and merge method ""; both are required`)
}

func TestPlanRefusesInputGitCouldReadAsAnOption(t *testing.T) {
	bad := change("1", "a")
	bad.Branch = "--delete"
	_, err := NewPlanner(newModel()).Run(context.Background(), Changes{Schema: SchemaChanges, Base: "main", Changes: []Change{bad}})
	require.EqualError(t, err, `changes[0]: #1: branch: "--delete" starts with '-'`)
}

func TestPlanRefusesAMethodTheRepositoryDoesNotAllowAndWaitsOnWhatAlreadyLanded(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	w.p.caps.Methods = []MergeMethod{MethodSquash}
	w.open("1", w.commit(w.base, "one", map[string]*string{"a": str("1\n")}), func(c *Change) { c.Method = MethodRebase })
	landed := w.commit(w.base, "two", map[string]*string{"b": str("2\n")})
	w.push(landed)
	w.open("2", landed)
	v := verdictsByID(w.plan(1))
	assert.Equal(t, CodeRefused, v["1"].Code)
	assert.Contains(t, v["1"].Reason, "does not allow the rebase merge method")
	assert.Equal(t, CodeMerged, v["2"].Code)
}

// A provider that describes itself wrongly is misconfigured, and planning stops there.
func TestPlanRefusesAProviderThatAllowsNoMethod(t *testing.T) {
	w := newWorld(t, map[string]string{"a": "a\n"})
	w.p.caps.Methods = nil
	_, err := w.planner(1).Run(context.Background(), w.changes())
	require.EqualError(t, err, "the provider allows no merge method")
	w.p.caps = Capabilities{StackMerge: "sometimes", Methods: []MergeMethod{MethodSquash}}
	_, err = w.planner(1).Run(context.Background(), w.changes())
	require.ErrorContains(t, err, `describes stack merging as "sometimes"`)
}
