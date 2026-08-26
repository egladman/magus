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

	err := PublishReview(context.Background(), types.ReviewTarget{ID: "482", Repo: "acme/acme"}, "pass",
		[]types.DiffComment{{Path: "a.go", Line: 4, Body: "why"}})
	require.NoError(t, err)
	require.Len(t, sent, 1)
	assert.Equal(t, map[string]any{"path": "a.go", "line": 4, "body": "why"}, sent[0])
}

// A spell that exports no publish_review answers nil without failing, which the handler reads as
// "the host took the batch": it marks every draft published, and publish only ever considers
// unpublished drafts, so the remarks can never go again. A capability the provider lacks has to
// refuse.
func TestPublishReviewRefusesAProviderThatCannotPublish(t *testing.T) {
	withReviewSpell(t, func(string) (any, error) { return nil, nil })

	err := PublishReview(context.Background(), types.ReviewTarget{ID: "482", Repo: "acme/acme"}, "pass",
		[]types.DiffComment{{Path: "a.go", Line: 4, Body: "why"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), spells.PublishReviewContract)
}

func TestPublishAndReplyRefuseWithNoProviderWired(t *testing.T) {
	prev := ReviewProvider()
	SetReviewProvider("")
	t.Cleanup(func() { SetReviewProvider(prev) })

	assert.ErrorIs(t, PublishReview(context.Background(), types.ReviewTarget{ID: "1"}, "", nil), errNoReviewProvider)
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
