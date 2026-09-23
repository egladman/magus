package queue

import (
	"context"
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
	b       *branch
	refused map[string]bool
	pushed  map[string]bool // changes whose landing needs a regeneration pushed
}

func (l *fakeLander) Tip(context.Context, string) (string, error) {
	return "main@" + strings.Join(l.b.landed, ","), nil
}
func (l *fakeLander) Fetch(context.Context, Change) error { return nil }

// Expect merges the stage's ids onto what has landed.
func (l *fakeLander) Expect(_ context.Context, _, _, stage string) (string, error) {
	return treeOf(append(slices.Clone(l.b.landed), strings.Split(stage, "+")[1:]...)), nil
}

func (l *fakeLander) Prepare(_ context.Context, _ string, c Change, _ string) (Prepared, error) {
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
	approval func(c Change, call int) Approval
	calls    map[string]int
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
}

func (p *writeProvider) Name() string                                      { return "fake" }
func (p *writeProvider) List(context.Context, ListQuery) ([]Change, error) { return nil, nil }
func (p *writeProvider) ApprovalAt(_ context.Context, c Change, sha string) (Approval, error) {
	if p.calls == nil {
		p.calls = map[string]int{}
	}
	p.calls[c.ID]++
	p.asked = append(p.asked, sha)
	if p.approval != nil {
		return p.approval(c, p.calls[c.ID]), nil
	}
	return Approval{Approved: true, Head: c.Head}, nil
}

func (p *writeProvider) PostStatus(_ context.Context, c Change, sha string, s Status) error {
	p.statuses = append(p.statuses, posted{c.ID, sha, s.State})
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

func land(id string, order, group int, stage, after string) Planned {
	return Planned{Change: Change{ID: id, Head: "sha-" + id}, Order: order, Group: group,
		Decision: DecisionLand, Stage: stage, After: after, Message: "* " + id}
}

func newLanding() (*Landing, *fakeLander, *writeProvider) {
	b := &branch{}
	l := &fakeLander{b: b, refused: map[string]bool{}, pushed: map[string]bool{}}
	p := &writeProvider{b: b, refuse: map[string]bool{}}
	return &Landing{Provider: p, Lander: l}, l, p
}

func outcomes(rs []Result) map[string]Outcome {
	out := map[string]Outcome{}
	for _, r := range rs {
		out[r.Change.ID] = r.Outcome
	}
	return out
}

func manifest(changes ...Planned) Manifest {
	return Manifest{Version: ManifestVersion, Base: "main", BaseSHA: "base", Changes: changes}
}

func TestEveryChangeLandsAsItsOwnCommitInQueueOrder(t *testing.T) {
	l, _, p := newLanding()
	// Listed out of order on purpose: landing follows Order, not the slice.
	res, err := l.Run(context.Background(), manifest(
		land("2", 1, 0, "base+1+2", "1"),
		land("1", 0, 0, "base+1", ""),
		land("3", 2, 0, "base+1+2+3", "2"),
	))
	require.NoError(t, err)
	assert.Equal(t, []string{"1@sha-1: * 1", "2@sha-2: * 2", "3@sha-3: * 3"}, p.merges)
	assert.Equal(t, map[string]Outcome{"1": OutcomeMerged, "2": OutcomeMerged, "3": OutcomeMerged}, outcomes(res))
	assert.Equal(t, []posted{{"1", "sha-1", StateSuccess}, {"2", "sha-2", StateSuccess}, {"3", "sha-3", StateSuccess}}, p.statuses)
	assert.Equal(t, []string{"sha-1", "sha-2", "sha-3"}, p.asked, "approval re-checked at each validated head")
}

func TestAChangeValidatedOnOneThatDidNotLandWaits(t *testing.T) {
	l, _, p := newLanding()
	p.approval = func(c Change, _ int) Approval {
		return Approval{Approved: c.ID != "1", Head: c.Head, Reason: "changes requested"}
	}
	res, err := l.Run(context.Background(), manifest(land("1", 0, 0, "base+1", ""), land("2", 1, 0, "base+1+2", "1")))
	require.NoError(t, err)
	assert.Empty(t, p.merges)
	assert.Equal(t, map[string]Outcome{"1": OutcomeWaiting, "2": OutcomeWaiting}, outcomes(res))
	assert.Equal(t, "validated on top of #1, which did not land", res[1].Reason)
}

func TestIndependentPartitionsLandOnWhateverLandedBeforeThem(t *testing.T) {
	l, _, p := newLanding()
	_, err := l.Run(context.Background(), manifest(land("1", 0, 0, "base+1", ""), land("2", 1, 1, "base+2", "")))
	require.NoError(t, err)
	assert.Equal(t, []string{"1@sha-1: * 1", "2@sha-2: * 2"}, p.merges)
}

func TestKickAndWaitDecisionsReachTheProvider(t *testing.T) {
	l, _, p := newLanding()
	_, err := l.Run(context.Background(), manifest(
		Planned{Change: Change{ID: "1", Head: "sha-1"}, Decision: DecisionKick, Report: "the gate failed"},
		Planned{Change: Change{ID: "2", Head: "sha-2"}, Order: 1, Decision: DecisionWait, Reason: "not approved"},
	))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"1": "the gate failed"}, p.kicked)
	assert.Equal(t, []posted{{"1", "sha-1", StateFailure}, {"2", "sha-2", StatePending}}, p.statuses)
}

func TestARegenerationThePushLandsIsMergedAtItsOwnCommit(t *testing.T) {
	l, lander, p := newLanding()
	lander.pushed["1"] = true
	_, err := l.Run(context.Background(), manifest(land("1", 0, 0, "base+1", "")))
	require.NoError(t, err)
	assert.Equal(t, []string{"1@landing-1: * 1"}, p.merges)
	assert.Equal(t, []posted{{"1", "landing-1", StateSuccess}}, p.statuses)
}

func TestARefusedLandingIsKickedBack(t *testing.T) {
	l, lander, p := newLanding()
	lander.refused["1"] = true
	res, err := l.Run(context.Background(), manifest(land("1", 0, 0, "base+1", "")))
	require.NoError(t, err)
	assert.Equal(t, OutcomeKicked, outcomes(res)["1"])
	assert.Contains(t, p.kicked["1"], "cannot push to a fork")
}

func TestAHeadPushedAfterValidationIsNotMerged(t *testing.T) {
	l, _, p := newLanding()
	p.approval = func(Change, int) Approval { return Approval{Approved: true, Head: "sha-new"} }
	res, err := l.Run(context.Background(), manifest(land("1", 0, 0, "base+1", "")))
	require.NoError(t, err)
	assert.Equal(t, OutcomeWaiting, outcomes(res)["1"])
	assert.Empty(t, p.merges)
	assert.Empty(t, p.statuses, "no status on a commit that is no longer the head")
}

func TestAHostRefusalWaitsAndHoldsWhatStacksOnIt(t *testing.T) {
	l, _, p := newLanding()
	p.refuse["1"] = true
	res, err := l.Run(context.Background(), manifest(land("1", 0, 0, "base+1", ""), land("2", 1, 0, "base+1+2", "1")))
	require.NoError(t, err)
	assert.Equal(t, map[string]Outcome{"1": OutcomeWaiting, "2": OutcomeWaiting}, outcomes(res))
}

func TestABaseThatDoesNotCarryTheValidatedTreeStopsLanding(t *testing.T) {
	l, _, p := newLanding()
	p.extra = "intruder"
	_, err := l.Run(context.Background(), manifest(land("1", 0, 0, "base+1", ""), land("2", 1, 0, "base+1+2", "1")))
	require.ErrorContains(t, err, "not the validated")
	assert.Equal(t, []string{"1@sha-1: * 1"}, p.merges, "nothing lands on an unvalidated base")
}

func TestLandingDryRunCallsNothing(t *testing.T) {
	l, _, p := newLanding()
	l.DryRun = true
	res, err := l.Run(context.Background(), manifest(land("1", 0, 0, "base+1", ""),
		Planned{Change: Change{ID: "2", Head: "sha-2"}, Order: 1, Decision: DecisionKick, Report: "r"}))
	require.NoError(t, err)
	assert.Equal(t, map[string]Outcome{"1": OutcomeMerged, "2": OutcomeKicked}, outcomes(res))
	assert.Empty(t, p.statuses)
	assert.Empty(t, p.asked)
}
