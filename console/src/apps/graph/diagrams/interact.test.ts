// interact.test.ts - the figure's camera and focus arithmetic, without a DOM.

import assert from "node:assert/strict";
import { test } from "node:test";
import {
  FIT_PAD,
  MAX_SCALE,
  MIN_SCALE,
  clientToFigure,
  figureKey,
  fitBox,
  formatViewBox,
  glideMs,
  neighbours,
  panBy,
  parseEdge,
  parseViewBox,
  readingOrder,
  zoomScale,
  wheelFactor,
  zoomAbout,
  type ViewBox,
} from "./interact";

const NATURAL: ViewBox = { x: 0, y: 0, w: 400, h: 200 };
const key = (
  k: string,
  mods: Partial<{ ctrlKey: boolean; metaKey: boolean; altKey: boolean }> = {},
) => ({
  key: k,
  ctrlKey: false,
  metaKey: false,
  altKey: false,
  ...mods,
});

test("a viewBox round-trips and a malformed one reads as none", () => {
  assert.deepEqual(parseViewBox("0 0 480 280"), { x: 0, y: 0, w: 480, h: 280 });
  assert.deepEqual(parseViewBox("-8,4 10.5 2"), { x: -8, y: 4, w: 10.5, h: 2 });
  assert.equal(formatViewBox({ x: 1.234, y: 0, w: 480, h: 280 }), "1.23 0 480 280");
  assert.equal(parseViewBox("0 0 0 280"), null, "a zero-width box draws nothing");
  assert.equal(parseViewBox("0 0 480"), null);
  assert.equal(parseViewBox(null), null);
});

test("zooming about a point keeps that point where it was on screen", () => {
  const p = { x: 100, y: 50 };
  const z = zoomAbout(NATURAL, NATURAL, 2, p);
  assert.deepEqual(z, { x: 50, y: 25, w: 200, h: 100 });
  // The point's fraction across the box is unchanged, which is what "stays put" means.
  assert.equal((p.x - z.x) / z.w, (p.x - NATURAL.x) / NATURAL.w);
  assert.equal((p.y - z.y) / z.h, (p.y - NATURAL.y) / NATURAL.h);
});

test("zoom clamps to the scale range in both directions", () => {
  let v = NATURAL;
  for (let i = 0; i < 40; i++) v = zoomAbout(NATURAL, v, 2, { x: 200, y: 100 });
  assert.ok(Math.abs(zoomScale(NATURAL, v) - MAX_SCALE) < 1e-9);
  for (let i = 0; i < 40; i++) v = zoomAbout(NATURAL, v, 0.5, { x: 200, y: 100 });
  assert.ok(Math.abs(zoomScale(NATURAL, v) - MIN_SCALE) < 1e-9);
});

test("fit frames the whole figure with the pad around it", () => {
  assert.deepEqual(fitBox(NATURAL), {
    x: -FIT_PAD,
    y: -FIT_PAD,
    w: 400 + 2 * FIT_PAD,
    h: 200 + 2 * FIT_PAD,
  });
});

test("a pointer maps to figure units through the letterbox", () => {
  // A 400x200 figure in an 800x800 frame: meet scales by 2 and leaves 200px bands above and below.
  const rect = { left: 10, top: 20, width: 800, height: 800 };
  assert.deepEqual(clientToFigure(NATURAL, rect, 10, 220), { x: 0, y: 0 });
  assert.deepEqual(clientToFigure(NATURAL, rect, 810, 620), { x: 400, y: 200 });
});

test("a drag pans by the pixels moved, in figure units", () => {
  const rect = { left: 0, top: 0, width: 200, height: 100 };
  assert.deepEqual(panBy(NATURAL, rect, 10, -5), { x: -20, y: 10, w: 400, h: 200 });
});

test("a ctrl-wheel toward the reader zooms in and lines count as much as pixels", () => {
  assert.ok(wheelFactor(-100, 0) > 1);
  assert.ok(wheelFactor(100, 0) < 1);
  assert.equal(wheelFactor(3, 1), wheelFactor(48, 0));
});

test("data-edge splits at the first arrow and rejects a half edge", () => {
  assert.deepEqual(parseEdge("libs-lib->libs-core"), ["libs-lib", "libs-core"]);
  assert.equal(parseEdge("a->"), null);
  assert.equal(parseEdge("->b"), null);
  assert.equal(parseEdge("ab"), null);
});

test("focus keeps one declared hop in either direction", () => {
  const edges = [
    ["a", "b"],
    ["c", "a"],
    ["b", "d"],
  ] as const;
  assert.deepEqual([...neighbours(edges, "a")].sort(), ["a", "b", "c"]);
});

test("a node with no declared edges highlights nothing but itself", () => {
  const edges = [["a", "b"]] as const;
  assert.deepEqual([...neighbours(edges, "lone")], ["lone"]);
});

test("reading order is rows top to bottom, then left to right", () => {
  const order = readingOrder([
    { id: "c", x: 32, y: 200 },
    { id: "b", x: 288, y: 118 },
    { id: "a", x: 32, y: 112 },
    { id: "d", x: 288, y: 20 },
  ]);
  assert.deepEqual(order, ["d", "a", "b", "c"], "a and b sit within a row's slack of each other");
});

test("figure keys: f, + - 0 and Esc, and never a modified key", () => {
  assert.equal(figureKey(key("f")), "fit");
  assert.equal(figureKey(key("+")), "zoom-in");
  assert.equal(figureKey(key("=")), "zoom-in");
  assert.equal(figureKey(key("-")), "zoom-out");
  assert.equal(figureKey(key("0")), "actual");
  assert.equal(figureKey(key("Escape")), "clear");
  assert.equal(figureKey(key("l")), null, "l is the graph explorer's, not ours");
  // ctrl/cmd with + - 0 is the browser's page zoom and must pass through.
  assert.equal(figureKey(key("+", { ctrlKey: true })), null);
  assert.equal(figureKey(key("0", { metaKey: true })), null);
  assert.equal(figureKey(key("f", { altKey: true })), null);
});

test("reduced motion snaps: a glide takes no time", () => {
  assert.equal(glideMs(true), 0);
  assert.ok(glideMs(false) > 0);
});
