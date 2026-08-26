import { test } from "node:test";
import assert from "node:assert/strict";
import { mergedNotice, saidNotice, type MergedReview } from "./review-notice";

const review = (over: Partial<MergedReview> = {}): MergedReview => ({
  repo: "acme/acme",
  ...over,
});

// The offer exists to catch a conversation before it becomes somebody else's website's problem.
test("a merged review with remarks offers to keep them", () => {
  const said = mergedNotice(review({ state: "merged" }), 3);
  assert.match(said, /merged on acme\/acme/);
  assert.match(said, /3 remarks/);
  assert.match(said, /magus notes capture/);
});

test("one remark is not pluralised", () => {
  assert.match(mergedNotice(review({ state: "merged" }), 1), /1 remark live/);
});

// The silence cases are the design. A prompt that fires on every merge is one a reader learns to
// dismiss unread, and then it is worth nothing on the merge that mattered.
test("a merged review nobody said anything on stays silent", () => {
  assert.equal(mergedNotice(review({ state: "merged" }), 0), "");
});

test("an open review says nothing, however much was said on it", () => {
  assert.equal(mergedNotice(review({ state: "open" }), 9), "");
  assert.equal(mergedNotice(review(), 9), "", "a provider that answers no state reads as open");
});

test("a closed review that never landed is not a merge", () => {
  assert.equal(mergedNotice(review({ state: "closed" }), 4), "");
});

test("no review at all says nothing", () => {
  assert.equal(mergedNotice(null, 4), "");
});

// A review with no repo still has something worth keeping; the sentence just does not name a place.
test("a review with no repo is offered without one", () => {
  const said = mergedNotice({ state: "merged" }, 2);
  assert.match(said, /This review merged, and/);
});
