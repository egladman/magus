package queue

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// world is the shared state behind the fakes: the base branch is the ordered list of
// change ids merged into it, so a merge through the provider moves what the repo sees.
type world struct {
	landed []string
	// conflicts pairs change ids whose source files collide; "base" collides with the
	// base branch as it started.
	conflicts map[[2]string][]string
	paths     map[string][]string
}

func (w *world) tip() string { return "base+" + strings.Join(w.landed, "+") }

func (w *world) conflictOf(present []string, c Change) (Conflict, bool) {
	for _, other := range append([]string{"base"}, present...) {
		if p, ok := w.conflicts[[2]string{other, c.ID}]; ok {
			return Conflict{Change: c, Paths: p, With: []string{"abc123 an earlier change"}}, true
		}
	}
	return Conflict{}, false
}

type fakeRepo struct {
	w      *world
	stages [][]string
}

func (f *fakeRepo) Tip(context.Context, string) (string, error) { return f.w.tip(), nil }
func (f *fakeRepo) Fetch(context.Context, Change) error           { return nil }
func (f *fakeRepo) Changed(_ context.Context, _, head string) ([]string, error) {
	return f.w.paths[strings.TrimPrefix(head, "sha-")], nil
}

func (f *fakeRepo) Overlap(_ context.Context, _ string, changes []Change) error {
	present := slices.Clone(f.w.landed)
	for _, c := range changes {
		if conf, ok := f.w.conflictOf(present, c); ok {
			return &ConflictError{Conflict: conf}
		}
		present = append(present, c.ID)
	}
	return nil
}

func (f *fakeRepo) Stage(ctx context.Context, base string, changes []Change) (string, error) {
	if err := f.Overlap(ctx, base, changes); err != nil {
		return "", err
	}
	ids := make([]string, len(changes))
	for i, c := range changes {
		ids[i] = c.ID
	}
	f.stages = append(f.stages, ids)
	return "stage:" + strings.Join(ids, ","), nil
}

func (f *fakeRepo) Land(ctx context.Context, base string, c Change) (Landing, error) {
	if err := f.Overlap(ctx, base, []Change{c}); err != nil {
		return Landing{}, err
	}
	return Landing{Merge: c.Head, Expect: base + "+" + c.ID}, nil
}

func (f *fakeRepo) SameTree(_ context.Context, a, b string) (bool, error) {
	return strings.ReplaceAll(a, "base++", "base+") == strings.ReplaceAll(b, "base++", "base+"), nil
}

type posted struct {
	id, sha string
	state   State
}

type fakeProvider struct {
	w        *world
	changes  []Change
	approval func(c Change, sha string, call int) Approval
	calls    map[string]int
	asked    []string // sha each approval was asked at
	statuses []posted
	merged   []string
	kicked   map[string]string
	rewrite  bool // merge lands something other than what was validated
}

func (p *fakeProvider) Name() string { return "fake" }
func (p *fakeProvider) List(context.Context, ListQuery) ([]Change, error) {
	return p.changes, nil
}

func (p *fakeProvider) ApprovalAt(_ context.Context, c Change, sha string) (Approval, error) {
	if p.calls == nil {
		p.calls = map[string]int{}
	}
	p.calls[c.ID]++
	p.asked = append(p.asked, sha)
	if p.approval != nil {
		return p.approval(c, sha, p.calls[c.ID]), nil
	}
	return Approval{Approved: true, Head: c.Head}, nil
}

func (p *fakeProvider) PostStatus(_ context.Context, c Change, sha string, s Status) error {
	p.statuses = append(p.statuses, posted{c.ID, sha, s.State})
	return nil
}

func (p *fakeProvider) Merge(_ context.Context, c Change, sha string) error {
	if sha != c.Head {
		return errors.New("head moved")
	}
	p.merged = append(p.merged, c.ID)
	id := c.ID
	if p.rewrite {
		id += "-rewritten"
	}
	p.w.landed = append(p.w.landed, id)
	return nil
}

func (p *fakeProvider) KickBack(_ context.Context, c Change, _, report string) error {
	if p.kicked == nil {
		p.kicked = map[string]string{}
	}
	p.kicked[c.ID] = report
	return nil
}

// fakeValidator turns a staging commit red when it carries any bad change.
type fakeValidator struct {
	bad  map[string]bool
	runs []string
}

func (v *fakeValidator) Validate(_ context.Context, commit, _ string) (Verdict, error) {
	v.runs = append(v.runs, commit)
	for _, id := range strings.Split(strings.TrimPrefix(commit, "stage:"), ",") {
		if v.bad[id] {
			return Verdict{Summary: "magus affected ci failed in " + id}, nil
		}
	}
	return Verdict{Green: true}, nil
}

// fakeGraph maps a path's first segment to its project; a magusfile edit is unproven.
type fakeGraph struct{}

func (fakeGraph) Closure(_ context.Context, paths []string) (Closure, error) {
	c := Closure{Proven: true}
	for _, p := range paths {
		if p == "magusfile.buzz" {
			return Closure{Why: "declarations changed"}, nil
		}
		proj, _, _ := strings.Cut(p, "/")
		if !slices.Contains(c.Projects, proj) {
			c.Projects = append(c.Projects, proj)
		}
	}
	return c, nil
}

type harness struct {
	w    *world
	repo *fakeRepo
	prov *fakeProvider
	val  *fakeValidator
	log  bytes.Buffer
	eng  *Engine
}

// newHarness queues one change per id, in order, each touching paths[id].
func newHarness(paths map[string][]string, ids ...string) *harness {
	w := &world{paths: paths, conflicts: map[[2]string][]string{}}
	h := &harness{w: w, repo: &fakeRepo{w: w}, val: &fakeValidator{bad: map[string]bool{}}}
	h.prov = &fakeProvider{w: w}
	for _, id := range ids {
		h.prov.changes = append(h.prov.changes, Change{ID: id, Head: "sha-" + id, Base: "main"})
	}
	h.eng = &Engine{Provider: h.prov, Repo: h.repo, Graph: fakeGraph{}, Validator: h.val, Base: "main", Log: &h.log}
	return h
}

func outcomes(r Report) map[string]Outcome {
	out := map[string]Outcome{}
	for _, res := range r.Results {
		out[res.Change.ID] = res.Outcome
	}
	return out
}

func TestSerialMergeLandsOverlappingChangesInQueueOrder(t *testing.T) {
	h := newHarness(map[string][]string{"1": {"app/a.go"}, "2": {"app/b.go"}}, "1", "2")
	rep, err := h.eng.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2"}, h.prov.merged)
	assert.Equal(t, [][]string{{"1", "2"}}, rep.Groups)
	assert.Equal(t, 1, rep.Validations, "one stack, validated once")
	assert.Equal(t, map[string]Outcome{"1": OutcomeMerged, "2": OutcomeMerged}, outcomes(rep))
	assert.Contains(t, h.prov.statuses, posted{"1", "sha-1", StateSuccess})
	assert.Contains(t, h.prov.statuses, posted{"2", "sha-2", StateSuccess})
}

func TestConflictWithBaseKicksBackWithTheOverlapReport(t *testing.T) {
	h := newHarness(map[string][]string{"1": {"app/a.go"}, "2": {"lib/b.go"}}, "1", "2")
	h.w.conflicts[[2]string{"base", "1"}] = []string{"app/a.go"}
	rep, err := h.eng.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]Outcome{"1": OutcomeKicked, "2": OutcomeMerged}, outcomes(rep))
	assert.Equal(t, []string{"2"}, h.prov.merged)
	require.Contains(t, h.prov.kicked, "1")
	assert.Contains(t, h.prov.kicked["1"], "`app/a.go`")
	assert.Contains(t, h.prov.kicked["1"], "abc123 an earlier change")
	assert.Contains(t, h.prov.statuses, posted{"1", "sha-1", StateFailure})
}

func TestDisjointChangesMergeIndependentlyWhenOneFails(t *testing.T) {
	h := newHarness(map[string][]string{"1": {"app/a.go"}, "2": {"lib/b.go"}}, "1", "2")
	h.val.bad["2"] = true
	rep, err := h.eng.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"1"}, {"2"}}, rep.Groups)
	assert.Equal(t, map[string]Outcome{"1": OutcomeMerged, "2": OutcomeKicked}, outcomes(rep))
	// The batch, then each group once: #1 is never validated on top of #2.
	assert.Equal(t, []string{"stage:1,2", "stage:1", "stage:2"}, h.val.runs)
	assert.Contains(t, h.prov.kicked["2"], "failed in 2")
}

func TestDisjointChangesShareOneValidationWhenGreen(t *testing.T) {
	h := newHarness(map[string][]string{"1": {"app/a.go"}, "2": {"lib/b.go"}, "3": {"web/c.go"}}, "1", "2", "3")
	rep, err := h.eng.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Validations)
	assert.Equal(t, []string{"1", "2", "3"}, h.prov.merged)
}

func TestOverlappingChangesStackAndBisectOnFailure(t *testing.T) {
	paths := map[string][]string{"1": {"app/a"}, "2": {"app/b"}, "3": {"app/c"}, "4": {"app/d"}}
	h := newHarness(paths, "1", "2", "3", "4")
	h.val.bad["3"] = true
	rep, err := h.eng.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]Outcome{
		"1": OutcomeMerged, "2": OutcomeMerged, "3": OutcomeKicked, "4": OutcomeMerged,
	}, outcomes(rep))
	assert.Equal(t, []string{"1", "2", "4"}, h.prov.merged)
	// Whole stack red; left half green and landed; the right half IS the red stack now,
	// so it bisects without re-running; #3 alone red; #4 alone green.
	assert.Equal(t, []string{"stage:1,2,3,4", "stage:1,2", "stage:3", "stage:4"}, h.val.runs)
}

func TestApprovalIsRecheckedAtTheTestedCommitBeforeMerging(t *testing.T) {
	h := newHarness(map[string][]string{"1": {"app/a.go"}}, "1")
	h.prov.approval = func(c Change, _ string, call int) Approval {
		// Approved when admitted, withdrawn by the time the queue would merge.
		return Approval{Approved: call == 1, Head: c.Head, Reason: "changes requested"}
	}
	rep, err := h.eng.Run(context.Background())
	require.NoError(t, err)
	assert.Empty(t, h.prov.merged)
	assert.Equal(t, OutcomeWaiting, outcomes(rep)["1"])
	assert.Equal(t, []string{"sha-1", "sha-1"}, h.prov.asked, "both checks name the tested commit")
	assert.NotContains(t, h.prov.statuses, posted{"1", "sha-1", StateSuccess})
}

func TestUnapprovedChangeWaitsWithoutValidating(t *testing.T) {
	h := newHarness(map[string][]string{"1": {"app/a.go"}}, "1")
	h.prov.approval = func(c Change, _ string, _ int) Approval {
		return Approval{Head: c.Head, Required: 1, Reason: "0 of 1 approvals"}
	}
	rep, err := h.eng.Run(context.Background())
	require.NoError(t, err)
	assert.Zero(t, rep.Validations)
	assert.Equal(t, OutcomeWaiting, outcomes(rep)["1"])
	assert.Equal(t, []posted{{"1", "sha-1", StatePending}}, h.prov.statuses)
}

func TestHeadPushedSinceListingIsLeftForTheNextRun(t *testing.T) {
	h := newHarness(map[string][]string{"1": {"app/a.go"}}, "1")
	h.prov.approval = func(Change, string, int) Approval { return Approval{Approved: true, Head: "sha-new"} }
	rep, err := h.eng.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, OutcomeWaiting, outcomes(rep)["1"])
	assert.Empty(t, h.prov.statuses, "no status on a commit that is no longer the head")
}

func TestAChangeConflictingWithOneAheadWaitsForIt(t *testing.T) {
	h := newHarness(map[string][]string{"1": {"app/a.go"}, "2": {"app/a.go"}}, "1", "2")
	h.w.conflicts[[2]string{"1", "2"}] = []string{"app/a.go"}
	rep, err := h.eng.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]Outcome{"1": OutcomeMerged, "2": OutcomeWaiting}, outcomes(rep))
	i := slices.IndexFunc(rep.Results, func(r Result) bool { return r.Change.ID == "2" })
	require.GreaterOrEqual(t, i, 0)
	assert.Contains(t, rep.Results[i].Reason, "#1")
}

func TestAMergeThatLandsAnotherTreeStopsTheQueue(t *testing.T) {
	h := newHarness(map[string][]string{"1": {"app/a"}, "2": {"app/b"}}, "1", "2")
	h.prov.rewrite = true
	_, err := h.eng.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not carry the tree the queue validated")
	assert.Equal(t, []string{"1"}, h.prov.merged, "nothing builds on an unvalidated base")
}

func TestDryRunPlansAndTouchesNothing(t *testing.T) {
	h := newHarness(map[string][]string{"1": {"app/a"}, "2": {"lib/b"}}, "1", "2")
	h.eng.DryRun = true
	rep, err := h.eng.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"1"}, {"2"}}, rep.Groups)
	assert.Empty(t, h.prov.statuses)
	assert.Empty(t, h.repo.stages)
	assert.Empty(t, h.prov.merged)
}

func TestAnEngineMissingAPartIsAnError(t *testing.T) {
	_, err := (&Engine{Base: "main"}).Run(context.Background())
	require.EqualError(t, err, "queue: engine is missing Provider, Repo, Graph, Validator")
}
