package bindings

import (
	"context"
	"errors"
	"fmt"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// errNoReviewProvider is what both outward-facing calls refuse with. One sentence, in one
// place, because the two are the same situation and a reader who meets it from publish and
// again from reply must not be left wondering whether they are different problems.
var errNoReviewProvider = errors.New(
	"no review provider wired; a magusfile selects one with magus\\review.provider(<spell>)")

// FindReview asks the selected spell which review is open for a branch.
//
// A workspace with no provider, a branch with no pull request, and a host that could not be
// reached are all the SAME answer here: an empty ID and a reason. None of them is an error,
// because none of them is a thing the reader did wrong, and a diff that refuses to open
// because a review lookup failed would be a worse tool than one that shows the change.
//
// The branch and remote are passed IN rather than discovered by the spell; see
// types.ReviewOrigin for why.
func FindReview(ctx context.Context, branch, remote string) types.ReviewTarget {
	drv, ok := reviewDriver()
	if !ok {
		return types.ReviewTarget{Reason: "no review provider wired"}
	}
	resp, err := drv.Invoke(ctx, spells.InvokeRequest{
		Target: spells.FindReviewContract,
		Params: map[string]any{"branch": branch, "remote": remote},
	})
	if err != nil {
		return types.ReviewTarget{Reason: "review provider: " + err.Error()}
	}
	at, err := decodeReviewTarget(resp.Data)
	if err != nil {
		// A malformed answer becomes a REASON rather than a zero target, because those two
		// render identically and mean opposite things. Silently emptying `id` would say
		// "no pull request for this branch", sending the reader to look at their branch when
		// the fault is in their spell.
		return types.ReviewTarget{Reason: err.Error()}
	}
	return at
}

func decodeReviewTarget(data any) (types.ReviewTarget, error) {
	where := "review provider: " + spells.FindReviewContract
	m, ok := data.(map[string]any)
	if !ok {
		return types.ReviewTarget{}, fmt.Errorf("%s returned %T, want a record", where, data)
	}
	var at types.ReviewTarget
	var err error
	if at.ID, err = strField(m, "id", where); err != nil {
		return types.ReviewTarget{}, err
	}
	if at.Repo, err = strField(m, "repo", where); err != nil {
		return types.ReviewTarget{}, err
	}
	if at.Reason, err = strField(m, "reason", where); err != nil {
		return types.ReviewTarget{}, err
	}
	if at.State, err = strField(m, "state", where); err != nil {
		return types.ReviewTarget{}, err
	}
	return at, nil
}

// PublishReview sends drafts as ONE review.
//
// The batch is the unit for the reason spells/review.go gives: self-review is a pass, and
// publishing each remark as it is written would send the first thought before the fifth
// changed your mind about it.
//
// An error here is REAL and propagates, unlike the read paths. Publishing is the one thing in
// this file that changes something a colleague can see, so a caller must never be told it
// happened when it did not.
func PublishReview(ctx context.Context, at types.ReviewTarget, summary string, drafts []types.DiffComment) error {
	drv, ok := reviewDriver()
	if !ok {
		return errNoReviewProvider
	}
	rows := make([]any, 0, len(drafts))
	for _, d := range drafts {
		rows = append(rows, map[string]any{
			"path": d.Path,
			// The hunk index is not a line number, and the spell needs a line. Carried as-is so
			// the spell can say so plainly rather than inventing a position: a comment anchored
			// to the wrong line is worse than one the host rejected.
			"line": d.Line,
			"body": d.Body,
		})
	}
	resp, err := drv.Invoke(ctx, spells.InvokeRequest{
		Target: spells.PublishReviewContract,
		Params: map[string]any{
			"repo": at.Repo, "id": at.ID, "summary": summary, "drafts": rows,
		},
	})
	if err != nil {
		return err
	}
	// An absent op answers nil without failing, which is the read paths' "this provider lacks
	// the capability" and cannot mean the same thing here: the caller marks every draft
	// published on a nil error, and publish considers only unpublished drafts, so a silent
	// no-op costs the remarks permanently.
	if resp.Data == nil {
		return fmt.Errorf("review provider: %s is not implemented by this spell, so nothing was sent",
			spells.PublishReviewContract)
	}
	// What came back is not read beyond that. A review posts as ONE request, so a per-draft
	// count could only restate the length of what was sent - which the caller already has. See
	// the handler's publish for why every draft in the batch is one the provider could place.
	return nil
}

// ReplyReview answers one existing thread, so a conversation can be finished without leaving.
//
// Loud, like PublishReview and unlike the two read paths: a reply is a sentence a colleague is
// waiting for, and a caller told it was sent when it was not will believe the conversation is
// finished. A spell that answers false without erroring is reported as a refusal here rather
// than passed off as success - the ONLY thing this function may not do is stay quiet.
func ReplyReview(ctx context.Context, at types.ReviewTarget, thread, body string) error {
	drv, ok := reviewDriver()
	if !ok {
		return errNoReviewProvider
	}
	resp, err := drv.Invoke(ctx, spells.InvokeRequest{
		Target: spells.ReplyReviewContract,
		Params: map[string]any{"repo": at.Repo, "id": at.ID, "thread": thread, "body": body},
	})
	if err != nil {
		return err
	}
	if sent, _ := resp.Data.(bool); !sent {
		return fmt.Errorf("review provider: %s did not accept a reply to thread %s",
			spells.ReplyReviewContract, thread)
	}
	return nil
}

// ReviewThreads reads the comment threads already on the review.
//
// Empty on an unreachable host, for the reason FindReview gives about itself: this is the one
// call that makes a local surface depend on a host being reachable, and the surface has to keep
// working when it is not. Nil error, empty list.
//
// A MALFORMED thread is different, and is reported. Dropping one leaves the surface saying a
// colleague said nothing, which is the single worst thing a review reader can be told - and
// the threads that did decode still come back, so the caller shows what it has and says what
// it could not read.
func ReviewThreads(ctx context.Context, at types.ReviewTarget) ([]types.ReviewThread, error) {
	drv, ok := reviewDriver()
	if !ok || !at.Open() {
		return nil, nil
	}
	resp, err := drv.Invoke(ctx, spells.InvokeRequest{
		Target: spells.ReviewThreadsContract,
		Params: map[string]any{"repo": at.Repo, "id": at.ID},
	})
	if err != nil {
		//nolint:nilerr // an unreachable host is not a failure of this call; see the doc above.
		return nil, nil
	}
	where := "review provider: " + spells.ReviewThreadsContract
	if resp.Data == nil {
		// Absent reads as the zero value, not as an error - the posture spell_decode.go states
		// for this whole layer. A spell whose review has no threads returns nothing, and
		// calling that malformed would put a provider bug on the screen for an empty review.
		return nil, nil
	}
	rows, ok := resp.Data.([]any)
	if !ok {
		return nil, fmt.Errorf("%s returned %T, want a list", where, resp.Data)
	}
	out := make([]types.ReviewThread, 0, len(rows))
	// Every row is attempted. Returning at the first bad one would drop the threads AFTER it,
	// so a provider with one malformed remark near the top would render as a conversation
	// nobody had - which is the failure this whole path is written to avoid.
	var bad []error
	for i, r := range rows {
		t, err := decodeReviewThread(r, fmt.Sprintf("%s[%d]", where, i))
		if err != nil {
			bad = append(bad, err)
			continue
		}
		out = append(out, t)
	}
	return out, errors.Join(bad...)
}

func decodeReviewThread(row any, where string) (types.ReviewThread, error) {
	m, ok := row.(map[string]any)
	if !ok {
		return types.ReviewThread{}, fmt.Errorf("%s is %T, want a record", where, row)
	}
	// UNPLACED until something places it. The zero value is a valid hunk index, so leaving it
	// would render every thread against the first hunk of its file - the wrong code, stated
	// confidently - on any path that does not reach diff.PlaceThreads.
	t := types.ReviewThread{Hunk: -1}
	var err error
	if t.ID, err = strField(m, "id", where); err != nil {
		return types.ReviewThread{}, err
	}
	if t.Path, err = strField(m, "path", where); err != nil {
		return types.ReviewThread{}, err
	}
	if t.Line, err = intField(m, "line", where); err != nil {
		return types.ReviewThread{}, err
	}
	if t.Author, err = strField(m, "author", where); err != nil {
		return types.ReviewThread{}, err
	}
	if t.Body, err = strField(m, "body", where); err != nil {
		return types.ReviewThread{}, err
	}
	return t, nil
}

func reviewDriver() (spells.Driver, bool) {
	name := ReviewProvider()
	if name == "" {
		return nil, false
	}
	return project.DefaultSpellRegistry().Lookup(name)
}
