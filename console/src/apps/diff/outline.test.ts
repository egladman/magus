// outline.test.ts - an agent's outline as rows and as a paste check. Pure: no DOM, no network.

import { test } from "node:test";
import assert from "node:assert/strict";
import { patchFixture } from "./fixtures";
import { outlineHeading, outlineKey, outlinesByThread, pastesOutline } from "./outline";
import { buildRows, placeThreads } from "./rows";
import type { DiffOutline } from "./session";

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

function thread(id: string, hunk: number) {
  return { id, path: "x.ts", hunk, line: 11, author: "dana", body: `remark ${id}` };
}

const outline: DiffOutline = {
  thread: "t1",
  agent_name: "scout",
  topics: ["is the cache write racy?", "who calls Put"],
};

test("the heading names the agent, or says an agent when it gave no name", () => {
  assert.equal(outlineHeading(outline), "scout suggests covering:");
  assert.equal(outlineHeading({ thread: "t1", topics: ["a"] }), "an agent suggests covering:");
});

// The outline closes the conversation it was left for, after the last reply and before the next
// conversation, and a reply's own id never starts a second one.
test("an outline sits after the last comment of its conversation", () => {
  const files = patchFixture(ONE_HUNK);
  const placed = placeThreads(files, [
    thread("t1", 0),
    thread("t2", 0),
    { ...thread("t1a", 0), root: "t1" },
  ]);
  const rows = buildRows(
    files,
    "unified",
    undefined,
    undefined,
    placed,
    undefined,
    outlinesByThread([outline]),
  );

  const shape = rows
    .filter((r) => r.kind === "thread" || r.kind === "outline")
    .map((r) => (r.kind === "thread" ? r.thread.id : `${r.head ? "head" : "topic"}:${r.text}`));
  assert.deepEqual(shape, [
    "t1",
    "t1a",
    "head:scout suggests covering:",
    "topic:is the cache write racy?",
    "topic:who calls Put",
    "t2",
  ]);
});

test("an outline for a conversation that is not on screen draws nothing", () => {
  const files = patchFixture(ONE_HUNK);
  const rows = buildRows(
    files,
    "unified",
    undefined,
    undefined,
    placeThreads(files, [thread("t2", 0)]),
    undefined,
    outlinesByThread([outline]),
  );
  assert.equal(rows.filter((r) => r.kind === "outline").length, 0);
});

test("a paste holding an outline topic is recognised, however it was rewrapped", () => {
  const outlines = [outline];
  assert.ok(pastesOutline("is the cache write racy?", outlines));
  assert.ok(pastesOutline("Re: Is  the cache\nwrite racy? yes", outlines), "case and wrapping");
  assert.ok(pastesOutline("see: WHO CALLS PUT", outlines), "a topic of a later position");
  assert.ok(!pastesOutline("a reply in my own words", outlines));
  assert.ok(!pastesOutline("", outlines));
  assert.ok(!pastesOutline("who calls Put", []), "no outline, nothing to refuse");
});

test("outlinesByThread keys by thread id and outlineKey moves with the topics", () => {
  assert.equal(outlinesByThread([outline]).get("t1"), outline);
  assert.equal(outlinesByThread(undefined).size, 0);
  assert.equal(outlineKey(undefined), outlineKey([]));
  assert.notEqual(outlineKey([outline]), outlineKey([{ ...outline, topics: ["other"] }]));
});
