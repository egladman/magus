// tiling.test.ts - the Pane tree ops are pure, so the layout algebra is tested here
// without a DOM. Run: `pnpm run test`. Covers the tree surgery a tiling UI is easy to
// get wrong: which leaf gets replaced, sibling promotion on close, and ratio clamping.

import { test } from "node:test";
import assert from "node:assert/strict";
import {
  leaves, splitLeaf, closePane, setRatio, MIN_RATIO, MAX_RATIO, type Pane,
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
