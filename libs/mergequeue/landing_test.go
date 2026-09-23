package mergequeue

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// branch is the base branch as landing sees it: the ids merged into it, in order. A tree
// is the sorted set of ids it carries, so trees compare the way content would.
type branch struct{ landed []string }

func treeOf(ids []string) string {
	s := slices.Clone(ids)
	slices.Sort(s)
	return "tree:" + strings.Join(slices.Compact(s), ",")
}

type fakeLander struct {
	b        *branch
	refused  map[string]bool
	pushed   map[string]bool // changes whose landing needs a regeneration pushed
	imported []string
}

func (l *fakeLander) Tip(context.Context, string) (string, error) {
	return "main@" + strings.Join(l.b.landed, ","), nil
}
func (l *fakeLander) Fetch(context.Context, Change) error { return nil }
func (l *fakeLander) Import(_ context.Context, file string) error {
	l.imported = append(l.imported, file)
	return nil
}

// Expect merges the stage's ids onto what has landed.
func (l *fakeLander) Expect(_ context.Context, _, _, stage string) (string, error) {
	return treeOf(append(slices.Clone(l.b.landed), strings.Split(stage, "+")[1:]...)), nil
}

func (l *fakeLander) Prepare(_ context.Context, _, _ string, c Change, _ string) (Prepared, error) {
	if l.refused[c.ID] {
		return Prepared{}, &RefusedError{Reason: "cannot push to a fork"}
	}
	if l.pushed[c.ID] {
		return Prepared{Merge: "landing-" + c.ID}, nil
	}
	return Prepared{Merge: c.Head}, nil
}

func (l *fakeLander) TreeOf(context.Context, string) (string, error) { return treeOf(l.b.landed), nil }

type writeProvider struct {
	b        *branch
	approval func(c Change) Approval
	asked    []string
	statuses []posted
	merges   []string // id@sha: message
	kicked   map[string]string
	extra    string // lands an unvalidated id alongside every merge
	refuse   map[string]bool
}

type posted struct {
	id, sha string
	state   State
	context string
}

func (p *writeProvider) Name() string                                      { return "fake" }
func (p *writeProvider) List(context.Context, ListQuery) ([]Change, error) { return nil, nil }
func (p *writeProvider) ApprovalAt(_ context.Context, c Change, sha string) (Approval, error) {
	p.asked = append(p.asked, sha)
	if p.approval != nil {
		return p.approval(c), nil
	}
	return Approval{Approved: true, Head: c.Head}, nil
}

func (p *writeProvider) PostStatus(_ context.Context, c Change, sha string, s Status) error {
	p.statuses = append(p.statuses, posted{c.ID, sha, s.State, s.Context})
	return nil
}

func (p *writeProvider) Merge(_ context.Context, c Change, sha, message string) error {
	if p.refuse[c.ID] {
		return errors.New("405 not mergeable")
	}
	p.merges = append(p.merges, c.ID+"@"+sha+": "+message)
	p.b.landed = append(p.b.landed, c.ID)
	if p.extra != "" {
		p.b.landed = append(p.b.landed, p.extra)
	}
	return nil
}

func (p *writeProvider) KickBack(_ context.Context, c Change, _, report string) error {
	if p.kicked == nil {
		p.kicked = map[string]string{}
	}
	p.kicked[c.ID] = report
	return nil
}

// polls hands out verdicts in the batches a validation run would publish them: each
// Poll reveals one more batch, and the last batch comes with done.
type polls struct {
	batches [][]StageResult
	n       int
}

func (p *polls) Poll(context.Context) (map[string]StageResult, bool, error) {
	if p.n < len(p.batches) {
		p.n++
	}
	out := map[string]StageResult{}
	for _, b := range p.batches[:p.n] {
		for _, r := range b {
			out[r.Change.ID] = r
		}
	}
	return out, p.n == len(p.batches), nil
}

func green(id, stage, after string) StageResult {
	return StageResult{Schema: SchemaStage, BaseSHA: "base", Change: change(id, "a"), Decision: DecisionLand,
		Stage: stage, After: after, Message: "* " + id}
}

type landed struct {
	events []Event
}

func (l landed) outcomes() map[string]string {
	out := map[string]string{}
	for _, e := range l.events {
		out[e.Change] = e.Event
	}
	return out
}

func (l landed) reason(id string) string {
	for _, e := range slices.Backward(l.events) {
		if e.Change == id {
			return e.Reason
		}
	}
	return ""
}

func newLanding() (*Landing, *fakeLander, *writeProvider) {
	b := &branch{}
	l := &fakeLander{b: b, refused: map[string]bool{}, pushed: map[string]bool{}}
	p := &writeProvider{b: b, refuse: map[string]bool{}}
	return &Landing{Provider: p, Lander: l, Interval: 1}, l, p
}

func landAll(t *testing.T, l *Landing, p Plan, src Results) (landed, error) {
	t.Helper()
	var buf bytes.Buffer
	l.Events = NewEvents(&buf)
	err := l.Run(context.Background(), p, src)
	var out landed
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		var e Event
		require.NoError(t, json.Unmarshal(sc.Bytes(), &e))
		out.events = append(out.events, e)
	}
	return out, err
}

func once(rs ...StageResult) *polls { return &polls{batches: [][]StageResult{rs}} }

func TestEveryChangeLandsAsItsOwnCommitInQueueOrder(t *testing.T) {
	l, _, p := newLanding()
	pl := planOf(3, []Change{change("1", "a"), change("2", "a"), change("3", "a")})
	got, err := landAll(t, l, pl, once(green("2", "base+1+2", "1"), green("1", "base+1", ""), green("3", "base+1+2+3", "2")))
	require.NoError(t, err)
	assert.Equal(t, []string{"1@sha-1: * 1", "2@sha-2: * 2", "3@sha-3: * 3"}, p.merges)
	assert.Equal(t, map[string]string{"1": EventMerged, "2": EventMerged, "3": EventMerged}, got.outcomes())
	assert.Equal(t, []posted{{"1", "sha-1", StateSuccess, DefaultStatusContext}, {"2", "sha-2", StateSuccess, DefaultStatusContext},
		{"3", "sha-3", StateSuccess, DefaultStatusContext}}, p.statuses)
	assert.Equal(t, []string{"sha-1", "sha-2", "sha-3"}, p.asked, "approval re-checked at each validated head")
}

// What landing only after the whole run misses: a green stage lands while stages above
// it and other partitions are still validating, and a verdict that arrives before the
// one beneath it waits for it rather than landing out of order.
func TestEachStageLandsTheMomentItAndEverythingBeneathItAreGreen(t *testing.T) {
	l, lander, p := newLanding()
	pl := planOf(3, []Change{change("1", "a"), change("2", "a")}, []Change{change("7", "b")})
	two := green("2", "base+1+2", "1")
	two.Bundle = "stages/2/stage.bundle"
	src := &polls{batches: [][]StageResult{
		{two},
		{green("7", "base+7", "")},
		{green("1", "base+1", "")},
	}}
	var order []string
	p.approval = func(c Change) Approval {
		order = append(order, c.ID+"@poll"+string(rune('0'+src.n)))
		return Approval{Approved: true, Head: c.Head}
	}
	_, err := landAll(t, l, pl, src)
	require.NoError(t, err)
	assert.Equal(t, []string{"7@poll2", "1@poll3", "2@poll3"}, order,
		"#7 lands while #1 is still validating; #2, green first, waits for #1")
	assert.Equal(t, []string{"stages/2/stage.bundle"}, lander.imported)
}

func TestAChangeValidatedOnOneThatDidNotLandWaits(t *testing.T) {
	l, _, p := newLanding()
	p.approval = func(c Change) Approval {
		return Approval{Approved: c.ID != "1", Head: c.Head, Reason: "changes requested"}
	}
	got, err := landAll(t, l, planOf(2, []Change{change("1", "a"), change("2", "a")}), once(green("1", "base+1", ""), green("2", "base+1+2", "1")))
	require.NoError(t, err)
	assert.Empty(t, p.merges)
	assert.Equal(t, map[string]string{"1": EventWaiting, "2": EventWaiting}, got.outcomes())
	assert.Equal(t, "validated on top of #1, which did not land", got.reason("2"))
}

func TestPlanDecisionsAndRedVerdictsReachTheProvider(t *testing.T) {
	l, _, p := newLanding()
	l.StatusContext = "magus/queue"
	pl := planOf(1, []Change{change("3", "a")})
	pl.Decided = []Decided{
		{Change: change("1"), Decision: DecisionKick, Report: "conflicts with main"},
		{Change: change("2"), Decision: DecisionWait, Reason: "not approved"},
	}
	red := StageResult{Schema: SchemaStage, BaseSHA: "base", Change: change("3", "a"), Decision: DecisionKick, Report: "the gate failed"}
	_, err := landAll(t, l, pl, once(red))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"1": "conflicts with main", "3": "the gate failed"}, p.kicked)
	assert.Equal(t, []posted{{"1", "sha-1", StateFailure, "magus/queue"}, {"2", "sha-2", StatePending, "magus/queue"},
		{"3", "sha-3", StateFailure, "magus/queue"}}, p.statuses)
}

func TestWhatNoVerdictReachedWaitsForTheNextRun(t *testing.T) {
	l, _, p := newLanding()
	got, err := landAll(t, l, planOf(1, []Change{change("1", "a"), change("2", "a")}), once(green("1", "base+1", "")))
	require.NoError(t, err)
	assert.Equal(t, []string{"1@sha-1: * 1"}, p.merges)
	assert.Equal(t, "not validated in this run", got.reason("2"))
}

func TestAVerdictOnAnotherBaseWaits(t *testing.T) {
	l, _, p := newLanding()
	stale := green("1", "old+1", "")
	stale.BaseSHA = "old"
	got, err := landAll(t, l, planOf(1, []Change{change("1", "a")}), once(stale))
	require.NoError(t, err)
	assert.Empty(t, p.merges)
	assert.Contains(t, got.reason("1"), "not this plan's base")
}

func TestARegenerationThePushLandsIsMergedAtItsOwnCommit(t *testing.T) {
	l, lander, p := newLanding()
	lander.pushed["1"] = true
	_, err := landAll(t, l, planOf(1, []Change{change("1", "a")}), once(green("1", "base+1", "")))
	require.NoError(t, err)
	assert.Equal(t, []string{"1@landing-1: * 1"}, p.merges)
	assert.Equal(t, []posted{{"1", "landing-1", StateSuccess, DefaultStatusContext}}, p.statuses)
}

func TestARefusedLandingIsKickedBack(t *testing.T) {
	l, lander, p := newLanding()
	lander.refused["1"] = true
	got, err := landAll(t, l, planOf(1, []Change{change("1", "a")}), once(green("1", "base+1", "")))
	require.NoError(t, err)
	assert.Equal(t, EventKicked, got.outcomes()["1"])
	assert.Contains(t, p.kicked["1"], "cannot push to a fork")
}

func TestAHeadPushedAfterValidationIsNotMerged(t *testing.T) {
	l, _, p := newLanding()
	p.approval = func(Change) Approval { return Approval{Approved: true, Head: "sha-new"} }
	got, err := landAll(t, l, planOf(1, []Change{change("1", "a")}), once(green("1", "base+1", "")))
	require.NoError(t, err)
	assert.Equal(t, EventWaiting, got.outcomes()["1"])
	assert.Empty(t, p.merges)
	assert.Empty(t, p.statuses, "no status on a commit that is no longer the head")
}

func TestAHostRefusalWaitsAndHoldsWhatStacksOnIt(t *testing.T) {
	l, _, p := newLanding()
	p.refuse["1"] = true
	got, err := landAll(t, l, planOf(2, []Change{change("1", "a"), change("2", "a")}), once(green("1", "base+1", ""), green("2", "base+1+2", "1")))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"1": EventWaiting, "2": EventWaiting}, got.outcomes())
}

func TestABaseThatDoesNotCarryTheValidatedTreeStopsLanding(t *testing.T) {
	l, _, p := newLanding()
	p.extra = "intruder"
	_, err := landAll(t, l, planOf(2, []Change{change("1", "a"), change("2", "a")}), once(green("1", "base+1", ""), green("2", "base+1+2", "1")))
	require.ErrorContains(t, err, "not the validated")
	assert.Equal(t, []string{"1@sha-1: * 1"}, p.merges, "nothing lands on an unvalidated base")
}

func TestLandingDryRunCallsNothing(t *testing.T) {
	l, _, p := newLanding()
	l.DryRun = true
	red := StageResult{Schema: SchemaStage, BaseSHA: "base", Change: change("2", "a"), Decision: DecisionKick, Report: "r"}
	got, err := landAll(t, l, planOf(2, []Change{change("1", "a"), change("2", "a")}), once(green("1", "base+1", ""), red))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"1": EventMerged, "2": EventKicked}, got.outcomes())
	assert.Empty(t, p.statuses)
	assert.Empty(t, p.asked)
}

func TestDirResultsSeesOnlyFinishedVerdictsAndTheDoneMarker(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	src := &DirResults{Dir: dir, Follow: true}
	got, done, err := src.Poll(ctx)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.False(t, done)

	exported := ""
	export := func(_ context.Context, file, stage string) error {
		exported = stage
		return WriteJSON(file, "bundle")
	}
	g := green("pr/1", "base+1", "")
	require.NoError(t, WriteResult(ctx, dir, g, export))
	require.NoError(t, WriteResult(ctx, dir, StageResult{Schema: SchemaStage, Change: change("2"), Decision: DecisionWait}, export))
	require.NoError(t, MarkDone(dir))
	got, done, err = src.Poll(ctx)
	require.NoError(t, err)
	assert.True(t, done)
	assert.Equal(t, "base+1", exported, "only a green verdict carries its stage")
	require.Contains(t, got, "pr/1", "an id is path-escaped on disk and restored on read")
	assert.Equal(t, dir+"/pr%2F1/"+BundleFile, got["pr/1"].Bundle)
	assert.Empty(t, got["2"].Bundle)
}
