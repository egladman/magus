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

// branch is the base branch as a Lander sees it: the ids merged into it, in order. A
// tree is the sorted set of ids it carries, so trees compare the way content would.
type branch struct{ landed []string }

func treeOf(ids []string) string {
	s := slices.Clone(ids)
	slices.Sort(s)
	return "tree:" + strings.Join(slices.Compact(s), ",")
}

type fakeLandingRepo struct {
	b        *branch
	refused  map[string]bool
	pushed   map[string]bool // changes whose landing needs a regeneration pushed
	moved    map[string]bool // changes whose branch moved before the push
	stale    map[string]bool // changes whose stage shares a file with what landed since
	imported []string
}

func (l *fakeLandingRepo) FetchTip(context.Context, string) (string, error) {
	return "main@" + strings.Join(l.b.landed, ","), nil
}
func (l *fakeLandingRepo) FetchHead(context.Context, Change) error { return nil }
func (l *fakeLandingRepo) ReviewTarget(_ context.Context, _, h string) (string, error) {
	return h, nil
}

func (l *fakeLandingRepo) ImportBundle(_ context.Context, file string) error {
	l.imported = append(l.imported, file)
	return nil
}

// Predict merges the stage's ids onto what has landed.
func (l *fakeLandingRepo) Predict(_ context.Context, _, _, _, stage string) (string, error) {
	ids := strings.Split(stage, "+")[1:]
	if l.stale[ids[len(ids)-1]] {
		return "", &ConflictError{Conflict: Conflict{Paths: []string{"MAGUS.md"}}}
	}
	return treeOf(append(slices.Clone(l.b.landed), ids...)), nil
}

func (l *fakeLandingRepo) PushLanding(_ context.Context, _, _ string, c Change, _ string) (string, error) {
	switch {
	case l.refused[c.ID]:
		return "", &RefusedError{Reason: "cannot push to a fork"}
	case l.moved[c.ID]:
		return "", &WaitError{Reason: "its branch moved or was deleted since validation"}
	case l.pushed[c.ID]:
		return head("landing-" + c.ID), nil
	}
	return c.Head, nil
}

func (l *fakeLandingRepo) TreeOf(context.Context, string) (string, error) {
	return treeOf(l.b.landed), nil
}

type writeProvider struct {
	b        *branch
	approval func(c Change) Approval
	asked    []string
	statuses []posted
	merges   []string      // id@commit: message
	atMerge  []CommitState // the last state posted on each merged commit when it merged
	kicked   map[string]string
	extra    string // lands an unvalidated id alongside every merge
	refuse   map[string]bool
}

type posted struct {
	id, commit string
	state      CommitState
	context    string
}

func (p *writeProvider) ListChanges(context.Context, ListQuery) ([]Change, error) { return nil, nil }
func (p *writeProvider) ApprovalAt(_ context.Context, c Change, commit string) (Approval, error) {
	p.asked = append(p.asked, commit)
	if p.approval != nil {
		return p.approval(c), nil
	}
	return Approval{Approved: true, Head: c.Head}, nil
}

func (p *writeProvider) PostStatus(_ context.Context, c Change, commit string, s CommitStatus) error {
	p.statuses = append(p.statuses, posted{c.ID, commit, s.State, s.Context})
	return nil
}

func (p *writeProvider) MergeChange(_ context.Context, c Change, commit, message string) error {
	var last CommitState
	for _, s := range p.statuses {
		if s.commit == commit {
			last = s.state
		}
	}
	p.atMerge = append(p.atMerge, last)
	if p.refuse[c.ID] {
		return errors.New("405 not mergeable")
	}
	p.merges = append(p.merges, c.ID+"@"+idOf(commit)+": "+message)
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
// Poll returns the next batch, and the last batch comes with done.
type polls struct {
	batches [][]Verdict
	n       int
}

func (p *polls) Poll(context.Context) ([]Verdict, bool, error) {
	var fresh []Verdict
	if p.n < len(p.batches) {
		fresh = p.batches[p.n]
		p.n++
	}
	return fresh, p.n == len(p.batches), nil
}

// green is the verdict on id validated onto onto, beneath which after was validated.
func green(id, onto, after string) Verdict {
	return Verdict{BaseCommit: base, Change: change(id, "a"), Decision: DecisionLand,
		Onto: onto, Stage: onto + "+" + id, After: after, Message: "* " + id}
}

type landed struct{ events []Event }

func (l landed) outcomes() map[string]EventKind {
	out := map[string]EventKind{}
	for _, e := range l.events {
		out[e.Change] = e.Kind
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

func newLanding(src VerdictSource) (*Lander, *fakeLandingRepo, *writeProvider) {
	b := &branch{}
	repo := &fakeLandingRepo{b: b, refused: map[string]bool{}, pushed: map[string]bool{}, moved: map[string]bool{}, stale: map[string]bool{}}
	p := &writeProvider{b: b, refuse: map[string]bool{}}
	l := NewLander(p, repo, src)
	l.Interval = 1
	return l, repo, p
}

func landAll(t *testing.T, l *Lander, p Plan) (landed, error) {
	t.Helper()
	var buf bytes.Buffer
	l.Events = NewEvents(&buf)
	err := l.Run(context.Background(), p)
	var out landed
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		var e Event
		require.NoError(t, json.Unmarshal(sc.Bytes(), &e))
		out.events = append(out.events, e)
	}
	return out, err
}

func once(vs ...Verdict) *polls { return &polls{batches: [][]Verdict{vs}} }

func TestEveryChangeLandsAsItsOwnCommitInQueueOrder(t *testing.T) {
	l, _, p := newLanding(once(green("2", base+"+1", "1"), green("1", base, ""), green("3", base+"+1+2", "2")))
	got, err := landAll(t, l, planOf(3, []Change{change("1", "a"), change("2", "a"), change("3", "a")}))
	require.NoError(t, err)
	assert.Equal(t, []string{"1@1: * 1", "2@2: * 2", "3@3: * 3"}, p.merges)
	assert.Equal(t, map[string]EventKind{"1": EventMerged, "2": EventMerged, "3": EventMerged}, got.outcomes())
	assert.Equal(t, []string{head("1"), head("2"), head("3")}, p.asked, "approval re-checked at each validated head")
}

// A success posted before the merge satisfies branch protection on its own: a crash in
// between would leave a change anyone could merge onto whatever the base had become.
// Before, the status read success when MergeChange ran.
func TestSuccessIsPostedOnlyOnceTheChangeMerged(t *testing.T) {
	l, _, p := newLanding(once(green("1", base, ""), green("2", base+"+1", "1")))
	p.refuse["2"] = true
	_, err := landAll(t, l, planOf(2, []Change{change("1", "a"), change("2", "a")}))
	require.NoError(t, err)
	assert.Equal(t, []CommitState{StatePending, StatePending}, p.atMerge, "pending while the merge is asked for")
	assert.Equal(t, []posted{
		{"1", head("1"), StatePending, DefaultStatusContext}, {"1", head("1"), StateSuccess, DefaultStatusContext},
		{"2", head("2"), StatePending, DefaultStatusContext}, {"2", head("2"), StatePending, DefaultStatusContext},
	}, p.statuses, "a refused merge never reads success")
}

// What landing only after the whole run misses: a green stage lands while stages above
// it and other partitions are still validating, and a verdict that arrives before the
// one beneath it waits for it rather than landing out of order.
func TestEachStageLandsTheMomentItAndEverythingBeneathItAreGreen(t *testing.T) {
	two := green("2", base+"+1", "1")
	two.Bundle = "verdicts/2/stage.bundle"
	src := &polls{batches: [][]Verdict{{two}, {green("7", base, "")}, {green("1", base, "")}}}
	l, repo, p := newLanding(src)
	var order []string
	p.approval = func(c Change) Approval {
		order = append(order, c.ID+"@poll"+string(rune('0'+src.n)))
		return Approval{Approved: true, Head: c.Head}
	}
	_, err := landAll(t, l, planOf(3, []Change{change("1", "a"), change("2", "a")}, []Change{change("7", "b")}))
	require.NoError(t, err)
	assert.Equal(t, []string{"7@poll2", "1@poll3", "2@poll3"}, order,
		"#7 lands while #1 is still validating; #2, green first, waits for #1")
	assert.Equal(t, []string{"verdicts/2/stage.bundle"}, repo.imported)
}

func TestAChangeValidatedOnOneThatDidNotLandWaits(t *testing.T) {
	l, _, p := newLanding(once(green("1", base, ""), green("2", base+"+1", "1")))
	p.approval = func(c Change) Approval {
		return Approval{Approved: c.ID != "1", Head: c.Head, Reason: "changes requested"}
	}
	got, err := landAll(t, l, planOf(2, []Change{change("1", "a"), change("2", "a")}))
	require.NoError(t, err)
	assert.Empty(t, p.merges)
	assert.Equal(t, map[string]EventKind{"1": EventWaiting, "2": EventWaiting}, got.outcomes())
	assert.Equal(t, "validated on top of #1, which did not land", got.reason("2"))
}

// The plan says which head was admitted and what lies beneath each change; a verdict
// file saying otherwise was trusted before, and landed.
func TestAVerdictThatDisagreesWithThePlanLandsNothing(t *testing.T) {
	forged := green("1", base, "")
	forged.Change.Head = head("other")
	skipped := green("3", base+"+9", "9")
	l, _, p := newLanding(once(forged, green("2", base, ""), skipped))
	got, err := landAll(t, l, planOf(3, []Change{change("1", "a")}, []Change{change("2", "b")}, []Change{change("3", "c")}))
	require.NoError(t, err)
	assert.Equal(t, []string{"2@2: * 2"}, p.merges)
	assert.Equal(t, "validated at "+short(head("other"))+", not the planned head "+short(head("1")), got.reason("1"))
	assert.Equal(t, "validated on top of #9, which is not beneath it in its partition", got.reason("3"))

	l, _, _ = newLanding(once(green("5", base, "")))
	_, err = landAll(t, l, planOf(1, []Change{change("1", "a")}))
	require.EqualError(t, err, "a verdict names #5, which the plan did not admit")
}

func TestPlanVerdictsAndRedVerdictsReachTheProvider(t *testing.T) {
	red := Verdict{BaseCommit: base, Change: change("3", "a"), Decision: DecisionKick, Report: "the gate failed"}
	l, _, p := newLanding(once(red))
	l.StatusContext = "magus/queue"
	pl := planOf(1, []Change{change("3", "a")})
	pl.Verdicts = []Verdict{
		{Change: change("1"), Decision: DecisionKick, Report: "conflicts with main"},
		{Change: change("2"), Decision: DecisionWait, Reason: "not approved"},
	}
	_, err := landAll(t, l, pl)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"1": "conflicts with main", "3": "the gate failed"}, p.kicked)
	assert.Equal(t, []posted{{"1", head("1"), StateFailure, "magus/queue"}, {"2", head("2"), StatePending, "magus/queue"},
		{"3", head("3"), StateFailure, "magus/queue"}}, p.statuses)
}

func TestWhatNoVerdictReachedWaitsForTheNextRun(t *testing.T) {
	l, _, p := newLanding(once(green("1", base, "")))
	got, err := landAll(t, l, planOf(1, []Change{change("1", "a"), change("2", "a")}))
	require.NoError(t, err)
	assert.Equal(t, []string{"1@1: * 1"}, p.merges)
	assert.Equal(t, "not validated in this run", got.reason("2"))
}

func TestAVerdictOnAnotherBaseWaits(t *testing.T) {
	stale := green("1", base, "")
	stale.BaseCommit = strings.Repeat("0", 40)
	l, _, p := newLanding(once(stale))
	got, err := landAll(t, l, planOf(1, []Change{change("1", "a")}))
	require.NoError(t, err)
	assert.Empty(t, p.merges)
	assert.Contains(t, got.reason("1"), "not this plan's base")
}

func TestAStageSharingAFileWithWhatLandedIsRestaged(t *testing.T) {
	l, repo, p := newLanding(once(green("1", base, "")))
	repo.stale["1"] = true
	got, err := landAll(t, l, planOf(1, []Change{change("1", "a")}))
	require.NoError(t, err)
	assert.Empty(t, p.merges)
	assert.Equal(t, "MAGUS.md changed both here and in what landed since validation; restaged next run", got.reason("1"))
}

func TestARegenerationThePushLandsIsMergedAtItsOwnCommit(t *testing.T) {
	l, repo, p := newLanding(once(green("1", base, "")))
	repo.pushed["1"] = true
	_, err := landAll(t, l, planOf(1, []Change{change("1", "a")}))
	require.NoError(t, err)
	assert.Equal(t, []string{"1@landing-1: * 1"}, p.merges)
	assert.Equal(t, []posted{{"1", head("landing-1"), StatePending, DefaultStatusContext},
		{"1", head("landing-1"), StateSuccess, DefaultStatusContext}}, p.statuses)
}

func TestARefusedLandingIsKickedBackAndAMovedBranchWaits(t *testing.T) {
	l, repo, p := newLanding(once(green("1", base, ""), green("2", base, "")))
	repo.refused["1"] = true
	repo.moved["2"] = true
	got, err := landAll(t, l, planOf(1, []Change{change("1", "a")}, []Change{change("2", "b")}))
	require.NoError(t, err)
	assert.Equal(t, EventKicked, got.outcomes()["1"])
	assert.Contains(t, p.kicked["1"], "cannot push to a fork")
	assert.Equal(t, EventWaiting, got.outcomes()["2"])
	assert.Equal(t, "its branch moved or was deleted since validation", got.reason("2"))
}

func TestAHeadPushedAfterValidationIsNotMerged(t *testing.T) {
	l, _, p := newLanding(once(green("1", base, "")))
	moved := head("new")
	p.approval = func(Change) Approval { return Approval{Approved: true, Head: moved} }
	got, err := landAll(t, l, planOf(1, []Change{change("1", "a")}))
	require.NoError(t, err)
	assert.Equal(t, EventWaiting, got.outcomes()["1"])
	assert.Empty(t, p.merges)
	assert.Equal(t, []posted{{"1", moved, StatePending, DefaultStatusContext}}, p.statuses,
		"pending on the new head; nothing on a commit that is no longer the head")
}

func TestAProviderThatReportsNoHeadStopsLanding(t *testing.T) {
	l, _, p := newLanding(once(green("1", base, "")))
	p.approval = func(Change) Approval { return Approval{Approved: true} }
	_, err := landAll(t, l, planOf(1, []Change{change("1", "a")}))
	require.EqualError(t, err, "approval of #1: the provider reported no head")
	assert.Empty(t, p.merges)
}

func TestAHostRefusalWaitsAndHoldsWhatStacksOnIt(t *testing.T) {
	l, _, p := newLanding(once(green("1", base, ""), green("2", base+"+1", "1")))
	p.refuse["1"] = true
	got, err := landAll(t, l, planOf(2, []Change{change("1", "a"), change("2", "a")}))
	require.NoError(t, err)
	assert.Equal(t, map[string]EventKind{"1": EventWaiting, "2": EventWaiting}, got.outcomes())
}

func TestABaseThatDoesNotCarryTheValidatedTreeStopsLanding(t *testing.T) {
	l, _, p := newLanding(once(green("1", base, ""), green("2", base+"+1", "1")))
	p.extra = "intruder"
	_, err := landAll(t, l, planOf(2, []Change{change("1", "a"), change("2", "a")}))
	require.ErrorContains(t, err, "not the validated")
	assert.Equal(t, []string{"1@1: * 1"}, p.merges, "nothing lands on an unvalidated base")
	assert.NotContains(t, p.statuses, posted{"1", head("1"), StateSuccess, DefaultStatusContext})
}

func TestLandingDryRunCallsNothing(t *testing.T) {
	red := Verdict{BaseCommit: base, Change: change("2", "a"), Decision: DecisionKick, Report: "r"}
	l, _, p := newLanding(once(green("1", base, ""), red))
	l.DryRun = true
	got, err := landAll(t, l, planOf(2, []Change{change("1", "a"), change("2", "a")}))
	require.NoError(t, err)
	assert.Equal(t, map[string]EventKind{"1": EventMerged, "2": EventKicked}, got.outcomes())
	assert.Empty(t, p.statuses)
	assert.Empty(t, p.asked)
}
