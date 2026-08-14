import { test } from "node:test";
import assert from "node:assert/strict";
import { parsePatch } from "./parse";
import {
  buildRows,
  hunkRowIndexes,
  fileRowIndexes,
  nextIndexAfter,
  prevIndexBefore,
  type Row,
} from "./rows";

// at() indexes without a non-null assertion (the repo bans them) and fails the test with a
// useful message instead of a TypeError three lines later.
function at<T>(xs: readonly T[], i: number, what: string): T {
  const v = xs[i];
  if (v === undefined) throw new Error(`missing ${what} at index ${i}`);
  return v;
}

const REPLACEMENT = [
  "diff --git a/x.ts b/x.ts",
  "--- a/x.ts",
  "+++ b/x.ts",
  "@@ -1,4 +1,4 @@",
  " keep",
  "-old one",
  "-old two",
  "+new one",
  "+new two",
  " tail",
  "",
].join("\n");

test("unified rows are one per line, with a file and a hunk heading", () => {
  const rows = buildRows(parsePatch(REPLACEMENT), "unified");
  assert.deepEqual(
    rows.map((r) => r.kind),
    ["file", "hunk", "line", "line", "line", "line", "line", "line"],
  );
});

// The point of split view: a deletion run and the addition run that follows it are a
// replacement and must sit SIDE BY SIDE. Emitting each as it arrives stacks old above new,
// which is just the unified view in two columns.
test("split pairs a deletion run against the addition run that follows it", () => {
  const rows = buildRows(parsePatch(REPLACEMENT), "split");
  const pairs = rows.filter((r): r is Extract<Row, { kind: "pair" }> => r.kind === "pair");
  assert.deepEqual(
    pairs.map((p) => [p.left?.text ?? null, p.right?.text ?? null]),
    [
      ["keep", "keep"],
      ["old one", "new one"],
      ["old two", "new two"],
      ["tail", "tail"],
    ],
  );
});

// An uneven replacement must pad the short side, not truncate the long one.
test("split pads the shorter side of an uneven replacement", () => {
  const patch = ["--- a/x", "+++ b/x", "@@ -1,3 +1,2 @@", "-a", "-b", "-c", "+z", ""].join("\n");
  const pairs = buildRows(parsePatch(patch), "split").filter(
    (r): r is Extract<Row, { kind: "pair" }> => r.kind === "pair",
  );
  assert.equal(pairs.length, 3);
  assert.deepEqual(
    pairs.map((p) => [p.left?.text ?? null, p.right?.text ?? null]),
    [
      ["a", "z"],
      ["b", null],
      ["c", null],
    ],
  );
});

// Additions with no deletions opposite them occupy the right column only.
test("split leaves the left column empty for a pure addition run", () => {
  const patch = ["--- a/x", "+++ b/x", "@@ -1,1 +1,3 @@", " ctx", "+one", "+two", ""].join("\n");
  const pairs = buildRows(parsePatch(patch), "split").filter(
    (r): r is Extract<Row, { kind: "pair" }> => r.kind === "pair",
  );
  assert.deepEqual(
    pairs.map((p) => [p.left?.text ?? null, p.right?.text ?? null]),
    [
      ["ctx", "ctx"],
      [null, "one"],
      [null, "two"],
    ],
  );
});

// Two replacements separated by context must not be zipped into each other.
test("split does not zip two replacements across a context line", () => {
  const patch = [
    "--- a/x",
    "+++ b/x",
    "@@ -1,5 +1,5 @@",
    "-a1",
    "+b1",
    " mid",
    "-a2",
    "+b2",
    "",
  ].join("\n");
  const pairs = buildRows(parsePatch(patch), "split").filter(
    (r): r is Extract<Row, { kind: "pair" }> => r.kind === "pair",
  );
  assert.deepEqual(
    pairs.map((p) => [p.left?.text ?? null, p.right?.text ?? null]),
    [
      ["a1", "b1"],
      ["mid", "mid"],
      ["a2", "b2"],
    ],
  );
});

// An addition run followed by a NEW deletion run starts a second replacement; buffering
// through would zip the first run's additions against the second run's deletions.
test("split starts a new replacement when a deletion follows an addition", () => {
  const patch = ["--- a/x", "+++ b/x", "@@ -1,2 +1,2 @@", "-a", "+b", "-c", "+d", ""].join("\n");
  const pairs = buildRows(parsePatch(patch), "split").filter(
    (r): r is Extract<Row, { kind: "pair" }> => r.kind === "pair",
  );
  assert.deepEqual(
    pairs.map((p) => [p.left?.text ?? null, p.right?.text ?? null]),
    [
      ["a", "b"],
      ["c", "d"],
    ],
  );
});

test("headings are indexed for navigation in both modes", () => {
  const patch = [
    "diff --git a/a.ts b/a.ts",
    "--- a/a.ts",
    "+++ b/a.ts",
    "@@ -1 +1 @@",
    "-x",
    "+y",
    "@@ -9 +9 @@",
    "-p",
    "+q",
    "diff --git a/b.ts b/b.ts",
    "--- a/b.ts",
    "+++ b/b.ts",
    "@@ -1 +1 @@",
    "-m",
    "+n",
    "",
  ].join("\n");
  const rows = buildRows(parsePatch(patch), "unified");
  assert.equal(fileRowIndexes(rows).length, 2);
  assert.equal(hunkRowIndexes(rows).length, 3);
  for (const i of hunkRowIndexes(rows)) assert.equal(at(rows, i, "rows").kind, "hunk");
  for (const i of fileRowIndexes(rows)) assert.equal(at(rows, i, "rows").kind, "file");
});

test("navigation steps to the next and previous entry", () => {
  const marks = [2, 8, 14];
  assert.equal(nextIndexAfter(marks, 0), 2);
  assert.equal(nextIndexAfter(marks, 2), 8);
  assert.equal(prevIndexBefore(marks, 14), 8);
  assert.equal(prevIndexBefore(marks, 8), 2);
});

// Holding still at the end reads as "you are at the last hunk"; wrapping to the top reads as
// a scroll bug.
test("navigation clamps at both ends instead of wrapping", () => {
  const marks = [2, 8, 14];
  assert.equal(nextIndexAfter(marks, 14), 14);
  assert.equal(nextIndexAfter(marks, 999), 14);
  assert.equal(prevIndexBefore(marks, 2), 2);
  assert.equal(prevIndexBefore(marks, 0), 2);
});

test("navigation over an empty stream yields null rather than throwing", () => {
  assert.equal(nextIndexAfter([], 0), null);
  assert.equal(prevIndexBefore([], 0), null);
});

// A binary or mode-only file has no hunks but must still appear, or it silently vanishes from
// a review that changed it.
test("a file with no hunks still gets its heading row", () => {
  const patch = [
    "diff --git a/logo.png b/logo.png",
    "Binary files a/logo.png and b/logo.png differ",
    "",
  ].join("\n");
  const rows = buildRows(parsePatch(patch), "unified");
  assert.equal(rows.length, 1);
  assert.equal(at(rows, 0, "rows").kind, "file");
});
