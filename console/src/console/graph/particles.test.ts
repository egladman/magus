// particles.test.ts - pure-math and module-state tests for the motion layer. Every test
// calls resetMotion() first so state does not leak between tests (particles.ts holds
// module-level flow edges and pulses by design - see particles.ts's header comment).

import { test } from "node:test";
import assert from "node:assert/strict";
import {
  EDGE_CAP,
  type FlowEdge,
  particleAlpha,
  resetMotion,
  samplePolyline,
  setFlowEdges,
  setPulses,
  tick,
} from "./particles.js";

test("samplePolyline: segment-boundary case (10 then 30 long, t=0.25 lands at end of first segment)", () => {
  const pts = [
    { x: 0, y: 0 },
    { x: 10, y: 0 },
    { x: 10, y: 30 },
  ];
  const p = samplePolyline(pts, 0.25); // 0.25 * 40 = 10 = exactly the first segment's length
  assert.equal(p.x, 10);
  assert.equal(p.y, 0);
});

test("samplePolyline: midpoint of the first segment", () => {
  const pts = [
    { x: 0, y: 0 },
    { x: 10, y: 0 },
    { x: 10, y: 30 },
  ];
  const p = samplePolyline(pts, 0.125); // 0.125 * 40 = 5 = halfway through the first segment
  assert.equal(p.x, 5);
  assert.equal(p.y, 0);
});

test("samplePolyline: midpoint of the second segment", () => {
  const pts = [
    { x: 0, y: 0 },
    { x: 10, y: 0 },
    { x: 10, y: 30 },
  ];
  const p = samplePolyline(pts, 0.625); // 0.625 * 40 = 25 = 10 + 15, halfway through segment 2
  assert.equal(p.x, 10);
  assert.equal(p.y, 15);
});

test("samplePolyline: t<=0 clamps to the first point, t>=1 clamps to the last point", () => {
  const pts = [
    { x: 0, y: 0 },
    { x: 10, y: 0 },
    { x: 10, y: 30 },
  ];
  assert.deepEqual(samplePolyline(pts, 0), { x: 0, y: 0 });
  assert.deepEqual(samplePolyline(pts, -1), { x: 0, y: 0 });
  assert.deepEqual(samplePolyline(pts, 1), { x: 10, y: 30 });
  assert.deepEqual(samplePolyline(pts, 5), { x: 10, y: 30 });
});

test("particleAlpha: 0 at the ends, ~0.8 in the middle, ramps over the first/last 10%", () => {
  assert.equal(particleAlpha(0), 0);
  assert.equal(particleAlpha(1), 0);
  assert.equal(particleAlpha(0.5), 0.8);
  assert.ok(particleAlpha(0.05) > 0 && particleAlpha(0.05) < 0.8); // rising in the first 10%
  assert.ok(particleAlpha(0.95) > 0 && particleAlpha(0.95) < 0.8); // falling in the last 10%
  assert.equal(particleAlpha(0.1), 0.8); // flat from 10% onward
  assert.equal(particleAlpha(0.9), 0.8); // flat up to 90%
});

test("tick: determinism - two calls at the same nowMs return identical flowPoints", () => {
  resetMotion();
  const edges: FlowEdge[] = [
    {
      pts: [
        { x: 0, y: 0 },
        { x: 100, y: 0 },
      ],
    },
    {
      pts: [
        { x: 0, y: 50 },
        { x: 50, y: 50 },
        { x: 100, y: 50 },
      ],
    },
  ];
  setFlowEdges(edges);
  const a = tick(12345);
  const b = tick(12345);
  assert.ok(a && b);
  assert.deepEqual(a.flowPoints, b.flowPoints);
});

test("tick: returns null when there are no flow edges and no live pulses", () => {
  resetMotion();
  assert.equal(tick(1000), null);
});

test("tick: produces 2 particles per flow edge", () => {
  resetMotion();
  setFlowEdges([
    {
      pts: [
        { x: 0, y: 0 },
        { x: 100, y: 0 },
      ],
    },
  ]);
  const result = tick(0);
  assert.ok(result);
  assert.equal(result.flowPoints.length, 2);
});

test("tick: pulse pruning - a pulse set at now is dropped by tick(now + 901)", () => {
  resetMotion();
  setPulses(["a"], 1000);
  const before = tick(1000);
  assert.ok(before);
  assert.ok(before.pulses.has("a"));

  const after = tick(1901); // age = 901ms > 900ms lifetime, and no flow edges
  assert.equal(after, null);
});

test("tick: pulse progress reflects age / lifetime and survives when other pulses are live", () => {
  resetMotion();
  setPulses(["a", "b"], 0);
  const result = tick(450); // half the 900ms lifetime
  assert.ok(result);
  assert.equal(result.pulses.get("a"), 0.5);
  assert.equal(result.pulses.get("b"), 0.5);
});

test("setFlowEdges: over-cap (EDGE_CAP+1) returns false and stores nothing - tick reports null", () => {
  resetMotion();
  const edges: FlowEdge[] = Array.from({ length: EDGE_CAP + 1 }, (_, i) => ({
    pts: [
      { x: i, y: 0 },
      { x: i + 1, y: 0 },
    ],
  }));
  assert.equal(setFlowEdges(edges), false);
  assert.equal(tick(0), null);
});

test("setFlowEdges: exactly EDGE_CAP edges is accepted (boundary, not capped) and returns true", () => {
  resetMotion();
  const edges: FlowEdge[] = Array.from({ length: EDGE_CAP }, (_, i) => ({
    pts: [
      { x: i, y: 0 },
      { x: i + 1, y: 0 },
    ],
  }));
  assert.equal(setFlowEdges(edges), true);
  const result = tick(0);
  assert.ok(result);
  assert.equal(result.flowPoints.length, EDGE_CAP * 2);
});

test("setFlowEdges: null or empty returns false", () => {
  resetMotion();
  assert.equal(setFlowEdges(null), false);
  assert.equal(setFlowEdges([]), false);
});

test("resetMotion: clears both flow edges and pulses", () => {
  resetMotion();
  setFlowEdges([
    {
      pts: [
        { x: 0, y: 0 },
        { x: 10, y: 0 },
      ],
    },
  ]);
  setPulses(["a"], 0);
  assert.ok(tick(0) !== null);
  resetMotion();
  assert.equal(tick(0), null);
});
