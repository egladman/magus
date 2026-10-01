package queue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/queue/types"
)

// ReviewsQuery names the change [Reviews] reads and whether it dismisses.
type ReviewsQuery struct {
	Base      string // branch the change targets
	RemoteURL string // URL of the remote the provider is asked about
	Change    string // the change's id
	// Head, when set, is the head the caller classifies for. A change whose head is
	// another is left to the caller asking about that one: nothing is classified or
	// dismissed.
	Head string
	// Dismiss dismisses each approval that does not carry, through the provider.
	Dismiss bool
}

// Reviews reports whether each approval standing on change q.Change still covers its
// head under policy, by [CarryApproval]'s classifier, so the reviews a provider shows
// agree with what the queue admits. Every reviewer's approval is classified the same
// way, a code owner's included. The provider must be a [types.ReviewDismisser].
//
// With q.Dismiss it dismisses each approval that does not carry, naming what changed,
// once it has read the change's head again and found it unmoved; it dismisses only an
// approval still standing in that read. It fetches the base and the commits it compares
// from cl's remote, and runs none of their code.
func Reviews(ctx context.Context, v types.ReadVCS, f types.BuildFacts, cl Clone, prov types.Provider, q ReviewsQuery, policy types.CarryPolicy) (types.ReviewReport, error) {
	rd, ok := prov.(types.ReviewDismisser)
	if !ok {
		return types.ReviewReport{}, errors.New("the provider reads no reviews")
	}
	listing, err := prov.ListChanges(ctx, types.ListQuery{Base: q.Base, RemoteURL: q.RemoteURL, Only: q.Change})
	if err != nil {
		return types.ReviewReport{}, err
	}
	c, others, ok := takeChange(listing, q.Change)
	if !ok {
		return types.ReviewReport{}, fmt.Errorf("#%s is not an open change against %s", q.Change, q.Base)
	}
	read, err := rd.Reviews(ctx, c)
	if err != nil {
		return types.ReviewReport{}, fmt.Errorf("reviews of %s: %w", c.Label(), err)
	}
	c.Head = read.Head
	report := types.ReviewReport{Change: c.ID, Head: c.Head, Reviews: []types.ReviewVerdict{}}
	if q.Head != "" && q.Head != c.Head {
		report.Moved = c.Head
		return report, nil
	}
	verdicts, owed, err := classifyReviews(ctx, v, f, cl, c, others, read.Approving, policy)
	if err != nil {
		return types.ReviewReport{}, err
	}
	report.Reviews, report.RegenerationOwed = verdicts, owed
	if !q.Dismiss || !slices.ContainsFunc(verdicts, func(rv types.ReviewVerdict) bool { return !rv.Carry }) {
		return report, nil
	}
	again, err := rd.Reviews(ctx, c)
	if err != nil {
		return types.ReviewReport{}, fmt.Errorf("reviews of %s: %w", c.Label(), err)
	}
	if again.Head != c.Head {
		report.Moved = again.Head
		return report, nil
	}
	standing := map[string]bool{}
	for _, r := range again.Approving {
		standing[r.ID] = true
	}
	for i, rv := range report.Reviews {
		if rv.Carry || !standing[rv.ReviewID] {
			continue
		}
		r := types.Review{ID: rv.ReviewID, Reviewer: rv.Reviewer, Commit: rv.Commit}
		if err := rd.DismissReview(ctx, c, r, dismissMessage(rv)); err != nil {
			return report, fmt.Errorf("dismiss @%s's review of %s: %w", rv.Reviewer, c.Label(), err)
		}
		report.Reviews[i].Dismissed = true
	}
	return report, nil
}

// classifyReviews runs the classifier once per distinct approved commit, from that
// commit to the commit a review of c's head covers, as plan admits c. An approval given
// there needs no verdict. A merge of the base into the change that differs from the
// plain merge only in generated files is peeled as plan peels it, and its commit is
// returned owed: the approval stands, as at plan, once apply's regeneration of the base
// reproduces that merge, which apply proves before it merges, and each carry says so.
func classifyReviews(ctx context.Context, v types.ReadVCS, f types.BuildFacts, cl Clone, c types.Change, others types.Changes, approving []types.Review, policy types.CarryPolicy) (verdicts []types.ReviewVerdict, owed []string, err error) {
	out := []types.ReviewVerdict{}
	if len(approving) == 0 {
		return out, nil, nil
	}
	tip, err := fetchBase(ctx, v, cl, others.Base)
	if err != nil {
		return nil, nil, err
	}
	if err := fetchHead(ctx, v, cl, c); err != nil {
		return nil, nil, fmt.Errorf("fetch %s: %w", c.Label(), err)
	}
	now, proofs, err := reviewTarget(ctx, v, f, cl.Root, tip, c.Head, "")
	if err != nil {
		return nil, nil, fmt.Errorf("review target of %s: %w", c.Label(), err)
	}
	for _, p := range proofs {
		owed = append(owed, p.Commit)
	}
	refs := planRefs(others)
	stackBase, err := deltaBase(ctx, v, cl, tip, now, refs)
	if err != nil {
		return nil, nil, err
	}
	decided := map[string]types.CarryVerdict{}
	for _, r := range approving {
		if r.Commit == now || r.Commit == c.Head {
			continue
		}
		verdict, ok := decided[r.Commit]
		if !ok {
			if verdict, err = carryApproval(ctx, v, f, cl, tip, r.Commit, now, stackBase, refs, policy); err != nil {
				return nil, nil, fmt.Errorf("compare %s with @%s's approved %s: %w", c.Label(), r.Reviewer, short(r.Commit), err)
			}
			decided[r.Commit] = verdict
		}
		reason := verdict.Reason
		if verdict.Carry && len(owed) > 0 {
			reason += "; it stands once the base's regeneration reproduces the generated files of the merge of the base at " +
				shorts(owed) + ", which apply proves before merging"
		}
		out = append(out, types.ReviewVerdict{Reviewer: r.Reviewer, ReviewID: r.ID, Commit: r.Commit,
			Carry: verdict.Carry, Tier: verdict.Tier, Changed: orEmpty(verdict.Changed), Reason: reason})
	}
	return out, owed, nil
}

func shorts(commits []string) string {
	out := make([]string, len(commits))
	for i, c := range commits {
		out[i] = short(c)
	}
	return strings.Join(out, ", ")
}

// deltaBase is where now's own delta starts, decided as [carryBase] decides it for an
// approved commit: the head of the change it is stacked on, else where it left the
// base's history. It is "" when that cannot be told, which measures the delta from
// where now left the base's history.
func deltaBase(ctx context.Context, v types.ReadVCS, cl Clone, tip, now string, refs []stackRef) (string, error) {
	base, _, why, err := replayBases(ctx, v, cl, tip, now, now, "", refs)
	if err != nil || why != "" {
		return "", err
	}
	return base, nil
}

// takeChange finds change id in listing, queued or not, and returns it with the listing
// of every other change. A change carrying no merge intent merges with no method yet, so
// it is built without one: nothing here merges it.
func takeChange(listing types.Changes, id string) (types.Change, types.Changes, bool) {
	others := listing
	others.Changes, others.Unqueued = nil, nil
	var found *types.Change
	for _, c := range listing.Changes {
		if c.ID == id {
			found = &c
			continue
		}
		others.Changes = append(others.Changes, c)
	}
	for _, u := range listing.Unqueued {
		if u.ID == id {
			base := u.Base
			if base == "" {
				base = listing.Base
			}
			found = &types.Change{ID: u.ID, Repo: u.Repo, Head: u.Head, Branch: u.Branch, Base: base, Title: u.Title, Author: u.Author}
			continue
		}
		others.Unqueued = append(others.Unqueued, u)
	}
	if found == nil {
		return types.Change{}, types.Changes{}, false
	}
	return *found, others, true
}

// WriteReviewLines renders r for a person: one line per approval, saying whether it
// carries and why.
func WriteReviewLines(w io.Writer, r types.ReviewReport) error {
	var b strings.Builder
	switch {
	case r.Moved != "":
		fmt.Fprintf(&b, "#%s moved to %s; the run on that head decides its reviews\n", r.Change, short(r.Moved))
	case len(r.Reviews) == 0:
		fmt.Fprintf(&b, "#%s at %s: no approval stands at an older commit\n", r.Change, short(r.Head))
	}
	for _, rv := range r.Reviews {
		verb := "does not carry"
		switch {
		case rv.Carry:
			verb = "carries"
		case rv.Dismissed:
			verb = "dismissed"
		}
		fmt.Fprintf(&b, "approval by @%s at %s %s (%s): %s\n", rv.Reviewer, short(rv.Commit), verb, rv.Tier, rv.Reason)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// dismissMessage is what the reviewer reads on the dismissal.
func dismissMessage(rv types.ReviewVerdict) string {
	return claim("magus queue: " + rv.Reason + ". Review the head again.")
}

// orEmpty keeps a report's changed list a list, never null, for a reader iterating it.
func orEmpty(paths []types.ClassifiedPath) []types.ClassifiedPath {
	if paths == nil {
		return []types.ClassifiedPath{}
	}
	return paths
}
