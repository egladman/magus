import { test } from "node:test";
import assert from "node:assert/strict";
import { parsePatch } from "./parse";
import {
  buildRows,
  byHunk,
  commentKey,
  hunkRowIndexes,
  hunksRead,
  fileRowIndexes,
  nextIndexAfter,
  prevIndexBefore,
  rowOffsets,
  rowAt,
  fileOfRow,
  heightOf,
  maxLineChars,
  storyText,
  ROW_HEIGHT,
  FILE_ROW_HEIGHT,
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

// A remark read three inches from the code it is about is a remark the reader has to hold in
// their head. It belongs directly under its hunk, in the scroll geometry.
test("a comment becomes a row directly under the hunk it annotates", () => {
  const comments = [
    { id: "c1", path: "x.ts", hunk: 0, author: "agent" as const, body: "look", resolved: false },
  ];
  const rows = buildRows(parsePatch(REPLACEMENT), "unified", byHunk(comments));
  assert.deepEqual(
    rows.map((r) => r.kind),
    ["file", "hunk", "comment", "line", "line", "line", "line", "line", "line"],
  );
});

test("comments land on their own hunk, not the first one", () => {
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
    "",
  ].join("\n");
  const comments = [
    { id: "c1", path: "a.ts", hunk: 1, author: "human" as const, body: "second", resolved: false },
  ];
  const rows = buildRows(parsePatch(patch), "unified", byHunk(comments));
  const at = rows.findIndex((r) => r.kind === "comment");
  const prevHunk = rows.slice(0, at).filter((r) => r.kind === "hunk").length;
  assert.equal(prevHunk, 2, "the comment must follow the SECOND hunk header");
});

// The anchor is the hunk's index within its file. A global row index would slide the comment
// onto a different hunk the moment the generated group folded.
test("comment keys are per file and per hunk index", () => {
  assert.notEqual(commentKey("a.ts", 0), commentKey("b.ts", 0));
  assert.notEqual(commentKey("a.ts", 0), commentKey("a.ts", 1));
});

test("several comments on one hunk keep their order", () => {
  const comments = [
    { id: "c1", path: "x.ts", hunk: 0, author: "agent" as const, body: "first", resolved: false },
    { id: "c2", path: "x.ts", hunk: 0, author: "human" as const, body: "reply", resolved: false },
  ];
  const rows = buildRows(parsePatch(REPLACEMENT), "unified", byHunk(comments));
  const bodies = rows
    .filter((r): r is Extract<Row, { kind: "comment" }> => r.kind === "comment")
    .map((r) => r.comment.body);
  assert.deepEqual(bodies, ["first", "reply"]);
});

test("no comments leaves the row shape untouched", () => {
  const withNone = buildRows(parsePatch(REPLACEMENT), "unified", byHunk([]));
  const without = buildRows(parsePatch(REPLACEMENT), "unified");
  assert.deepEqual(
    withNone.map((r) => r.kind),
    without.map((r) => r.kind),
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

// Covers virtualized hunk lookup at scale and boundaries.
test("navigation preserves exact boundaries in a large hunk index", () => {
  const marks = Array.from({ length: 100_000 }, (_, i) => i * 3 + 1);
  assert.equal(nextIndexAfter(marks, 150_000), 150_001);
  assert.equal(prevIndexBefore(marks, 150_000), 149_998);
  assert.equal(nextIndexAfter(marks, -1), 1);
  assert.equal(prevIndexBefore(marks, Number.MAX_SAFE_INTEGER), 299_998);
});

test("content rows retain their hunk for lazy per-hunk rendering work", () => {
  const rows = buildRows(parsePatch(REPLACEMENT), "unified");
  const hunk = rows.find((row): row is Extract<Row, { kind: "hunk" }> => row.kind === "hunk");
  const line = rows.find((row): row is Extract<Row, { kind: "line" }> => row.kind === "line");
  assert.ok(hunk);
  assert.ok(line);
  assert.equal(line.hunk, hunk.hunk);
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

// The virtualizer positions rows from these offsets instead of measuring them, so an error here
// does not throw - it silently paints rows at the wrong y. These pin the arithmetic.

// A stand-in row of each kind. Only `kind` is read by heightOf, so the rest stays minimal.
function rowOfKind(kind: string): Row {
  return { kind } as unknown as Row;
}

test("a file row is taller than a code row, and heightOf is what says so", () => {
  assert.equal(heightOf(rowOfKind("file")), FILE_ROW_HEIGHT);
  assert.equal(heightOf(rowOfKind("line")), ROW_HEIGHT);
  assert.equal(heightOf(rowOfKind("hunk")), ROW_HEIGHT);
  assert.ok(FILE_ROW_HEIGHT > ROW_HEIGHT, "the file header must have more room than a code line");
});

test("rowOffsets accumulates each row's own height and ends with the total", () => {
  const rows = ["file", "line", "line", "file", "line"].map(rowOfKind);
  const offs = rowOffsets(rows);
  assert.deepEqual(offs, [
    0,
    FILE_ROW_HEIGHT,
    FILE_ROW_HEIGHT + ROW_HEIGHT,
    FILE_ROW_HEIGHT + 2 * ROW_HEIGHT,
    2 * FILE_ROW_HEIGHT + 2 * ROW_HEIGHT,
    2 * FILE_ROW_HEIGHT + 3 * ROW_HEIGHT,
  ]);
  // The last entry is the spacer height, which is what gives the scrollbar its length.
  assert.equal(at(offs, rows.length, "total"), 2 * FILE_ROW_HEIGHT + 3 * ROW_HEIGHT);
});

test("rowOffsets of an empty diff is just the zero total", () => {
  assert.deepEqual(rowOffsets([]), [0]);
});

test("rowAt finds the row containing a y, including exactly on a boundary", () => {
  const rows = ["file", "line", "line"].map(rowOfKind);
  const offs = rowOffsets(rows); // [0, 40, 64, 88]
  assert.equal(rowAt(offs, 0), 0);
  assert.equal(rowAt(offs, FILE_ROW_HEIGHT - 1), 0);
  // A boundary belongs to the row it STARTS, not the one it ends.
  assert.equal(rowAt(offs, FILE_ROW_HEIGHT), 1);
  assert.equal(rowAt(offs, FILE_ROW_HEIGHT + ROW_HEIGHT), 2);
});

test("rowAt clamps rather than running off either end", () => {
  const offs = rowOffsets(["file", "line"].map(rowOfKind));
  assert.equal(rowAt(offs, -50), 0, "a rubber-band scroll goes negative on macOS");
  assert.equal(rowAt(offs, 999_999), 1, "past the end answers with the last row");
  assert.equal(rowAt([0], 10), 0, "an empty diff has no row to land on");
});

test("rowAt inverts rowOffsets for every row", () => {
  const rows = ["file", "line", "hunk", "file", "line", "line", "comment"].map(rowOfKind);
  const offs = rowOffsets(rows);
  for (let i = 0; i < rows.length; i++) {
    assert.equal(rowAt(offs, at(offs, i, "offset")), i, `row ${i} must round-trip`);
  }
});

test("fileOfRow attributes every row to the file header above it", () => {
  const rows = ["file", "hunk", "line", "file", "line"].map(rowOfKind);
  assert.deepEqual(fileOfRow(rows), [0, 0, 0, 3, 3]);
});

test("fileOfRow reports -1 for rows before the first file header", () => {
  // The pinned header reads this directly, and -1 is what tells it to stay hidden rather than
  // pin row 0 of a file the reader has not reached.
  assert.deepEqual(fileOfRow(["comment", "file", "line"].map(rowOfKind)), [-1, 1, 1]);
  assert.deepEqual(fileOfRow([]), []);
});

const SHORT_LINES = [
  "diff --git a/x.ts b/x.ts",
  "--- a/x.ts",
  "+++ b/x.ts",
  "@@ -1,1 +1,1 @@",
  "-old",
  "+new",
  "",
].join("\n");

// A hunk header, a comment, and a story sentence can each be longer than every code line in
// the diff - main.ts renders all of them as .console-diff-row, sharing the same width floor, so
// maxLineChars has to consider all four kinds or the shorter ones fall short of whichever one
// is actually widest (the bug this function exists to prevent).
test("maxLineChars picks up a hunk header longer than every code line", () => {
  const longHeader = [
    "diff --git a/x.ts b/x.ts",
    "--- a/x.ts",
    "+++ b/x.ts",
    "@@ -1,1 +1,1 @@ a hunk header considerably longer than either line below it",
    "-old",
    "+new",
    "",
  ].join("\n");
  const rows = buildRows(parsePatch(longHeader), "unified");
  const hunk = rows.find((r) => r.kind === "hunk");
  assert.equal(hunk?.hunk.header.length, maxLineChars(rows));
});

test("maxLineChars picks up a comment longer than every code line", () => {
  const comments = [
    {
      id: "c1",
      path: "x.ts",
      hunk: 0,
      author: "human" as const,
      body: "a remark considerably longer than either code line in this hunk",
      resolved: false,
    },
  ];
  const rows = buildRows(parsePatch(SHORT_LINES), "unified", byHunk(comments));
  assert.equal(maxLineChars(rows), comments[0].body.length);
});

test("maxLineChars picks up a story sentence longer than every code line", () => {
  const touches = new Map([
    [
      "x.ts",
      [{ host: "claude-code", read: ["a/very/long/path/that/pushes/this/sentence/out.ts"] }],
    ],
  ]);
  const rows = buildRows(parsePatch(SHORT_LINES), "unified", undefined, touches);
  const story = rows.find((r) => r.kind === "story");
  assert.ok(story);
  assert.equal(maxLineChars(rows), storyText(story.touch).length);
});

test("maxLineChars applies in split mode too: hunk headers are emitted regardless of view mode", () => {
  const longHeader = [
    "diff --git a/x.ts b/x.ts",
    "--- a/x.ts",
    "+++ b/x.ts",
    "@@ -1,1 +1,1 @@ a hunk header considerably longer than either line below it",
    "-old",
    "+new",
    "",
  ].join("\n");
  const rows = buildRows(parsePatch(longHeader), "split");
  const hunk = rows.find((r) => r.kind === "hunk");
  assert.equal(hunk?.hunk.header.length, maxLineChars(rows));
});

test("maxLineChars of an empty row set is 0", () => {
  assert.equal(maxLineChars([]), 0);
});

// A leading tab in a `white-space: pre` monospace line does not cost one character of width
// the way every other character does - it advances to the next tab stop (8, by the browser's
// unoverridden default) - so a short but deeply-indented line can render wider than a longer,
// tab-free one of equal .length. A plain character count would have under-floored it.
test("maxLineChars expands tabs to their rendered width rather than counting them as one char", () => {
  const patch = [
    "diff --git a/x.go b/x.go",
    "--- a/x.go",
    "+++ b/x.go",
    "@@ -1,2 +1,2 @@",
    "-\t\t\tx", // three leading tabs: columns 0->8->16->24, then "x" at column 24 = width 25
    "+a line of plain text with no tabs in it at all", // .length 46, comfortably over 25
    "",
  ].join("\n");
  const rows = buildRows(parsePatch(patch), "unified");
  // The tab-heavy line (width 25) must NOT win over the 46-character plain line - this pins
  // that tabs still lose to genuinely longer plain text, not just that they count for something.
  assert.equal(maxLineChars(rows), "a line of plain text with no tabs in it at all".length);
});

test("maxLineChars: a short, deeply-indented line can still outrun a longer flat one", () => {
  const patch = [
    "diff --git a/x.go b/x.go",
    "--- a/x.go",
    "+++ b/x.go",
    "@@ -1,2 +1,2 @@",
    "-\t\t\t\t\t\tshort", // six leading tabs: column 48, then "short" = width 53
    "+a forty-six character line of flat unindented text", // shorter than 53 either way
    "",
  ].join("\n");
  const rows = buildRows(parsePatch(patch), "unified");
  assert.equal(maxLineChars(rows), 53);
});

test("hunksRead counts only hunks the current stream still holds", () => {
  const digestByRow = new Map([
    [2, "digest-a"],
    [5, "digest-b"],
    [9, "digest-c"], // folded away below - still in the map, no longer in `hunks`
  ]);
  const viewed = new Set(["digest-a", "digest-c"]);

  // Before the fold: three hunks in the stream, two marked.
  assert.equal(hunksRead([2, 5, 9], digestByRow, viewed), 2);

  // After folding the generated group away, row 9 leaves the stream. Its digest is still
  // in `viewed` (marks are never pruned there) and still in `digestByRow` (never cleared for a
  // row that simply stopped being in view) - this is the exact shape the "12/8" bug had, and the
  // count must drop with the denominator instead of over-reporting against it.
  assert.equal(hunksRead([2, 5], digestByRow, viewed), 1);
});

test("hunksRead undercounts rather than guesses at a hunk with no digest yet", () => {
  // Row 7 is in the stream but has never scrolled into view, so its digest was never computed -
  // even though a mark for that exact content exists from an earlier session. The honest answer
  // is "not confirmed read", not a guess that could overstate the count.
  const digestByRow = new Map([[3, "digest-a"]]);
  const viewed = new Set(["digest-a", "digest-would-match-row-7-once-known"]);
  assert.equal(hunksRead([3, 7], digestByRow, viewed), 1);
});

test("hunksRead is zero for an empty stream or an empty viewed set", () => {
  assert.equal(hunksRead([], new Map(), new Set()), 0);
  assert.equal(hunksRead([1, 2, 3], new Map([[1, "d"]]), new Set()), 0);
});
