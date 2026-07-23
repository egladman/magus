// layout.test.ts - pure-math coverage for the deterministic Sugiyama layouts.
// No DOM: layoutLayered/layoutWaves read only GNode/GLink fields and write
// fx/fy/points back onto them, so a plain node:test run exercises the real
// algorithm end to end. Fixtures use the id/label/degree/r/x/y/fx/fy shape
// layout.ts actually reads and writes - see types.ts's GNode.

import { test } from "node:test";
import assert from "node:assert/strict";
import { LAYERED_COL_W, layoutLayered, layoutWaves } from "./layout.js";
import type { GLink, GNode } from "./types.js";

function mkNode(id: string, project?: string): GNode {
  return {
    id,
    kind: "target",
    label: id,
    attrs: project === undefined ? undefined : { project },
    degree: 0,
    r: 6,
    x: 0,
    y: 0,
    fx: null,
    fy: null,
  };
}

// mkLink builds a depends_on edge: source = the DEPENDENT, target = the
// DEPENDENCY (matches the Go emitter convention layout.ts relies on), so
// mkLink("a", "b") reads as "a depends on b".
function mkLink(source: string, target: string): GLink {
  return { source, target, relation: "depends_on" };
}

// ---- 3.3: layoutLayered edge routing ---------------------------------------

test("layoutLayered: a unit-span chain gets no points on any edge", () => {
  const nodes = ["a", "b", "c", "d"].map((id) => mkNode(id));
  const links = [mkLink("a", "b"), mkLink("b", "c"), mkLink("c", "d")];
  layoutLayered(nodes, links);
  for (const l of links) assert.equal(l.points, undefined);
});

test("layoutLayered: an edge spanning 3 layers gets 2 ascending-x bend points at the intermediate column centers", () => {
  const nodes = ["a", "b", "c", "d"].map((id) => mkNode(id));
  const links = [mkLink("a", "b"), mkLink("b", "c"), mkLink("c", "d"), mkLink("a", "d")];
  layoutLayered(nodes, links);

  for (const l of links.slice(0, 3)) assert.equal(l.points, undefined); // unit-span edges: no points

  const long = links[3]; // a -> d: layer(a)=3, layer(d)=0, span 3
  assert.ok(long.points);
  assert.equal(long.points.length, 2);
  const [p0, p1] = long.points;
  assert.ok(p0.x < p1.x, "points must be ordered ascending x (dependency end -> dependent end)");
  assert.equal(p0.x, 1 * LAYERED_COL_W + LAYERED_COL_W / 2); // intermediate layer 1 (c's column)
  assert.equal(p1.x, 2 * LAYERED_COL_W + LAYERED_COL_W / 2); // intermediate layer 2 (b's column)
});

test("layoutLayered: repeated runs on fresh copies produce identical fx/fy and points", () => {
  const build = (): { nodes: GNode[]; links: GLink[] } => ({
    nodes: ["a", "b", "c", "d"].map((id) => mkNode(id)),
    links: [mkLink("a", "b"), mkLink("b", "c"), mkLink("c", "d"), mkLink("a", "d")],
  });

  const run1 = build();
  layoutLayered(run1.nodes, run1.links);
  const run2 = build();
  layoutLayered(run2.nodes, run2.links);

  assert.deepEqual(
    run1.nodes.map((n) => ({ id: n.id, fx: n.fx, fy: n.fy })),
    run2.nodes.map((n) => ({ id: n.id, fx: n.fx, fy: n.fy })),
  );
  assert.deepEqual(
    run1.links.map((l) => l.points),
    run2.links.map((l) => l.points),
  );
});

test("layoutLayered: a two-node cycle reverses exactly one edge and both nodes get placed", () => {
  const nodes = ["a", "b"].map((id) => mkNode(id));
  const links = [mkLink("a", "b"), mkLink("b", "a")];
  layoutLayered(nodes, links);

  const reversedCount = links.filter((l) => l.layoutReversed).length;
  assert.equal(reversedCount, 1);
  for (const n of nodes) {
    assert.equal(typeof n.fx, "number");
    assert.equal(typeof n.fy, "number");
  }
});

// ---- 5.7: layoutWaves -------------------------------------------------------

test("layoutWaves: a 4-node chain yields 4 waves of 1", () => {
  const nodes = ["a", "b", "c", "d"].map((id) => mkNode(id));
  const links = [mkLink("a", "b"), mkLink("b", "c"), mkLink("c", "d")];
  const { waves } = layoutWaves(nodes, links);

  assert.equal(waves.length, 4);
  for (const ids of waves) assert.equal(ids.length, 1);
});

test("layoutWaves: a diamond (a depends on b,c; b,c depend on d) yields wave0=[d], wave1=[b,c] sharing an x, wave2=[a]", () => {
  const nodes = ["a", "b", "c", "d"].map((id) => mkNode(id));
  const links = [mkLink("a", "b"), mkLink("a", "c"), mkLink("b", "d"), mkLink("c", "d")];
  const { waves } = layoutWaves(nodes, links);

  assert.deepEqual(waves, [["d"], ["b", "c"], ["a"]]);

  const byId = new Map(nodes.map((n) => [n.id, n]));
  assert.equal(byId.get("b")?.fx, byId.get("c")?.fx);
});

test("layoutWaves: within a wave, nodes sort by (project, id)", () => {
  const nodes = [mkNode("b1", "z-project"), mkNode("a1", "a-project"), mkNode("c1", "a-project"), mkNode("root")];
  const links = [mkLink("b1", "root"), mkLink("a1", "root"), mkLink("c1", "root")];
  const { waves } = layoutWaves(nodes, links);

  assert.deepEqual(waves[1], ["a1", "c1", "b1"]);
});

test("layoutWaves: repeated runs on fresh copies produce identical waves and coordinates", () => {
  const build = (): { nodes: GNode[]; links: GLink[] } => ({
    nodes: ["a", "b", "c", "d"].map((id) => mkNode(id)),
    links: [mkLink("a", "b"), mkLink("a", "c"), mkLink("b", "d"), mkLink("c", "d")],
  });

  const run1 = build();
  const w1 = layoutWaves(run1.nodes, run1.links);
  const run2 = build();
  const w2 = layoutWaves(run2.nodes, run2.links);

  assert.deepEqual(w1.waves, w2.waves);
  assert.deepEqual(
    run1.nodes.map((n) => ({ id: n.id, fx: n.fx, fy: n.fy })),
    run2.nodes.map((n) => ({ id: n.id, fx: n.fx, fy: n.fy })),
  );
});
