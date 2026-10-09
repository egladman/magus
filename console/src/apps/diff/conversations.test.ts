// conversations.test.ts - how the host's flat comment list becomes conversations, where they land,
// and what an outdated one quotes. Pure: rows.ts has no DOM.

import { test } from "node:test";
import assert from "node:assert/strict";
import { patchFixture } from "./fixtures";
import {
  buildRows,
  commentKey,
  conversations,
  maxLineChars,
  narrowToHunk,
  placeThreads,
  rootOf,
} from "./rows";

const ONE_HUNK = [
  "diff --git a/x.ts b/x.ts",
  "--- a/x.ts",
  "+++ b/x.ts",
  "@@ -10,3 +10,3 @@",
  " ten",
  "-old eleven",
  "+new eleven",
  " twelve",
  "",
].join("\n");

const TWO_HUNKS = [
  "diff --git a/x.ts b/x.ts",
  "--- a/x.ts",
  "+++ b/x.ts",
  "@@ -1,2 +1,2 @@",
  " first",
  "+added in the first",
  "@@ -40,2 +40,2 @@",
  " second",
  "+added in the second",
  "",
].join("\n");

function thread(id: string, path: string, hunk: number) {
  return { id, path, hunk, line: 11, author: "dana", body: `remark ${id}` };
}

// The wire is flat: one record per comment, a reply naming its conversation's first comment as
// its root. The conversation is assembled at render time, because the host's record does not nest.
test("a conversation is its root followed by its replies, oldest first", () => {
  const grouped = conversations([
    thread("t1", "x.ts", 0),
    thread("t2", "x.ts", 0),
    { ...thread("t1a", "x.ts", 0), root: "t1" },
    { ...thread("t1b", "x.ts", 0), root: "t1" },
  ]);
  assert.deepEqual(
    grouped.map((c) => c.map((t) => t.id)),
    [["t1", "t1a", "t1b"], ["t2"]],
    "two conversations interleaved in the listing each keep their own replies, in list order",
  );
});

// A reply listed before its root still sits behind it, and one whose root the host did not send
// is kept rather than dropped: "a colleague said nothing" is the one thing this app must not say.
test("a root listed late still leads, and a reply with no root in the list is kept", () => {
  const grouped = conversations([
    { ...thread("r1", "x.ts", 0), root: "t1" },
    thread("t1", "x.ts", 0),
    { ...thread("o1", "x.ts", 0), root: "gone" },
  ]);
  assert.deepEqual(
    grouped.map((c) => c.map((t) => t.id)),
    [["t1", "r1"], ["o1"]],
  );
});

test("rootOf is the thread's own root, or its id when it is the first comment", () => {
  assert.equal(rootOf(thread("t1", "x.ts", 0)), "t1");
  assert.equal(rootOf({ ...thread("t1a", "x.ts", 0), root: "t1" }), "t1");
});

// A reply can carry a line the head no longer has while its root still has one. Placing each
// comment alone would split the conversation across the file heading and the hunk.
test("a conversation lands where its root does", () => {
  const placed = placeThreads(patchFixture(ONE_HUNK), [
    thread("t1", "x.ts", 0),
    { ...thread("t1a", "x.ts", -1), root: "t1" },
  ]);
  assert.deepEqual(
    placed.atHunk.get(commentKey("x.ts", 0))?.map((t) => t.id),
    ["t1", "t1a"],
  );
  assert.equal(placed.atFile.size, 0);
});

test("a conversation on a file outside the changeset stays together in elsewhere", () => {
  const placed = placeThreads(patchFixture(ONE_HUNK), [
    thread("t1", "other.ts", -1),
    { ...thread("t1a", "other.ts", -1), root: "t1" },
  ]);
  assert.deepEqual(
    placed.elsewhere.map((t) => t.id),
    ["t1", "t1a"],
  );
});

// The host keeps the hunk an outdated remark was made on, because the line it was about is gone
// from the head. It is quoted above the root, and only above the root.
test("an outdated root quotes its diff_hunk, and its replies do not", () => {
  const files = patchFixture(ONE_HUNK);
  const root = {
    ...thread("t1", "x.ts", -1),
    outdated: true,
    diff_hunk: "@@ -1,2 +1,2 @@\n-gone line\n+replacement",
  };
  const reply = { ...thread("t1a", "x.ts", -1), root: "t1", diff_hunk: "@@ -1 +1 @@\n-ignored" };
  const rows = buildRows(
    files,
    "unified",
    undefined,
    undefined,
    placeThreads(files, [root, reply]),
  );
  assert.deepEqual(
    rows.slice(0, 6).map((r) => r.kind),
    ["file", "quote", "quote", "quote", "thread", "thread"],
  );
  assert.deepEqual(
    rows.flatMap((r) => (r.kind === "quote" ? [r.text] : [])),
    ["@@ -1,2 +1,2 @@", "-gone line", "+replacement"],
  );
});

test("a current root quotes nothing even when the host sent its hunk", () => {
  const files = patchFixture(ONE_HUNK);
  const rows = buildRows(
    files,
    "unified",
    undefined,
    undefined,
    placeThreads(files, [{ ...thread("t1", "x.ts", 0), diff_hunk: "@@ -1 +1 @@\n-x" }]),
  );
  assert.equal(rows.filter((r) => r.kind === "quote").length, 0);
});

// A step shows several hunks at once; each of them keeps its own remarks.
test("narrowing to a step keeps the remarks on every hunk it shows", () => {
  const placed = placeThreads(patchFixture(TWO_HUNKS), [
    { id: "t1", path: "x.ts", line: 3, hunk: 0, author: "priya", body: "here" },
    { id: "t2", path: "x.ts", line: 90, hunk: 1, author: "marcus", body: "over here" },
  ]);
  const both = narrowToHunk(placed, [commentKey("x.ts", 0), commentKey("x.ts", 1)]);
  assert.equal(both, placed, "nothing is outside the step, so nothing moves");

  const one = narrowToHunk(placed, [commentKey("x.ts", 1)]);
  assert.deepEqual(
    one.elsewhere.map((t) => t.id),
    ["t1"],
  );
});

// The step heading leads the stream, and each placed hunk's reason sits directly under its heading.
test("a step's heading leads the rows and each hunk is followed by its reason", () => {
  const files = patchFixture(TWO_HUNKS);
  const why = { relation: "starts" as const, text: "defines x" };
  const rows = buildRows(files, "unified", undefined, undefined, undefined, {
    head: { position: 1, total: 3, group: "connected", label: "x", hunks: 2 },
    places: new Map([
      [commentKey("x.ts", 0), { label: "x", why }],
      [commentKey("x.ts", 1), { label: "y", why: { ...why, text: "uses x" } }],
    ]),
  });
  assert.deepEqual(
    rows.filter((r) => r.kind !== "line").map((r) => r.kind),
    ["step", "file", "hunk", "why", "hunk", "why"],
  );
  assert.ok(maxLineChars(rows) >= "defines x".length);
});
