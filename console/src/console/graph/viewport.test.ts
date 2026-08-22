// viewport.test.ts - the inset rule and the framing arithmetic behind Fit. Run: `pnpm run test`.

import assert from "node:assert/strict";
import { test } from "node:test";
import { type Rect, fitTransform, overlayInsets, recenterOn, usableCenter } from "./viewport";

const STAGE: Rect = { left: 0, top: 0, right: 600, bottom: 400 };
const rect = (left: number, top: number, right: number, bottom: number): Rect => ({
  left,
  top,
  right,
  bottom,
});

test("a left-edge panel insets only the left", () => {
  const legend = rect(10, 10, 130, 380); // the legend: tall, pinned left
  assert.deepEqual(overlayInsets(STAGE, [legend]), { left: 130, right: 0, top: 0, bottom: 0 });
});

test("a top-right toolbar insets the top, not the whole right margin", () => {
  // Charging the right edge here would reserve 480px of margin for a 40px-tall button row.
  const tools = rect(420, 10, 590, 40);
  assert.deepEqual(overlayInsets(STAGE, [tools]), { left: 0, right: 0, top: 40, bottom: 0 });
});

test("insets accumulate as the max per edge across overlays", () => {
  const insets = overlayInsets(STAGE, [
    rect(10, 10, 130, 380),
    rect(10, 10, 90, 30),
    rect(420, 10, 590, 40),
    rect(150, 44, 400, 70),
  ]);
  assert.deepEqual(insets, { left: 130, right: 0, top: 70, bottom: 0 });
});

test("a hidden overlay measures zero and insets nothing", () => {
  assert.deepEqual(overlayInsets(STAGE, [rect(0, 0, 0, 0)]), {
    left: 0,
    right: 0,
    top: 0,
    bottom: 0,
  });
});

test("an overlay outside the stage insets nothing", () => {
  // The explain card sits BESIDE the canvas, not over it.
  assert.deepEqual(overlayInsets(STAGE, [rect(600, 0, 900, 400)]), {
    left: 0,
    right: 0,
    top: 0,
    bottom: 0,
  });
});

test("an overlay wider than the stage cannot inset past the stage", () => {
  const insets = overlayInsets(STAGE, [rect(-50, 0, 900, 30)]);
  assert.ok(insets.top <= 400 && insets.left <= 600);
});

test("fit centers in the usable box, not the viewport", () => {
  const box = { minX: 0, minY: 0, maxX: 100, maxY: 100 };
  const bare = fitTransform(box, 600, 400);
  const inset = fitTransform(box, 600, 400, { left: 130, right: 0, top: 0, bottom: 0 });
  // World center (50,50) lands at the viewport center without chrome, and 65px right of it
  // once a 130px panel covers the left edge.
  assert.equal(bare.x + 50 * bare.k, 300);
  assert.equal(inset.x + 50 * inset.k, 130 + (600 - 130) / 2);
  assert.ok(inset.x > bare.x, "an inset left edge pushes the frame right");
});

test("fit scales to the usable box, so chrome shrinks the zoom", () => {
  const box = { minX: 0, minY: 0, maxX: 1000, maxY: 100 };
  const bare = fitTransform(box, 600, 400);
  const inset = fitTransform(box, 600, 400, { left: 200, right: 0, top: 0, bottom: 0 });
  assert.ok(inset.k < bare.k, "less room means a smaller scale");
  assert.ok(Math.abs(inset.k - (600 - 200 - 96) / 1000) < 1e-9);
});

test("scale is clamped at both ends", () => {
  const huge = fitTransform({ minX: 0, minY: 0, maxX: 1e9, maxY: 1e9 }, 600, 400);
  assert.equal(huge.k, 0.1);
  const tiny = fitTransform({ minX: 0, minY: 0, maxX: 1, maxY: 1 }, 600, 400);
  assert.equal(tiny.k, 8);
});

test("a single point frames at max scale rather than dividing by zero", () => {
  const t = fitTransform({ minX: 7, minY: 7, maxX: 7, maxY: 7 }, 600, 400);
  assert.equal(t.k, 8);
  assert.equal(t.x + 7 * t.k, 300);
  assert.equal(t.y + 7 * t.k, 200);
});

test("a zero span on one axis still fits the other", () => {
  const t = fitTransform({ minX: 0, minY: 5, maxX: 1000, maxY: 5 }, 600, 400);
  assert.ok(Math.abs(t.k - (600 - 96) / 1000) < 1e-9);
});

test("chrome wider than the viewport still yields a usable transform", () => {
  const t = fitTransform({ minX: 0, minY: 0, maxX: 100, maxY: 100 }, 600, 400, {
    left: 500,
    right: 500,
    top: 0,
    bottom: 0,
  });
  assert.ok(Number.isFinite(t.k) && t.k > 0);
  assert.ok(Number.isFinite(t.x) && Number.isFinite(t.y));
});

test("the simulation settles in the usable center", () => {
  assert.deepEqual(usableCenter(600, 400), { x: 300, y: 200 });
  assert.deepEqual(usableCenter(600, 400, { left: 130, right: 0, top: 40, bottom: 0 }), {
    x: 130 + 235,
    y: 40 + 180,
  });
});

test("recentring keeps the fit's scale and puts the focus in the usable center", () => {
  const insets = { left: 130, right: 0, top: 40, bottom: 0 };
  const fit = fitTransform({ minX: 0, minY: 0, maxX: 1000, maxY: 800 }, 600, 400, insets);
  const t = recenterOn(fit, { x: 900, y: 700 }, 600, 400, insets);
  assert.equal(t.k, fit.k);
  assert.deepEqual({ x: t.x + 900 * t.k, y: t.y + 700 * t.k }, usableCenter(600, 400, insets));
});

test("recentring on the middle of the box it fitted is the fit itself", () => {
  const box = { minX: 0, minY: 0, maxX: 1000, maxY: 800 };
  const fit = fitTransform(box, 600, 400);
  const t = recenterOn(fit, { x: 500, y: 400 }, 600, 400);
  assert.ok(Math.abs(t.x - fit.x) < 1e-9 && Math.abs(t.y - fit.y) < 1e-9);
});
