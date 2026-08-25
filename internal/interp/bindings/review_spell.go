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
// reached are all the SAME answer here: a zero Number and a reason. None of them is an error,
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
	m, _ := resp.Data.(map[string]any)
	return types.ReviewTarget{
		Number: intOf(m["number"]),
		Repo:   strOf(m["repo"]),
		Reason: strOf(m["reason"]),
	}
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
			"repo": at.Repo, "number": at.Number, "summary": summary, "drafts": rows,
		},
	})
	if err != nil {
		return 0, err
	}
	m, _ := resp.Data.(map[string]any)
	return intOf(m["count"]), nil
}

// ReviewThreads reads the comment threads already on the review.
//
// Empty on every failure, for the reason OpenReview gives about itself: this is the one call
// that makes a local surface depend on a host being reachable, and the surface has to keep
// working when it is not.
func ReviewThreads(ctx context.Context, at types.ReviewTarget) []types.ReviewThread {
	drv, ok := reviewDriver()
	if !ok || at.Number == 0 {
		return nil
	}
	resp, err := drv.Invoke(ctx, spells.InvokeRequest{
		Target: spells.ThreadsContract,
		Params: map[string]any{"repo": at.Repo, "number": at.Number},
	})
	if err != nil {
		return nil
	}
	rows, _ := resp.Data.([]any)
	out := make([]types.ReviewThread, 0, len(rows))
	for _, r := range rows {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, types.ReviewThread{
			ID:     strOf(m["id"]),
			Path:   strOf(m["path"]),
			Line:   intOf(m["line"]),
			Author: strOf(m["author"]),
			Body:   strOf(m["body"]),
		})
	}
	return out
}

func reviewDriver() (spells.Driver, bool) {
	name := ReviewProvider()
	if name == "" {
		return nil, false
	}
	return project.DefaultSpellRegistry().Lookup(name)
}

// strOf and intOf read one field out of a spell's answer without trusting its type.
//
// A spell is user code returning a dynamically-typed map, so a wrong type is a spell bug and
// must degrade to a zero value rather than panicking inside magus. Buzz integers arrive as
// float64 through the JSON boundary, which is why that case is here and not an oversight.
func strOf(v any) string {
	s, _ := v.(string)
	return s
}

func intOf(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
