package queue

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/internal/queue/types/gen/mocks"
)

// reviewing is a provider that reads reviews and dismisses them: each read answers the
// next of reads, the last one repeating, and each dismissal is kept as "<id>: <message>".
type reviewing struct {
	*mocks.MockProvider
	reads     []types.Reviews
	nreads    int
	dismissed []string
}

func (r *reviewing) Reviews(context.Context, types.Change) (types.Reviews, error) {
	got := r.reads[min(r.nreads, len(r.reads)-1)]
	r.nreads++
	return got, nil
}

func (r *reviewing) DismissReview(_ context.Context, _ types.Change, rv types.Review, message string) error {
	r.dismissed = append(r.dismissed, rv.ID+": "+message)
	return nil
}

// reviewsOf sets up the doubles for classifying the approvals on c, listed alone on main,
// whose head forked from carryNewBase: the listing, the base, the head and its fork
// point, then cc for the approved carryOld.
func reviewsOf(t *testing.T, c types.Change, cc carryCase, reads ...types.Reviews) (doubles, *reviewing) {
	d := newDoubles(t)
	d.provider.EXPECT().ListChanges(mock.Anything, types.ListQuery{Base: "main"}).Return(types.Changes{Base: "main", Changes: []types.Change{c}}, nil)
	d.tip(base)
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, c.Head).Return(nil)
	d.plain(c.Head)
	d.carry(c, cc)
	return d, &reviewing{MockProvider: d.provider, reads: reads}
}

func approvedBy(reviewer, id, commit string) types.Review {
	return types.Review{ID: id, Reviewer: reviewer, Commit: commit}
}

const codeWhy = "differs beyond comments from the revision compared against"

var codeEdit = carryCase{edits: map[string]carryEdit{"a.go": {[]byte("package a\n\nfunc A() int { return 1 }\n"), []byte("package a\n\nfunc A() int { return 2 }\n")}}}

// An approval at an older commit is classified as plan classifies it; one given at the
// head needs no verdict, and a carried one is left standing even with dismissal on.
func TestReviewsCarriesWhatThePolicyAllowsAndDismissesNothing(t *testing.T) {
	c := change("1", "a")
	cc := carryCase{edits: map[string]carryEdit{"changes/unreleased/a.md": {nil, []byte("# a\n")}}}
	d, prov := reviewsOf(t, c, cc, types.Reviews{Head: c.Head, Approving: []types.Review{approvedBy("ann", "12", carryOld), approvedBy("bob", "13", c.Head)}})

	got, err := Reviews(t.Context(), d.vcs, d.facts, clone, prov, ReviewsQuery{Base: "main", Change: "1", Dismiss: true}, types.DefaultCarryPolicy())
	require.NoError(t, err)
	const why = `matches "**/*.md" (built-in default)`
	assert.Equal(t, types.ReviewReport{Change: "1", Head: c.Head, Reviews: []types.ReviewVerdict{{
		Reviewer: "ann", ReviewID: "12", Commit: carryOld, Carry: true, Tier: types.CarryProse,
		Changed: []types.ClassifiedPath{{Path: "changes/unreleased/a.md", Tier: types.CarryProse, Why: why}},
		Reason:  "head " + c.Head[:12] + " is " + carryOld[:12] + " rebased; changed since the approval: changes/unreleased/a.md (prose: " + why + ")",
	}}}, got)
	assert.Empty(t, prov.dismissed)
	assert.Equal(t, 1, prov.nreads, "nothing to dismiss reads the head once")

	var out bytes.Buffer
	require.NoError(t, WriteReviews(&out, got))
	assert.JSONEq(t, `{"schema":"mergequeue.reviews/v1","change":"1","head":"`+c.Head+`","reviews":[{"reviewer":"ann","review_id":"12","commit":"`+carryOld+`",`+
		`"carry":true,"tier":"prose","changed":[{"path":"changes/unreleased/a.md","tier":"prose","why":"matches \"**/*.md\" (built-in default)"}],`+
		`"reason":"head `+c.Head[:12]+` is `+carryOld[:12]+` rebased; changed since the approval: changes/unreleased/a.md (prose: matches \"**/*.md\" (built-in default))","dismissed":false}]}`, out.String())
}

// Two approvals at one commit are one classification, and each is dismissed with a
// message naming the files that kept it from carrying.
func TestReviewsDismissesWhatDoesNotCarryNamingTheFiles(t *testing.T) {
	c := change("1", "a")
	d, prov := reviewsOf(t, c, codeEdit, types.Reviews{Head: c.Head, Approving: []types.Review{approvedBy("ann", "12", carryOld), approvedBy("cy", "14", carryOld)}})

	got, err := Reviews(t.Context(), d.vcs, d.facts, clone, prov, ReviewsQuery{Base: "main", Change: "1", Head: c.Head, Dismiss: true}, types.DefaultCarryPolicy())
	require.NoError(t, err)
	reason := "changed since the approval at " + carryOld[:12] + ": a.go (code: " + codeWhy + ")"
	want := types.ReviewVerdict{Commit: carryOld, Tier: types.CarryCode, Changed: []types.ClassifiedPath{{Path: "a.go", Tier: types.CarryCode, Why: codeWhy}},
		Reason: reason, Dismissed: true}
	ann, cy := want, want
	ann.Reviewer, ann.ReviewID, cy.Reviewer, cy.ReviewID = "ann", "12", "cy", "14"
	assert.Equal(t, []types.ReviewVerdict{ann, cy}, got.Reviews)
	assert.Equal(t, []string{"12: magus queue: " + reason + ". Review the head again.", "14: magus queue: " + reason + ". Review the head again."}, prov.dismissed)
	assert.Equal(t, 2, prov.nreads, "the head is read again before dismissing")
	d.vcs.AssertNumberOfCalls(t, "MergeTrees", 1)

	var out bytes.Buffer
	require.NoError(t, WriteReviewLines(&out, got))
	assert.Equal(t, "approval by @ann at "+carryOld[:12]+" dismissed (code): "+reason+"\n"+
		"approval by @cy at "+carryOld[:12]+" dismissed (code): "+reason+"\n", out.String())
}

// Only an approval still standing when the head is read again is dismissed: one gone
// since, dismissed by hand or replaced, needs nothing.
func TestReviewsLeavesAnApprovalAlreadyDismissed(t *testing.T) {
	c := change("1", "a")
	first := types.Reviews{Head: c.Head, Approving: []types.Review{approvedBy("ann", "12", carryOld), approvedBy("cy", "14", carryOld)}}
	d, prov := reviewsOf(t, c, codeEdit, first, types.Reviews{Head: c.Head, Approving: []types.Review{approvedBy("cy", "14", carryOld)}})

	got, err := Reviews(t.Context(), d.vcs, d.facts, clone, prov, ReviewsQuery{Base: "main", Change: "1", Dismiss: true}, types.DefaultCarryPolicy())
	require.NoError(t, err)
	require.Len(t, got.Reviews, 2)
	assert.False(t, got.Reviews[0].Dismissed, "ann's approval was gone by the second read")
	assert.True(t, got.Reviews[1].Dismissed)
	require.Len(t, prov.dismissed, 1)
	assert.Contains(t, prov.dismissed[0], "14: ")
}

// A head that moved between the classification and the dismissal was not what was
// classified, so nothing is dismissed: the run on the new head decides.
func TestReviewsDismissesNothingWhenTheHeadMoved(t *testing.T) {
	c := change("1", "a")
	approving := []types.Review{approvedBy("ann", "12", carryOld)}
	d, prov := reviewsOf(t, c, codeEdit, types.Reviews{Head: c.Head, Approving: approving}, types.Reviews{Head: head("2"), Approving: approving})

	got, err := Reviews(t.Context(), d.vcs, d.facts, clone, prov, ReviewsQuery{Base: "main", Change: "1", Dismiss: true}, types.DefaultCarryPolicy())
	require.NoError(t, err)
	assert.Equal(t, head("2"), got.Moved)
	require.Len(t, got.Reviews, 1)
	assert.False(t, got.Reviews[0].Carry)
	assert.False(t, got.Reviews[0].Dismissed)
	assert.Empty(t, prov.dismissed)
}

// Without --dismiss it only reads, and a caller naming a head the change has left reads
// nothing past the reviews.
func TestReviewsWithoutDismissOrOnAnotherHeadOnlyReads(t *testing.T) {
	c := change("1", "a")
	approving := []types.Review{approvedBy("ann", "12", carryOld)}
	d, prov := reviewsOf(t, c, codeEdit, types.Reviews{Head: c.Head, Approving: approving})

	got, err := Reviews(t.Context(), d.vcs, d.facts, clone, prov, ReviewsQuery{Base: "main", Change: "1"}, types.DefaultCarryPolicy())
	require.NoError(t, err)
	require.Len(t, got.Reviews, 1)
	assert.False(t, got.Reviews[0].Carry)
	assert.Empty(t, prov.dismissed)
	assert.Equal(t, 1, prov.nreads)

	moved := newDoubles(t)
	moved.provider.EXPECT().ListChanges(mock.Anything, types.ListQuery{Base: "main"}).Return(types.Changes{Base: "main", Changes: []types.Change{c}}, nil)
	stale := &reviewing{MockProvider: moved.provider, reads: []types.Reviews{{Head: c.Head, Approving: approving}}}
	got, err = Reviews(t.Context(), moved.vcs, moved.facts, clone, stale, ReviewsQuery{Base: "main", Change: "1", Head: head("0"), Dismiss: true}, nil)
	require.NoError(t, err)
	assert.Equal(t, types.ReviewReport{Change: "1", Head: c.Head, Moved: c.Head, Reviews: []types.ReviewVerdict{}}, got)
}

func TestReviewsRefusesAnUnlistedChangeAndAProviderReadingNoReviews(t *testing.T) {
	d := newDoubles(t)
	d.provider.EXPECT().ListChanges(mock.Anything, types.ListQuery{Base: "main"}).Return(types.Changes{Base: "main"}, nil)
	_, err := Reviews(t.Context(), d.vcs, d.facts, clone, &reviewing{MockProvider: d.provider}, ReviewsQuery{Base: "main", Change: "9"}, nil)
	require.EqualError(t, err, "#9 is not an open change against main")

	_, err = Reviews(t.Context(), d.vcs, d.facts, clone, d.provider, ReviewsQuery{Base: "main", Change: "9"}, nil)
	require.EqualError(t, err, "the provider reads no reviews")
}

// A change carrying no merge intent is the one most often under review; it is found
// among the unqueued changes, and every other change stays in the listing the delta
// bases are decided from.
func TestTakeChangeFindsAQueuedOrUnqueuedChange(t *testing.T) {
	queued, other := change("1"), change("2")
	listing := types.Changes{Base: "main", Changes: []types.Change{queued, other},
		Unqueued: []types.UnqueuedChange{{ID: "3", Repo: "acme/acme", Head: head("3"), Branch: "wip", Author: "ann"}}}

	got, rest, ok := takeChange(listing, "1")
	require.True(t, ok)
	assert.Equal(t, queued, got)
	assert.Equal(t, []types.Change{other}, rest.Changes)
	assert.Len(t, rest.Unqueued, 1)

	got, rest, ok = takeChange(listing, "3")
	require.True(t, ok)
	assert.Equal(t, types.Change{ID: "3", Repo: "acme/acme", Head: head("3"), Branch: "wip", Base: "main", Author: "ann"}, got)
	assert.Len(t, rest.Changes, 2)
	assert.Empty(t, rest.Unqueued)

	_, _, ok = takeChange(listing, "4")
	assert.False(t, ok)
}
