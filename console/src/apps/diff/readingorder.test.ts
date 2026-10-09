// readingorder.test.ts - the unit focus mode reads. Pure: no DOM, no server.

import { test } from "node:test";
import assert from "node:assert/strict";
import { patchFixture } from "./fixtures";
import {
  fileSteps,
  firstUnreadStep,
  readingSteps,
  stepFiles,
  findStepIndex,
  stepRows,
  type HunkAddress,
} from "./readingorder";
import type { DiffGroup, DiffHunkRef, DiffOrder, DiffStepHunk, DiffWhy } from "./session";

const addr = (path: string, index: number): HunkAddress => ({
  path,
  index,
  digest: `${path}#${index}`,
});

const ref = (a: HunkAddress): DiffHunkRef => ({ path: a.path, index: a.index, digest: a.digest });

function placed(a: HunkAddress, text: string, label = ""): DiffStepHunk {
  const why: DiffWhy = { relation: "uses", text };
  return { ref: ref(a), label, why };
}

function order(groups: DiffGroup[]): DiffOrder {
  const hunkCount = groups.reduce((n, g) => n + g.hunk_count, 0);
  return { groups, count: { hunk_count: hunkCount, placed: hunkCount, complete: true } };
}

const A0 = addr("a.go", 0);
const A1 = addr("a.go", 1);
const B0 = addr("b.go", 0);
const G0 = addr("gen/g.go", 0);
const U0 = addr("notes.md", 0);
const VISIBLE = [A0, A1, B0, G0, U0];

const ORDER = order([
  {
    kind: "connected",
    label: "Thing",
    hunk_count: 3,
    reach: 4,
    steps: [
      { number: 1, hunks: [placed(B0, "defines Thing", "Thing")] },
      { number: 2, hunks: [placed(A0, "uses Thing"), placed(A1, "continues")] },
    ],
  },
  {
    kind: "generated",
    hunk_count: 1,
    reach: 0,
    steps: [{ number: 3, hunks: [placed(G0, "generated")] }],
  },
  {
    kind: "unranked",
    hunk_count: 1,
    reach: 0,
    steps: [{ number: 4, hunks: [placed(U0, "prose")] }],
  },
]);

test("steps follow the server's order, not the file order", () => {
  const steps = readingSteps(ORDER, VISIBLE);
  assert.deepEqual(
    steps.map((s) => [s.number, s.group, s.groupLabel, s.hunks.map((h) => `${h.path}#${h.index}`)]),
    [
      [1, "connected", "Thing", ["b.go#0"]],
      [2, "connected", "Thing", ["a.go#0", "a.go#1"]],
      [3, "generated", "", ["gen/g.go#0"]],
      [4, "unranked", "", ["notes.md#0"]],
    ],
  );
  const first = steps[0]?.hunks[0];
  assert.equal(first?.label, "Thing");
  assert.equal(first?.why.text, "defines Thing");
});

// Not in the order is not the same as not there: the reader still has to see the hunk, so it
// follows the last group as a step of its own.
test("a visible hunk the order does not place is appended, never dropped", () => {
  const late = addr("late.go", 0);
  const steps = readingSteps(ORDER, [...VISIBLE, late]);
  const last = steps[steps.length - 1];
  assert.equal(last?.number, 5);
  assert.equal(last?.group, "unranked");
  assert.deepEqual(
    last?.hunks.map((h) => h.path),
    ["late.go"],
  );
});

// An order computed for one patch must not be laid over a hunk of the same index in another.
test("a hunk whose digest differs from the placed one is treated as unplaced", () => {
  const moved = { ...B0, digest: "changed" };
  const steps = readingSteps(ORDER, [moved, A0, A1, G0, U0]);
  const where = steps.find((s) => s.hunks.some((h) => h.path === "b.go"));
  assert.equal(where?.group, "unranked");
  assert.equal(where?.hunks[0]?.digest, "changed");
});

test("hunks the reader cannot see leave their steps, and an emptied step goes", () => {
  // The generated file is folded away.
  const steps = readingSteps(ORDER, [A0, A1, B0, U0]);
  assert.deepEqual(
    steps.map((s) => s.number),
    [1, 2, 4],
  );
  // One hunk of a two-hunk step is hidden: the step stays with the other.
  const part = readingSteps(ORDER, [A1, B0, G0, U0]);
  assert.deepEqual(
    part[1]?.hunks.map((h) => h.index),
    [1],
  );
});

test("a hunk the order places twice is shown at its first place only", () => {
  const twice = order([
    {
      kind: "connected",
      label: "T",
      hunk_count: 3,
      reach: 1,
      steps: [
        { number: 1, hunks: [placed(A0, "first")] },
        { number: 2, hunks: [placed(A0, "again"), placed(B0, "b")] },
      ],
    },
  ]);
  const steps = readingSteps(twice, [A0, B0]);
  assert.deepEqual(
    steps.map((s) => s.hunks.map((h) => h.path)),
    [["a.go"], ["b.go"]],
  );
  assert.equal(steps[0]?.hunks[0]?.why.text, "first");
});

// With no order on the wire, focus mode is what it was before the order existed.
test("without an order every hunk is a step of its own, in file order", () => {
  const steps = readingSteps(undefined, VISIBLE);
  assert.deepEqual(steps, fileSteps(VISIBLE));
  assert.deepEqual(
    steps.map((s) => [s.number, s.group, s.hunks.length, s.hunks[0]?.path]),
    [
      [1, undefined, 1, "a.go"],
      [2, undefined, 1, "a.go"],
      [3, undefined, 1, "b.go"],
      [4, undefined, 1, "gen/g.go"],
      [5, undefined, 1, "notes.md"],
    ],
  );
});

test("an empty changeset has no steps", () => {
  assert.deepEqual(readingSteps(ORDER, []), []);
  assert.deepEqual(readingSteps(undefined, []), []);
});

test("findStepIndex finds the step holding any of its hunks", () => {
  const steps = readingSteps(ORDER, VISIBLE);
  assert.equal(findStepIndex(steps, { path: "a.go", index: 1 }), 1);
  assert.equal(findStepIndex(steps, { path: "a.go", index: 7 }), -1);
  assert.equal(findStepIndex(steps, null), -1);
});

test("a pass resumes at the first step with a hunk still unread", () => {
  const steps = readingSteps(ORDER, VISIBLE);
  assert.equal(firstUnreadStep(steps, new Set())?.number, 1);
  // Reading one hunk of a two-hunk step leaves the step unread.
  assert.equal(firstUnreadStep(steps, new Set([B0.digest, A0.digest]))?.number, 2);
  assert.equal(firstUnreadStep(steps, new Set(VISIBLE.map((v) => v.digest))), null);
});

const PATCH = [
  "diff --git a/a.go b/a.go",
  "--- a/a.go",
  "+++ b/a.go",
  "@@ -1,2 +1,2 @@",
  " one",
  "+two",
  "@@ -40,2 +40,2 @@",
  " forty",
  "+forty-one",
  "diff --git a/b.go b/b.go",
  "--- a/b.go",
  "+++ b/b.go",
  "@@ -1,2 +1,2 @@",
  " x",
  "+y",
  "",
].join("\n");

// A step holds only its own hunks, in its own order, and a hunk keeps the index the session
// addresses it by.
test("stepFiles slices the files down to the step, keeping each hunk's own index", () => {
  const files = patchFixture(PATCH);
  const digests = (path: string) => files.find((f) => f.path === path)?.hunks.map((h) => h.digest);
  const a1 = { path: "a.go", index: 1, digest: digests("a.go")?.[1] ?? "" };
  const b0 = { path: "b.go", index: 0, digest: digests("b.go")?.[0] ?? "" };
  const steps = readingSteps(
    order([
      {
        kind: "connected",
        label: "cycle",
        hunk_count: 2,
        reach: 1,
        steps: [{ number: 1, hunks: [placed(b0, "b"), placed(a1, "a")] }],
      },
    ]),
    [b0, a1],
  );
  const sliced = stepFiles(steps[0] as (typeof steps)[number], files);
  assert.deepEqual(
    sliced.map((f) => [f.path, f.hunks.map((h) => h.index)]),
    [
      ["b.go", [0]],
      ["a.go", [1]],
    ],
    "a step spanning files lists each where the step reaches it",
  );
});

test("stepRows heads the step by its position among the steps on screen", () => {
  const steps = readingSteps(ORDER, [A0, A1, B0, U0]);
  const rows = stepRows(steps, 1);
  assert.deepEqual(rows?.head, {
    position: 2,
    total: 3,
    group: "connected",
    label: "Thing",
    hunks: 2,
  });
  assert.equal(rows?.places.get("a.go\n1")?.why.text, "continues");
  assert.equal(stepRows(steps, 9), null);
});
