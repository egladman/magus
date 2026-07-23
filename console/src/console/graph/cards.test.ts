// cards.test.ts - pure-math coverage for cards.ts: measureCards' width
// clamping and ellipsize's fit/truncate behavior. Canvas 2D is not available
// under plain node, so ctx is faked with a minimal structural object whose
// measureText mirrors a monospace-ish metric (width = 6px per character) -
// enough to exercise the clamp and ellipsize logic deterministically without
// a real canvas.

import { test } from "node:test";
import assert from "node:assert/strict";
import { CARD_H, CARD_MAX_W, CARD_MIN_W, ellipsize, measureCards } from "./cards.js";
import type { GNode } from "./types.js";

function fakeCtx(): CanvasRenderingContext2D {
  return {
    font: "",
    measureText: (s: string) => ({ width: s.length * 6 }),
  } as unknown as CanvasRenderingContext2D;
}

function fakeNode(id: string, label: string): GNode {
  return {
    id,
    kind: "target",
    label,
    degree: 0,
    r: 0,
    x: 0,
    y: 0,
    fx: null,
    fy: null,
  } as unknown as GNode;
}

test("measureCards: a very long label clamps to CARD_MAX_W", () => {
  const ctx = fakeCtx();
  const nodes = [fakeNode("a", "a".repeat(80))];
  measureCards(ctx, nodes, "sans");
  assert.equal(nodes[0].w, CARD_MAX_W);
  assert.equal(nodes[0].h, CARD_H);
});

test("measureCards: a 1-char label clamps up to CARD_MIN_W", () => {
  const ctx = fakeCtx();
  const nodes = [fakeNode("a", "x")];
  measureCards(ctx, nodes, "sans");
  assert.equal(nodes[0].w, CARD_MIN_W);
  assert.equal(nodes[0].h, CARD_H);
});

test("measureCards: a mid-length label sits strictly between MIN and MAX", () => {
  const ctx = fakeCtx();
  // 20 chars * 6px + 2*10 padding = 140, comfortably inside [96, 200].
  const nodes = [fakeNode("a", "a".repeat(20))];
  measureCards(ctx, nodes, "sans");
  assert.equal(nodes[0].w, 140);
});

test("ellipsize: returns the original string when it already fits", () => {
  const ctx = fakeCtx();
  const text = "short";
  assert.equal(ellipsize(ctx, text, 200), text);
});

test("ellipsize: returns a shorter mid-ellipsis string when it does not fit", () => {
  const ctx = fakeCtx();
  const text = "a-very-long-target-name-that-does-not-fit";
  const out = ellipsize(ctx, text, 100);
  assert.ok(out.length < text.length);
  assert.ok(out.includes("..."));
  // Middle-ellipsis: keeps a prefix and a suffix of the original text.
  const [head, tail] = out.split("...");
  assert.ok(text.startsWith(head));
  assert.ok(text.endsWith(tail));
});

test("ellipsize: deterministic across repeated calls on the same input", () => {
  const ctx = fakeCtx();
  const text = "another-quite-long-identifier-for-a-target";
  const first = ellipsize(ctx, text, 90);
  const second = ellipsize(ctx, text, 90);
  assert.equal(first, second);
});
