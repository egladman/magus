package bindings

import (
	"context"
	"fmt"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// OpenReview asks the selected spell which review is open for a branch.
//
// A workspace with no provider, a branch with no pull request, and a host that could not be
// reached are all the SAME answer here: an empty ID and a reason. None of them is an error,
// because none of them is a thing the reader did wrong, and a diff that refuses to open
// because a review lookup failed would be a worse tool than one that shows the change.
//
// The branch and remote are passed IN rather than discovered by the spell. magus already knows
// both through a VCS layer that speaks four backends; a spell rederiving them would be a
// second opinion about the same working tree, and one that only knows one host's conventions.
func OpenReview(ctx context.Context, branch, remote string) types.ReviewTarget {
	drv, ok := reviewDriver()
	if !ok {
		return types.ReviewTarget{Reason: "no review provider wired"}
	}
	resp, err := drv.Invoke(ctx, spells.InvokeRequest{
		Target: spells.OpenReviewContract,
		Params: map[string]any{"branch": branch, "remote": remote},
	})
	if err != nil {
		return types.ReviewTarget{Reason: "review provider: " + err.Error()}
	}
	at, err := decodeReviewTarget(resp.Data)
	if err != nil {
		// A malformed answer becomes a REASON rather than a zero target, because those two
		// render identically and mean opposite things. Silently zeroing `number` would say
		// "no pull request for this branch", sending the reader to look at their branch when
		// the fault is in their spell.
		return types.ReviewTarget{Reason: err.Error()}
	}
	return at
}

func decodeReviewTarget(data any) (types.ReviewTarget, error) {
	where := "review provider: " + spells.OpenReviewContract
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
	return at, nil
}

// PublishReview sends drafts as ONE review and reports how many the host accepted.
//
// The batch is the unit for the reason spells/review.go gives: self-review is a pass, and
// publishing each remark as it is written would send the first thought before the fifth
// changed your mind about it.
//
// An error here is REAL and propagates, unlike the read paths. Publishing is the one thing in
// this file that changes something a colleague can see, so a caller must never be told it
// happened when it did not.
func PublishReview(ctx context.Context, at types.ReviewTarget, summary string, drafts []types.DiffComment) (int, error) {
	drv, ok := reviewDriver()
	if !ok {
		return 0, fmt.Errorf("no review provider wired; a magusfile selects one with magus\\review.provider(<spell>)")
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
		return 0, err
	}
	where := "review provider: " + spells.PublishReviewContract
	m, ok := resp.Data.(map[string]any)
	if !ok {
		return 0, fmt.Errorf("%s returned %T, want a record", where, resp.Data)
	}
	return intField(m, "count", where)
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
		return fmt.Errorf("no review provider wired; a magusfile selects one with magus\\review.provider(<spell>)")
	}
	resp, err := drv.Invoke(ctx, spells.InvokeRequest{
		Target: spells.ReplyReviewContract,
		Params: map[string]any{"repo": at.Repo, "id": at.ID, "thread": thread, "body": body},
	})
	if err != nil {
		return err
	}
	if sent, _ := resp.Data.(bool); !sent {
		return fmt.Errorf("the host did not accept a reply to thread %s", thread)
	}
	return nil
}

// ReviewThreads reads the comment threads already on the review.
//
// Empty on an unreachable host, for the reason OpenReview gives about itself: this is the one
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
		Target: spells.ThreadsContract,
		Params: map[string]any{"repo": at.Repo, "id": at.ID},
	})
	if err != nil {
		//nolint:nilerr // an unreachable host is not a failure of this call: the surface has
		// to keep working when the forge is down, and a diff that will not open because
		// GitHub is slow is a worse tool than one that shows the change and says nothing
		// about the conversation. A MALFORMED answer is different and is reported below.
		return nil, nil
	}
	where := "review provider: " + spells.ThreadsContract
	rows, ok := resp.Data.([]any)
	if !ok {
		return nil, fmt.Errorf("%s returned %T, want a list", where, resp.Data)
	}
	out := make([]types.ReviewThread, 0, len(rows))
	for i, r := range rows {
		t, err := decodeReviewThread(r, fmt.Sprintf("%s[%d]", where, i))
		if err != nil {
			return out, err
		}
		out = append(out, t)
	}
	return out, nil
}

func decodeReviewThread(row any, where string) (types.ReviewThread, error) {
	m, ok := row.(map[string]any)
	if !ok {
		return types.ReviewThread{}, fmt.Errorf("%s is %T, want a record", where, row)
	}
	var t types.ReviewThread
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
