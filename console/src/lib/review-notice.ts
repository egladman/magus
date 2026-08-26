// review-notice.ts - one sentence, offered when a review has landed with a conversation on it.
//
// It lives in lib/ rather than beside the diff surface because TWO things ask the question and they
// are in different bundles: the Diff surface when you open it on a merged review, and the shell's
// watcher when a review you took part in lands while you are elsewhere. The rule that decides
// whether to say anything is the design, not an implementation detail of either one, and two copies
// of it would drift the moment somebody tuned the wording.

// MergedReview is the little of a review this needs. Structural, so the Diff surface's fuller
// ReviewInfo satisfies it without lib/ having to know that type exists.
export interface MergedReview {
  readonly repo?: string;
  // state is what the host says became of the review: "open", "merged" or "closed". Absent when the
  // provider does not answer it, which reads as open.
  readonly state?: string;
}

// mergedNotice is what to say, or "" when there is nothing worth saying.
//
// The emptiness is the point. A merged review whose conversation was empty has nothing to preserve,
// and a prompt that fires on every merge regardless is one a reader learns to dismiss unread - which
// spends the attention it was saving for the merge that mattered.
//
// It names `magus notes capture` rather than doing it. Notes are human-authored by construction:
// there is no author field to spoof because authorship rides version control, and a console that
// wrote one would be the first thing to break that.
export function mergedNotice(review: MergedReview | null, said: number): string {
  if (!review || review.state !== "merged" || said <= 0) return "";
  const where = review.repo ? ` on ${review.repo}` : "";
  return (
    `This review merged${where}, and its ${said} ${said === 1 ? "remark" : "remarks"} live only on the host. ` +
    "Run magus notes capture to keep the conversation in your knowledge graph."
  );
}
