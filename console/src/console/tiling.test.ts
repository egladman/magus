// tiling.test.ts - the Pane tree ops are pure, so the layout algebra is tested here
// without a DOM. Run: `pnpm run test`. Covers the tree surgery a tiling UI is easy to
// get wrong: which leaf gets replaced, sibling promotion on close, and ratio clamping.

import { test } from "node:test";
import assert from "node:assert/strict";
import {
  leaves, splitLeaf, closePane, setRatio, setLeafPage, pickAxis, neighborInDirection,
  swapLeaves, siblingLeafId,
  MIN_RATIO, MAX_RATIO, type Pane, type Rect,
} from "./tiling";

const leaf = (id: string, pageId = id): Pane => ({ kind: "leaf", id, pageId });

test("leaves lists every leaf in order", () => {
  const t = splitLeaf(leaf("a"), "a", "row", "s1", { id: "b", pageId: "b" });
  assert.deepEqual(leaves(t).map((l) => l.id), ["a", "b"]);
});

test("splitLeaf replaces the target leaf with a split of [old, new]", () => {
  const t = splitLeaf(leaf("a"), "a", "row", "s1", { id: "b", pageId: "b" });
  assert.equal(t.kind, "split");
  if (t.kind !== "split") return;
  assert.equal(t.dir, "row");
  assert.equal(t.ratio, 0.5);
  assert.deepEqual([t.a, t.b].map((p) => p.kind === "leaf" && p.id), ["a", "b"]);
});

test("splitLeaf with first=true puts the new leaf on the a-side", () => {
  const t = splitLeaf(leaf("a"), "a", "col", "s1", { id: "b", pageId: "b" }, true);
  if (t.kind !== "split") { assert.fail("expected split"); return; }
  assert.deepEqual([t.a, t.b].map((p) => p.kind === "leaf" && p.id), ["b", "a"]);
});

test("splitLeaf targets a nested leaf, leaving siblings intact", () => {
  let t = splitLeaf(leaf("a"), "a", "row", "s1", { id: "b", pageId: "b" });
  t = splitLeaf(t, "b", "col", "s2", { id: "c", pageId: "c" });
  assert.deepEqual(leaves(t).map((l) => l.id), ["a", "b", "c"]);
});

test("splitLeaf on an unknown target returns the tree unchanged", () => {
  const t = leaf("a");
  assert.equal(splitLeaf(t, "zzz", "row", "s1", { id: "b", pageId: "b" }), t);
});

test("closePane promotes the sibling when a split child closes", () => {
  const t = splitLeaf(leaf("a"), "a", "row", "s1", { id: "b", pageId: "b" });
  const after = closePane(t, "b");
  assert.deepEqual(after, leaf("a"));
});

test("closePane on the only pane returns null", () => {
  assert.equal(closePane(leaf("a"), "a"), null);
});

test("closePane on a deep leaf collapses only its parent split", () => {
  let t = splitLeaf(leaf("a"), "a", "row", "s1", { id: "b", pageId: "b" });
  t = splitLeaf(t, "b", "col", "s2", { id: "c", pageId: "c" }); // a | (b / c)
  const after = closePane(t, "c");
  // s2 collapses to b; s1 (a | b) remains.
  assert.equal(after?.kind, "split");
  assert.deepEqual(after && leaves(after).map((l) => l.id), ["a", "b"]);
});

test("closePane on an unknown id leaves the tree unchanged", () => {
  const t = splitLeaf(leaf("a"), "a", "row", "s1", { id: "b", pageId: "b" });
  assert.deepEqual(leaves(closePane(t, "zzz") as Pane).map((l) => l.id), ["a", "b"]);
});

test("setRatio sets the matching split and clamps to the drag limits", () => {
  const t = splitLeaf(leaf("a"), "a", "row", "s1", { id: "b", pageId: "b" });
  const wide = setRatio(t, "s1", 0.73);
  assert.equal(wide.kind === "split" && wide.ratio, 0.73);
  const shut = setRatio(t, "s1", -1);
  assert.equal(shut.kind === "split" && shut.ratio, MIN_RATIO);
  const full = setRatio(t, "s1", 2);
  assert.equal(full.kind === "split" && full.ratio, MAX_RATIO);
});

test("setRatio on an unknown split id changes nothing", () => {
  const t = splitLeaf(leaf("a"), "a", "row", "s1", { id: "b", pageId: "b" });
  assert.deepEqual(setRatio(t, "zzz", 0.9), t);
});

test("setLeafPage fills an empty pane's surface and leaves siblings alone", () => {
  let t = splitLeaf(leaf("a", "dashboard"), "a", "row", "s1", { id: "p2", pageId: "" });
  t = setLeafPage(t, "p2", "logs");
  assert.deepEqual(leaves(t).map((l) => [l.id, l.pageId]), [["a", "dashboard"], ["p2", "logs"]]);
});

test("setLeafPage on an unknown id changes nothing", () => {
  const t = leaf("a", "dashboard");
  assert.deepEqual(setLeafPage(t, "zzz", "logs"), t);
});

test("pickAxis splits a wide pane into a row and a tall pane into a col", () => {
  assert.equal(pickAxis({ left: 0, top: 0, width: 1200, height: 400 }), "row");
  assert.equal(pickAxis({ left: 0, top: 0, width: 400, height: 900 }), "col");
  // A square (width === height) prefers a row (the >= tie-break in pickAxis).
  assert.equal(pickAxis({ left: 0, top: 0, width: 500, height: 500 }), "row");
});

// A 2x2 grid of panes centered so the geometry is unambiguous: from the top-left pane, "right"
// is top-right, "down" is bottom-left, and there is nothing further "left" or "up".
const grid: { id: string; rect: Rect }[] = [
  { id: "tl", rect: { left: 0, top: 0, width: 100, height: 100 } },
  { id: "tr", rect: { left: 100, top: 0, width: 100, height: 100 } },
  { id: "bl", rect: { left: 0, top: 100, width: 100, height: 100 } },
  { id: "br", rect: { left: 100, top: 100, width: 100, height: 100 } },
];
const others = (id: string): { id: string; rect: Rect }[] => grid.filter((c) => c.id !== id);
const tl = grid[0].rect;

test("neighborInDirection picks the nearest centroid in the requested half-plane", () => {
  assert.equal(neighborInDirection(tl, others("tl"), "right"), "tr");
  assert.equal(neighborInDirection(tl, others("tl"), "down"), "bl");
});

test("neighborInDirection returns null when nothing lies that way", () => {
  assert.equal(neighborInDirection(tl, others("tl"), "left"), null);
  assert.equal(neighborInDirection(tl, others("tl"), "up"), null);
});

test("neighborInDirection prefers the closer of two panes in the same direction", () => {
  const from: Rect = { left: 0, top: 0, width: 100, height: 100 };
  const near = { id: "near", rect: { left: 100, top: 0, width: 100, height: 100 } };
  const far = { id: "far", rect: { left: 400, top: 0, width: 100, height: 100 } };
  assert.equal(neighborInDirection(from, [far, near], "right"), "near");
});

// --- swapLeaves ---------------------------------------------------------------

test("swapLeaves swaps two leaves and leaves the rest intact", () => {
  let t = splitLeaf(leaf("a"), "a", "row", "s1", { id: "b", pageId: "b" });
  t = splitLeaf(t, "b", "col", "s2", { id: "c", pageId: "c" }); // a | (b / c)
  const swapped = swapLeaves(t, "a", "c");
  // a and c trade places: the position that held "a" now holds "c" (its id AND pageId travel with
  // it), the position that held "c" now holds "a", and "b" - untouched - stays exactly where it was.
  assert.deepEqual(leaves(swapped).map((l) => [l.id, l.pageId]), [["c", "c"], ["b", "b"], ["a", "a"]]);
});

test("swapLeaves with an unknown id is a no-op", () => {
  const t = splitLeaf(leaf("a"), "a", "row", "s1", { id: "b", pageId: "b" });
  assert.equal(swapLeaves(t, "a", "zzz"), t);
  assert.equal(swapLeaves(t, "zzz", "a"), t);
});

test("swapLeaves preserves the ids and pageIds of the swapped leaves", () => {
  const t = splitLeaf(leaf("a", "dashboard"), "a", "row", "s1", { id: "b", pageId: "logs" });
  const swapped = swapLeaves(t, "a", "b");
  assert.deepEqual(leaves(swapped).map((l) => [l.id, l.pageId]), [["b", "logs"], ["a", "dashboard"]]);
});

test("swapLeaves with the same id twice is a no-op", () => {
  const t = splitLeaf(leaf("a"), "a", "row", "s1", { id: "b", pageId: "b" });
  assert.equal(swapLeaves(t, "a", "a"), t);
});

// --- siblingLeafId -------------------------------------------------------------

test("siblingLeafId returns the sibling's first leaf in a simple split", () => {
  const t = splitLeaf(leaf("a"), "a", "row", "s1", { id: "b", pageId: "b" });
  assert.equal(siblingLeafId(t, "a"), "b");
  assert.equal(siblingLeafId(t, "b"), "a");
});

test("siblingLeafId returns null for a lone root leaf", () => {
  assert.equal(siblingLeafId(leaf("a"), "a"), null);
});

test("siblingLeafId returns null for an unknown id", () => {
  const t = splitLeaf(leaf("a"), "a", "row", "s1", { id: "b", pageId: "b" });
  assert.equal(siblingLeafId(t, "zzz"), null);
});

test("siblingLeafId picks the correct sibling in a nested tree", () => {
  let t = splitLeaf(leaf("a"), "a", "row", "s1", { id: "b", pageId: "b" });
  t = splitLeaf(t, "b", "col", "s2", { id: "c", pageId: "c" }); // a | (b / c)
  assert.equal(siblingLeafId(t, "a"), "b"); // a's sibling subtree is (b/c); its first leaf is b
  assert.equal(siblingLeafId(t, "b"), "c"); // b's immediate sibling is c
  assert.equal(siblingLeafId(t, "c"), "b"); // c's immediate sibling is b
});
