// radial.test.ts - pure-math invariants for the radial ego layout: ring 1 is
// evenly spaced starting at 12 o'clock, deeper rings keep a parent's children
// angularly contiguous, the layout is deterministic across runs, and nodes
// beyond RADIAL_MAX_RINGS are left unplaced rather than parked at a fallback
// angle.

import { test } from "node:test";
import assert from "node:assert/strict";
import { layoutRadial, RADIAL_MAX_RINGS } from "./radial.js";
import type { GNode } from "./types.js";

function makeNode(id: string): GNode {
  return {
    id,
    kind: "node",
    label: id,
    degree: 0,
    r: 4,
    x: 0,
    y: 0,
    fx: null,
    fy: null,
  } as unknown as GNode;
}

function angleOf(n: GNode): number {
  return Math.atan2(n.fy as number, n.fx as number);
}

test("layoutRadial: star fixture places ring 1 evenly spaced from 12 o'clock", () => {
  const ids = ["a", "b", "c", "d", "e", "f"];
  const nodes = [makeNode("center"), ...ids.map(makeNode)];
  const adj = new Map<string, Set<string>>();
  adj.set("center", new Set(ids));
  for (const id of ids) adj.set(id, new Set(["center"]));

  const { rings } = layoutRadial("center", nodes, adj);
  assert.equal(rings[0].length, 1);
  assert.equal(rings[1].length, 6);

  const byId = new Map(nodes.map((n) => [n.id, n]));
  const angles = ids.map((id) => angleOf(byId.get(id) as GNode));
  const step = Math.PI / 3;
  const twoPi = 2 * Math.PI;
  for (let i = 1; i < angles.length; i++) {
    // atan2 wraps to (-PI, PI], so normalize the raw gap into [0, 2*PI)
    // before comparing - the underlying angle assignment still advances by
    // exactly PI/3 each step, it just crosses the wrap for the last member.
    const rawGap = angles[i] - angles[i - 1];
    const gap = ((rawGap % twoPi) + twoPi) % twoPi;
    assert.ok(Math.abs(gap - step) < 1e-9, `gap ${i} is not PI/3`);
  }
  // First member by id ("a") sits at -PI/2 (12 o'clock): cos ~ 0, sin ~ -1.
  const first = byId.get("a") as GNode;
  const r = 150; // RADIAL_RING_R, checked structurally via magnitude below
  assert.ok(Math.abs((first.fx as number) / r) < 1e-9);
  assert.ok(Math.abs((first.fy as number) / r + 1) < 1e-9);
});

test("layoutRadial: ring 2 keeps a parent's children in a contiguous angular span", () => {
  const nodes = [
    makeNode("center"),
    makeNode("p1"),
    makeNode("p2"),
    makeNode("c1"),
    makeNode("c2"),
    makeNode("c3"),
    makeNode("c4"),
  ];
  const adj = new Map<string, Set<string>>([
    ["center", new Set(["p1", "p2"])],
    ["p1", new Set(["center", "c1", "c2"])],
    ["p2", new Set(["center", "c3", "c4"])],
    ["c1", new Set(["p1"])],
    ["c2", new Set(["p1"])],
    ["c3", new Set(["p2"])],
    ["c4", new Set(["p2"])],
  ]);

  const { rings } = layoutRadial("center", nodes, adj);
  assert.equal(rings[1].length, 2);
  assert.equal(rings[2].length, 4);

  const byId = new Map(nodes.map((n) => [n.id, n]));
  const ring2Sorted = [...rings[2]].sort((a, b) => angleOf(byId.get(a) as GNode) - angleOf(byId.get(b) as GNode));
  // c1/c2 (children of p1) and c3/c4 (children of p2) each form a contiguous
  // run once all ring-2 ids are sorted by angle.
  const p1Children = new Set(["c1", "c2"]);
  const p2Children = new Set(["c3", "c4"]);
  const runs: string[][] = [[ring2Sorted[0]]];
  for (let i = 1; i < ring2Sorted.length; i++) {
    const sameGroup =
      (p1Children.has(ring2Sorted[i]) && p1Children.has(ring2Sorted[i - 1])) ||
      (p2Children.has(ring2Sorted[i]) && p2Children.has(ring2Sorted[i - 1]));
    if (sameGroup) runs[runs.length - 1].push(ring2Sorted[i]);
    else runs.push([ring2Sorted[i]]);
  }
  assert.equal(runs.length, 2, `expected 2 contiguous runs, got ${JSON.stringify(runs)}`);

  // Determinism: run twice on fresh deep copies, expect identical fx/fy.
  const nodesA = nodes.map((n) => makeNode(n.id));
  const nodesB = nodes.map((n) => makeNode(n.id));
  layoutRadial("center", nodesA, adj);
  layoutRadial("center", nodesB, adj);
  assert.deepEqual(
    nodesA.map((n) => [n.fx, n.fy]),
    nodesB.map((n) => [n.fx, n.fy]),
  );
});

test("layoutRadial: nodes beyond RADIAL_MAX_RINGS are left unplaced", () => {
  const chainIds = ["a", "b", "c", "d", "e"]; // center -> a -> b -> c -> d -> e (e is depth 5)
  const nodes = [makeNode("center"), ...chainIds.map(makeNode)];
  const path = ["center", ...chainIds];
  const adj = new Map<string, Set<string>>();
  for (let i = 0; i < path.length; i++) {
    const neighbors = new Set<string>();
    if (i > 0) neighbors.add(path[i - 1]);
    if (i < path.length - 1) neighbors.add(path[i + 1]);
    adj.set(path[i], neighbors);
  }

  const { rings } = layoutRadial("center", nodes, adj);
  assert.ok(rings.length <= RADIAL_MAX_RINGS + 1);
  const placed = new Set(rings.flat());
  assert.ok(!placed.has("e"));

  const eNode = nodes.find((n) => n.id === "e") as GNode;
  assert.equal(eNode.fx, null);
  assert.equal(eNode.fy, null);
});
