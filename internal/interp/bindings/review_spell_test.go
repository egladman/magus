package bindings

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// withReviewSpell registers a fake provider and selects it, restoring the selection afterwards.
//
// The name is derived from the test and a counter because the registry panics on a duplicate
// and every case here needs its own answer. `answer` receives the op, so one provider can
// succeed on a lookup and refuse a publish - which no real spell can be made to do on demand.
func withReviewSpell(t *testing.T, answer func(op string) (any, error)) {
	t.Helper()
	fakeSpellSeq++
	name := fmt.Sprintf("fake-review-%s-%d", t.Name(), fakeSpellSeq)
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(name,
		spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
			return answer(req.Target)
		})))
	prev := ReviewProvider()
	SetReviewProvider(name)
	t.Cleanup(func() { SetReviewProvider(prev) })
}

// fakeSpellSeq keeps two registrations inside one test distinct.
var fakeSpellSeq int

func TestFindReviewDecodesTheTargetAndCarriesTheRepo(t *testing.T) {
	withReviewSpell(t, func(string) (any, error) {
		return map[string]any{"id": "482", "repo": "acme/acme"}, nil
	})
	at := FindReview(context.Background(), "feat/x", "git@github.com:acme/acme.git")
	assert.True(t, at.Open())
	assert.Equal(t, "482", at.ID)
	assert.Equal(t, "acme/acme", at.Repo)
}

// A mistyped field becomes a REASON, never a zero target: those two render identically to the
// reader and mean opposite things. Silently emptying the id would say "no pull request for this
// branch", sending them to look at their branch when the fault is in their spell.
func TestFindReviewReportsAMalformedAnswerRatherThanReadingItAsNoReview(t *testing.T) {
	withReviewSpell(t, func(string) (any, error) {
		return map[string]any{"id": 482}, nil
	})
	at := FindReview(context.Background(), "feat/x", "")
	assert.False(t, at.Open())
	assert.Contains(t, at.Reason, "want str")
	assert.Contains(t, at.Reason, "find_review")
}

// A spell that fails outright is the same answer as a branch with no review: a reason, not an
// error. A diff that refused to open because a lookup failed would be a worse tool.
func TestFindReviewTurnsASpellFailureIntoAReason(t *testing.T) {
	withReviewSpell(t, func(string) (any, error) { return nil, errors.New("boom") })
	at := FindReview(context.Background(), "feat/x", "")
	assert.False(t, at.Open())
	assert.Contains(t, at.Reason, "boom")
}

func TestReviewThreadsDecodesEveryFieldAndDefaultsTheHunkToUnplaced(t *testing.T) {
	withReviewSpell(t, func(string) (any, error) {
		return []any{map[string]any{
			"id": "t1", "path": "a.go", "line": float64(12),
			"author": "priya", "body": "why",
		}}, nil
	})
	got, err := ReviewThreads(context.Background(), types.ReviewTarget{ID: "482"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, types.ReviewThread{
		ID: "t1", Path: "a.go", Line: 12, Hunk: -1, Author: "priya", Body: "why",
	}, got[0])
	// -1, not 0. The zero value is a VALID hunk index, so a thread nothing placed would
	// otherwise render against the first hunk of its file - the wrong code, stated confidently.
	assert.Equal(t, -1, got[0].Hunk)
}

// Absent reads as the zero value, not as an error: the posture spell_decode.go states for this
// whole layer. A review with no threads is not a malformed provider.
func TestReviewThreadsTreatsNothingAsNoThreads(t *testing.T) {
	withReviewSpell(t, func(string) (any, error) { return nil, nil })
	got, err := ReviewThreads(context.Background(), types.ReviewTarget{ID: "482"})
	require.NoError(t, err)
	assert.Empty(t, got)
}

// A malformed thread does not take the readable ones down with it. Returning only the error
// would leave the surface saying a colleague said nothing, which is the worst thing it can say.
func TestReviewThreadsReturnsWhatItReadAlongsideTheReason(t *testing.T) {
	withReviewSpell(t, func(string) (any, error) {
		return []any{
			map[string]any{"id": "t1", "path": "a.go", "body": "readable"},
			map[string]any{"id": "t2", "line": "not a number"},
		}, nil
	})
	got, err := ReviewThreads(context.Background(), types.ReviewTarget{ID: "482"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "want int")
	require.Len(t, got, 1, "the thread that decoded still travels")
	assert.Equal(t, "readable", got[0].Body)
}

// The unreachable host is the fact ReviewThreads swallows on purpose, and a caller that reports a
// COUNT rather than rendering the threads cannot afford to lose it: an empty review and a review
// nobody could read produce the same number. Reported here so the count can refuse to be taken.
func TestReviewThreadsReachedTellsAnUnreachableHostFromAnEmptyReview(t *testing.T) {
	withReviewSpell(t, func(string) (any, error) { return nil, errors.New("dial: connection refused") })
	threads, reached, err := ReviewThreadsReached(context.Background(), types.ReviewTarget{ID: "482"})
	require.NoError(t, err, "an unreachable host is still not this call's failure")
	assert.Empty(t, threads)
	assert.False(t, reached)

	withReviewSpell(t, func(string) (any, error) { return []any{}, nil })
	_, reached, err = ReviewThreadsReached(context.Background(), types.ReviewTarget{ID: "482"})
	require.NoError(t, err)
	assert.True(t, reached, "a review with no threads is a host that answered")

	// A malformed remark is a host that answered badly, not one that failed to answer. Reading it
	// as unreachable is what made a merge with one bad thread report nothing at all.
	withReviewSpell(t, func(string) (any, error) {
		return []any{map[string]any{"id": "t2", "line": "not a number"}}, nil
	})
	_, reached, err = ReviewThreadsReached(context.Background(), types.ReviewTarget{ID: "482"})
	require.Error(t, err)
	assert.True(t, reached)
}

// A closed target consults no provider at all: there is nothing to ask about.
func TestReviewThreadsAsksNothingWithoutAnOpenReview(t *testing.T) {
	asked := false
	withReviewSpell(t, func(string) (any, error) { asked = true; return nil, nil })
	got, err := ReviewThreads(context.Background(), types.ReviewTarget{})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.False(t, asked, "a closed target must not reach the provider")
}

func TestPublishReviewCarriesTheDraftsAndFailsLoudly(t *testing.T) {
	var sent []any
	fakeSpellSeq++
	name := fmt.Sprintf("fake-capture-%d", fakeSpellSeq)
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(name,
		spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
			sent, _ = req.Params["drafts"].([]any)
			return map[string]any{}, nil
		})))
	prev := ReviewProvider()
	SetReviewProvider(name)
	t.Cleanup(func() { SetReviewProvider(prev) })

	_, err := PublishReview(context.Background(), types.ReviewTarget{ID: "482", Repo: "acme/acme"}, "pass",
		types.VerdictComment, []types.DiffComment{{Path: "a.go", Line: 4, Body: "why"}})
	require.NoError(t, err)
	require.Len(t, sent, 1)
	assert.Equal(t, map[string]any{"path": "a.go", "line": 4, "body": "why"}, sent[0])
}

// State is optional, and a spell that says nothing about it reads as open - which is what keeps
// a provider written before the field existed working unchanged. Merged is separate from Open on
// purpose: a review that landed still has a conversation worth reading.
func TestFindReviewCarriesTheStateAndTreatsSilenceAsOpen(t *testing.T) {
	withReviewSpell(t, func(string) (any, error) {
		return map[string]any{"id": "482", "repo": "acme/acme", "state": "merged"}, nil
	})
	merged := FindReview(context.Background(), "feat/x", "")
	assert.True(t, merged.Open(), "a merged review is still readable")
	assert.True(t, merged.Merged())

	withReviewSpell(t, func(string) (any, error) {
		return map[string]any{"id": "9", "repo": "acme/acme"}, nil
	})
	quiet := FindReview(context.Background(), "feat/x", "")
	assert.True(t, quiet.Open())
	assert.False(t, quiet.Merged(), "silence about state is not a claim that it merged")
}

// A spell that exports no publish_review answers nil without failing, which the handler reads as
// "the host took the batch": it marks every draft published, and publish only ever considers
// unpublished drafts, so the remarks can never go again. A capability the provider lacks has to
// refuse.
func TestPublishReviewRefusesAProviderThatCannotPublish(t *testing.T) {
	withReviewSpell(t, func(string) (any, error) { return nil, nil })

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

// A spell that answers anything but true is a REFUSAL. Reporting success on an unrecognized
// answer is how a reply nobody sent gets treated as delivered.
func TestReplyReviewTreatsAnyNonTrueAnswerAsARefusal(t *testing.T) {
	withReviewSpell(t, func(string) (any, error) { return true, nil })
	require.NoError(t, ReplyReview(context.Background(), types.ReviewTarget{ID: "482"}, "t1", "agreed"))

	withReviewSpell(t, func(string) (any, error) { return "sure", nil })
	err := ReplyReview(context.Background(), types.ReviewTarget{ID: "482"}, "t1", "agreed")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "t1", "the error names the thread that did not take it")
}

// Every row is attempted. Returning at the first malformed one drops the threads AFTER it, so
// a provider with one bad remark near the top renders as a conversation nobody had - and the
// reader has no way to tell that from silence.
func TestReviewThreadsKeepsReadingPastAMalformedRow(t *testing.T) {
	withReviewSpell(t, func(string) (any, error) {
		return []any{
			map[string]any{"id": "t1", "path": "a.go", "body": "before"},
			map[string]any{"id": "t2", "line": "not a number"},
			map[string]any{"id": "t3", "path": "b.go", "body": "after"},
		}, nil
	})
	got, err := ReviewThreads(context.Background(), types.ReviewTarget{ID: "482"})

	require.Error(t, err)
	require.Len(t, got, 2, "the readable threads on BOTH sides of the bad one survive")
	assert.Equal(t, "before", got[0].Body)
	assert.Equal(t, "after", got[1].Body, "a thread after the malformed row is not dropped")
}

// The credential owner is asked for ONCE per process and reused after.
//
// Naming it costs a second round trip on every host magus ships for, and this sits under opening
// a diff, where latency is the product. The cache is what keeps a "whose review is this" question
// off that path; the flag is what lets the spell skip the call it would otherwise always make.
func TestFindReviewAsksWhoTheCredentialIsOnlyOnce(t *testing.T) {
	// The cache is process-global, so a run under -count=2 - or any later test that answers with
	// a viewer - would otherwise start this one with the cache already warm and see the first
	// call skip the ask. Establish the state rather than assuming it.
	rememberViewer("")
	t.Cleanup(func() { rememberViewer("") })

	var asked []bool
	fakeSpellSeq++
	name := fmt.Sprintf("fake-viewer-%d", fakeSpellSeq)
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(name,
		spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
			asked = append(asked, req.Params["want_viewer"] == true)
			return map[string]any{"id": "482", "author": "priya", "viewer": "eli"}, nil
		})))
	prev := ReviewProvider()
	SetReviewProvider(name)
	t.Cleanup(func() { SetReviewProvider(prev) })

	first := FindReview(context.Background(), "feat/x", "")
	opened, known := first.OpenedByViewer()
	assert.True(t, known, "both names present means the question is answerable")
	assert.False(t, opened, "priya's review is not eli's")

	second := FindReview(context.Background(), "feat/x", "")
	assert.Equal(t, "eli", second.Viewer, "the cached owner rides along on every later answer")
	assert.Equal(t, []bool{true, false}, asked, "asked once, then never again")
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

// capturedVerdict publishes to a throwaway spell and returns the verdict that actually reached
// it. What the resolver decided matters only if that decision is what gets SENT.
func capturedVerdict(t *testing.T, at types.ReviewTarget, want types.ReviewVerdict) (sent string, published types.ReviewVerdict) {
	t.Helper()
	var got string
	fakeSpellSeq++
	name := fmt.Sprintf("fake-verdict-%d", fakeSpellSeq)
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(name,
		spells.WithInvoker(func(_ context.Context, req spells.InvokeRequest) (any, error) {
			got, _ = req.Params["verdict"].(string)
			return map[string]any{}, nil
		})))
	prev := ReviewProvider()
	SetReviewProvider(name)
	t.Cleanup(func() { SetReviewProvider(prev) })

	pub, err := PublishReview(context.Background(), at, "s", want,
		[]types.DiffComment{{Path: "a.go", Line: 4, Body: "why"}})
	require.NoError(t, err)
	return got, pub
}

// TestPublishSendsTheResolvedVerdictNotTheRequestedOne. The resolver could be perfectly correct
// and the feature still broken, if the requested verdict were the one that travelled - so this
// asserts on what the provider received rather than on what the resolver returned.
func TestPublishSendsTheResolvedVerdictNotTheRequestedOne(t *testing.T) {
	sent, published := capturedVerdict(t, types.ReviewTarget{ID: "1", Author: "ada", Viewer: "ada"}, types.VerdictApprove)
	assert.Equal(t, string(types.VerdictComment), sent, "a change must not approve itself at the wire")
	assert.Equal(t, types.VerdictComment, published, "and the caller is told what actually went")

	sent, published = capturedVerdict(t, types.ReviewTarget{ID: "1", Author: "grace", Viewer: "ada"}, types.VerdictApprove)
	assert.Equal(t, string(types.VerdictApprove), sent, "a colleague's change may be approved")
	assert.Equal(t, types.VerdictApprove, published)
}
