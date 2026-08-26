import { test } from "node:test";
import assert from "node:assert/strict";
import { fetchContext, mergedNotice, type ReviewInfo } from "./session";

const review = (over: Partial<ReviewInfo> = {}): ReviewInfo => ({
  id: "482",
  repo: "acme/acme",
  threads: [],
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

// The SAME golden vector internal/diff/session_test.go asserts.
//
// If these two drift the feature silently half-works: a hunk the person marked read in the
// browser still looks unread to an agent reading the same session, and neither side reports
// an error. Two tests over one literal turns that into a build failure.
// The digest tests that were here are gone with the code they covered. hunkDigest and
// patchDigest were TypeScript reimplementations of internal/diff's, kept in step by a golden
// vector pasted from Go - which is the arrangement that let the two readers drift in the first
// place. The daemon computes both now and ships them, so there is nothing here to pin.

test("context requests carry the reviewed patch identity", async () => {
  const realFetch = globalThis.fetch;
  let url = "";
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    url = String(input);
    return new Response(JSON.stringify({ path: "a.go", as_of: "snapshot-a", start: 2, lines: [] }));
  }) as typeof fetch;
  try {
    await fetchContext("127.0.0.1:7391", "a.go", "snapshot-a", 3, 3, new AbortController().signal);
    const query = new URL(url).searchParams;
    assert.equal(query.get("path"), "a.go");
    assert.equal(query.get("as_of"), "snapshot-a");
  } finally {
    globalThis.fetch = realFetch;
  }
});
