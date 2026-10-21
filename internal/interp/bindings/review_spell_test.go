package bindings

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// withReviewSpell registers a review provider backed by src and selects it, restoring the
// selection afterwards. The name is derived from the test because the registry panics on a
// duplicate and every case here needs its own answer.
func withReviewSpell(t *testing.T, src string) {
	t.Helper()
	name := register.Spell(t, "fake-review", spells.WithFuncSource(`import "magus/spell";
`+src))
	prev := ReviewProvider()
	SetReviewProvider(name)
	t.Cleanup(func() { SetReviewProvider(prev) })
}

// openReview answers find_review with review 482 on acme/acme.
const openReview = `
export fun find_review(target: Target, req: FindReviewRequest) > ReviewTarget {
    return ReviewTarget{ id = "482", repo = "acme/acme" };
}
`

func TestFindReviewDecodesTheTargetAndCarriesTheRepo(t *testing.T) {
	withReviewSpell(t, openReview)
	at := FindReview(context.Background(), "feat/x", "git@github.com:acme/acme.git")
	assert.True(t, at.Open())
	assert.Equal(t, types.ReviewTarget{ID: "482", Repo: "acme/acme"}, at)
}

func TestFindReviewHandsTheBranchAndRemoteThrough(t *testing.T) {
	withReviewSpell(t, `
export fun find_review(target: Target, req: FindReviewRequest) > ReviewTarget {
    return ReviewTarget{ id = "1", repo = req.branch + "@" + req.remote };
}`)
	at := FindReview(context.Background(), "feat/x", "origin")
	assert.Equal(t, "feat/x@origin", at.Repo)
}

// A mistyped field becomes a REASON, never a zero target: those two render identically to the
// reader and mean opposite things. Silently emptying the id would say "no pull request for this
// branch", sending them to look at their branch when the fault is in their spell.
func TestFindReviewReportsAMalformedAnswerRatherThanReadingItAsNoReview(t *testing.T) {
	withReviewSpell(t, `
export fun find_review(target: Target, req: FindReviewRequest) > any {
    return {"id": 482};
}`)
	at := FindReview(context.Background(), "feat/x", "")
	assert.False(t, at.Open())
	assert.Contains(t, at.Reason, "want str")
	assert.Contains(t, at.Reason, "find_review")
}

// A spell that fails outright is the same answer as a branch with no review: a reason, not an
// error. A diff that refused to open because a lookup failed would be a worse tool.
func TestFindReviewTurnsASpellFailureIntoAReason(t *testing.T) {
	withReviewSpell(t, `
export fun find_review(target: Target, req: FindReviewRequest) > ReviewTarget !> any {
    throw "boom";
}`)
	at := FindReview(context.Background(), "feat/x", "")
	assert.False(t, at.Open())
	assert.Contains(t, at.Reason, "boom")
}

func TestFindReviewNamesAProviderThatDoesNotExportIt(t *testing.T) {
	withReviewSpell(t, `export fun mgs_getName() > str { return "x"; }`)
	at := FindReview(context.Background(), "feat/x", "")
	assert.False(t, at.Open())
	assert.Contains(t, at.Reason, "find_review")
}

func TestReviewCommentsDecodesEveryFieldAndDefaultsTheHunkToUnplaced(t *testing.T) {
	withReviewSpell(t, `
export fun review_threads(target: Target, req: ReviewRef) > [ReviewComment] {
    return [ReviewComment{ id = "t1", path = "a.go", line = 12, author = "priya", body = "why" }];
}`)
	got, err := ReviewComments(context.Background(), types.ReviewTarget{ID: "482"})
	require.NoError(t, err)
	// -1, not 0. The zero value is a VALID hunk index, so a thread nothing placed would
	// otherwise render against the first hunk of its file: the wrong code, stated confidently.
	assert.Equal(t, []types.ReviewComment{{
		ID: "t1", Path: "a.go", Line: 12, Hunk: -1, Author: "priya", Body: "why",
	}}, got)
}

func TestReviewCommentsDecodesReplyOutdatedAndHunk(t *testing.T) {
	withReviewSpell(t, `
export fun review_threads(target: Target, req: ReviewRef) > [ReviewComment] {
    return [ReviewComment{
        id = "t2", root = "t1", path = "a.go", author = "eli", body = "because",
        outdated = true, diffHunk = "@@ -1,2 +1,3 @@",
    }];
}`)
	got, err := ReviewComments(context.Background(), types.ReviewTarget{ID: "482"})
	require.NoError(t, err)
	assert.Equal(t, []types.ReviewComment{{
		ID: "t2", Root: "t1", Path: "a.go", Hunk: -1, Author: "eli", Body: "because",
		Outdated: true, DiffHunk: "@@ -1,2 +1,3 @@",
	}}, got)
}

func TestReviewCommentsHandsTheReviewThrough(t *testing.T) {
	withReviewSpell(t, `
export fun review_threads(target: Target, req: ReviewRef) > [ReviewComment] {
    return [ReviewComment{ id = req.repo + "#" + req.id }];
}`)
	got, err := ReviewComments(context.Background(), types.ReviewTarget{ID: "482", Repo: "acme/acme"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "acme/acme#482", got[0].ID)
}

// The list decodes strictly: one malformed row fails the whole answer, loudly and by path,
// rather than a provider bug passing as a review with fewer comments on it.
func TestReviewCommentsFailsLoudlyOnAMalformedRow(t *testing.T) {
	withReviewSpell(t, `
export fun review_threads(target: Target, req: ReviewRef) > any {
    return [{"id": "t1", "path": "a.go", "body": "readable"}, {"id": "t2", "line": "not a number"}];
}`)
	got, err := ReviewComments(context.Background(), types.ReviewTarget{ID: "482"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "review_threads")
	assert.ErrorContains(t, err, "[1].line")
	assert.Empty(t, got)
}

func TestReviewCommentsTreatsAMissingOpAsNoComments(t *testing.T) {
	withReviewSpell(t, openReview)
	got, err := ReviewComments(context.Background(), types.ReviewTarget{ID: "482"})
	require.NoError(t, err)
	assert.Empty(t, got)
}

// The unreachable host is the fact ReviewComments swallows on purpose, and a caller that reports
// a COUNT rather than rendering the comments cannot afford to lose it: an empty review and a
// review nobody could read produce the same number. Reported here so the count can refuse to be
// taken.
func TestReviewCommentsReachedTellsAnUnreachableHostFromAnEmptyReview(t *testing.T) {
	withReviewSpell(t, `
export fun review_threads(target: Target, req: ReviewRef) > [ReviewComment] !> any {
    throw "dial: connection refused";
}`)
	comments, reached, err := ReviewCommentsReached(context.Background(), types.ReviewTarget{ID: "482"})
	require.NoError(t, err, "an unreachable host is still not this call's failure")
	assert.Empty(t, comments)
	assert.False(t, reached)

	withReviewSpell(t, `
export fun review_threads(target: Target, req: ReviewRef) > [ReviewComment] { return [<ReviewComment>]; }`)
	_, reached, err = ReviewCommentsReached(context.Background(), types.ReviewTarget{ID: "482"})
	require.NoError(t, err)
	assert.True(t, reached, "a review with no threads is a host that answered")

	// A malformed remark is a host that answered badly, not one that failed to answer. Reading it
	// as unreachable is what made a merge with one bad thread report nothing at all.
	withReviewSpell(t, `
export fun review_threads(target: Target, req: ReviewRef) > any { return [{"id": "t2", "line": "not a number"}]; }`)
	_, reached, err = ReviewCommentsReached(context.Background(), types.ReviewTarget{ID: "482"})
	require.Error(t, err)
	assert.True(t, reached)
}

// OriginReviewComments is the lookup every transport makes: the review for a branch, then its
// comments, and a review that is not there says why instead of reading as an empty one.
func TestOriginReviewCommentsFindsTheReviewThenReadsItsComments(t *testing.T) {
	withReviewSpell(t, openReview+`
export fun review_threads(target: Target, req: ReviewRef) > [ReviewComment] {
    return [ReviewComment{ id = "t1", path = "a.go", body = "why" }];
}`)

	at, comments, err := OriginReviewComments(t.Context(), types.ReviewOrigin{Branch: "feat/x"}, time.Minute)

	require.NoError(t, err)
	assert.Equal(t, types.ReviewTarget{ID: "482", Repo: "acme/acme"}, at)
	require.Len(t, comments, 1)
	assert.Equal(t, "t1", comments[0].ID)

	// review_threads throws if it is asked, so a comment list here means it was.
	withReviewSpell(t, `
export fun find_review(target: Target, req: FindReviewRequest) > ReviewTarget !> any {
    throw "boom";
}
export fun review_threads(target: Target, req: ReviewRef) > [ReviewComment] {
    return [ReviewComment{ id = "asked" }];
}`)
	at, comments, err = OriginReviewComments(t.Context(), types.ReviewOrigin{Branch: "feat/x"}, time.Minute)
	require.NoError(t, err)
	assert.False(t, at.Open())
	assert.Contains(t, at.Reason, "boom")
	assert.Empty(t, comments, "no comments are asked for when no review is open")
}

// A closed target consults no provider at all: there is nothing to ask about.
func TestReviewCommentsAsksNothingWithoutAnOpenReview(t *testing.T) {
	withReviewSpell(t, `
export fun review_threads(target: Target, req: ReviewRef) > [ReviewComment] {
    return [ReviewComment{ id = "asked" }];
}`)
	got, err := ReviewComments(context.Background(), types.ReviewTarget{})
	require.NoError(t, err)
	assert.Empty(t, got, "a closed target must not reach the provider")
}

// The spell accepts only the exact request it should have been sent, so a nil error proves
// what crossed: the review, the summary and each draft's path, line and body.
func TestPublishReviewCarriesTheDrafts(t *testing.T) {
	withReviewSpell(t, `
export fun publish_review(target: Target, req: PublishReviewRequest) > Ack {
    return Ack{ ok = req.repo == "acme/acme" and req.id == "482" and req.summary == "pass"
        and req.drafts.len() == 1 and req.drafts[0].path == "a.go"
        and req.drafts[0].line == 4 and req.drafts[0].body == "why" };
}`)
	_, err := PublishReview(context.Background(), types.ReviewTarget{ID: "482", Repo: "acme/acme"}, "pass",
		types.VerdictComment, []types.DiffComment{{Path: "a.go", Line: 4, Body: "why"}})
	require.NoError(t, err)
}

func TestPublishReviewReportsARefusal(t *testing.T) {
	withReviewSpell(t, `
export fun publish_review(target: Target, req: PublishReviewRequest) > Ack { return Ack{}; }`)
	_, err := PublishReview(context.Background(), types.ReviewTarget{ID: "482"}, "pass", types.VerdictComment, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "publish_review")
}

// State is optional, and a spell that says nothing about it reads as open. Merged is separate
// from Open on purpose: a review that landed still has a conversation worth reading.
func TestFindReviewCarriesTheStateAndTreatsSilenceAsOpen(t *testing.T) {
	withReviewSpell(t, `
export fun find_review(target: Target, req: FindReviewRequest) > ReviewTarget {
    return ReviewTarget{ id = "482", repo = "acme/acme", state = "merged" };
}`)
	merged := FindReview(context.Background(), "feat/x", "")
	assert.True(t, merged.Open(), "a merged review is still readable")
	assert.True(t, merged.Merged())

	withReviewSpell(t, openReview)
	quiet := FindReview(context.Background(), "feat/x", "")
	assert.True(t, quiet.Open())
	assert.False(t, quiet.Merged(), "silence about state is not a claim that it merged")
}

// The handler marks every draft published on a nil error, and publish only ever considers
// unpublished drafts, so a provider that cannot publish has to refuse or the remarks are lost.
func TestPublishReviewRefusesAProviderThatCannotPublish(t *testing.T) {
	withReviewSpell(t, openReview)

	_, err := PublishReview(context.Background(), types.ReviewTarget{ID: "482", Repo: "acme/acme"}, "pass",
		types.VerdictComment, []types.DiffComment{{Path: "a.go", Line: 4, Body: "why"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), spells.PublishReviewContract)
}

func TestPublishAndReplyRefuseWithNoProviderWired(t *testing.T) {
	prev := ReviewProvider()
	SetReviewProvider("")
	t.Cleanup(func() { SetReviewProvider(prev) })

	_, perr := PublishReview(context.Background(), types.ReviewTarget{ID: "1"}, "", types.VerdictComment, nil)
	assert.ErrorIs(t, perr, errNoReviewProvider)
	assert.ErrorIs(t, ReplyReview(context.Background(), types.ReviewTarget{ID: "1"}, "t1", "ok"), errNoReviewProvider)
}

// A spell that answers false is a REFUSAL, and one that exports no reply_review cannot have
// sent it. Reporting success on either is how a reply nobody sent gets treated as delivered.
func TestReplyReviewTreatsAnythingButAcceptanceAsARefusal(t *testing.T) {
	withReviewSpell(t, `
export fun reply_review(target: Target, req: ReplyReviewRequest) > Ack {
    return Ack{ ok = req.repo == "acme/acme" and req.id == "482" and req.thread == "t1" and req.body == "agreed" };
}`)
	require.NoError(t, ReplyReview(context.Background(), types.ReviewTarget{ID: "482", Repo: "acme/acme"}, "t1", "agreed"))

	err := ReplyReview(context.Background(), types.ReviewTarget{ID: "482", Repo: "acme/acme"}, "t1", "not this")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "t1", "the error names the thread that did not take it")

	withReviewSpell(t, openReview)
	err = ReplyReview(context.Background(), types.ReviewTarget{ID: "482"}, "t1", "agreed")
	require.Error(t, err)
	assert.Contains(t, err.Error(), spells.ReplyReviewContract)
}

// The credential owner is asked for ONCE per process and reused after.
//
// Naming it costs a second round trip on every host magus ships for, and this sits under opening
// a diff, where latency is the product. The cache is what keeps a "whose review is this" question
// off that path; the flag is what lets the spell skip the call it would otherwise always make.
func TestFindReviewAsksWhoTheCredentialIsOnlyOnce(t *testing.T) {
	// The cache is process-global, so a run under -count=2 (or any later test that answers with
	// a viewer) would otherwise start this one with the cache already warm and see the first
	// call skip the ask. Establish the state rather than assuming it.
	rememberViewer("")
	t.Cleanup(func() { rememberViewer("") })

	// The spell reports in reason whether it was asked, and names the viewer only then.
	withReviewSpell(t, `
export fun find_review(target: Target, req: FindReviewRequest) > ReviewTarget {
    return ReviewTarget{
        id = "482", author = "priya",
        viewer = if (req.wantViewer) "eli" else "",
        reason = if (req.wantViewer) "asked" else "not asked",
    };
}`)

	first := FindReview(context.Background(), "feat/x", "")
	assert.Equal(t, types.ReviewTarget{ID: "482", Author: "priya", Viewer: "eli", Reason: "asked"}, first)
	opened, known := first.OpenedByViewer()
	assert.True(t, known, "both names present means the question is answerable")
	assert.False(t, opened, "priya's review is not eli's")

	// Asked once, then never again, and the cached owner rides along on every later answer.
	second := FindReview(context.Background(), "feat/x", "")
	assert.Equal(t, types.ReviewTarget{ID: "482", Author: "priya", Viewer: "eli", Reason: "not asked"}, second)
}

// A provider that names neither party leaves the question UNANSWERED, which is not the same as
// answering no: guessing "yours" would refuse a legitimate approval on a colleague's change, and
// guessing "theirs" would let a change approve itself.
func TestOpenedByViewerIsUnknownWhenEitherNameIsMissing(t *testing.T) {
	for _, at := range []types.ReviewTarget{
		{ID: "1"},
		{ID: "1", Author: "priya"},
		{ID: "1", Viewer: "eli"},
	} {
		opened, known := at.OpenedByViewer()
		assert.False(t, known, "%#v must read as unknown", at)
		assert.False(t, opened)
	}
}

// TestPublishSendsTheResolvedVerdictNotTheRequestedOne. The resolver could be perfectly correct
// and the feature still broken, if the requested verdict were the one that travelled, so the
// spell accepts only the verdict it should have received.
func TestPublishSendsTheResolvedVerdictNotTheRequestedOne(t *testing.T) {
	accepting := func(verdict types.ReviewVerdict) {
		withReviewSpell(t, `
export fun publish_review(target: Target, req: PublishReviewRequest) > Ack {
    return Ack{ ok = req.verdict == "`+string(verdict)+`" };
}`)
	}

	accepting(types.VerdictComment)
	published, err := PublishReview(context.Background(), types.ReviewTarget{ID: "1", Author: "ada", Viewer: "ada"}, "s",
		types.VerdictApprove, []types.DiffComment{{Path: "a.go", Line: 4, Body: "why"}})
	require.NoError(t, err, "a change must not approve itself at the wire")
	assert.Equal(t, types.VerdictComment, published, "and the caller is told what actually went")

	accepting(types.VerdictApprove)
	published, err = PublishReview(context.Background(), types.ReviewTarget{ID: "1", Author: "grace", Viewer: "ada"}, "s",
		types.VerdictApprove, []types.DiffComment{{Path: "a.go", Line: 4, Body: "why"}})
	require.NoError(t, err, "a colleague's change may be approved")
	assert.Equal(t, types.VerdictApprove, published)
}
