// shapes.test.ts - nodeReach must equal what traceNodeShape actually draws.
//
// The two are separate code paths over the same K_* constants, and the force simulation trusts
// nodeReach to keep marks apart. A reach that drifts low silently returns the graph to the state
// this test was written against: shaped nodes overlapping while collide reports them clear.

import { test } from "node:test";
import assert from "node:assert/strict";
import {
  type NodeShape,
  declaredCall,
  nodeClass,
  nodeReach,
  shapeOfNode,
  traceNodeShape,
} from "./shapes.js";
import type { GLink, GNode } from "./types.js";

const SHAPES: NodeShape[] = ["circle", "square", "triangle", "diamond", "hexagon", "ring"];

// One node per shape, via the kind each shape is keyed off in SHAPE_BY_KIND.
const KIND_OF: Record<NodeShape, string> = {
  circle: "function",
  square: "file",
  triangle: "target",
  diamond: "doc",
  hexagon: "charm",
  ring: "owner",
};

// Records the furthest point traceNodeShape puts on the path, measured from the node centre.
// An arc contributes its full radius; rect and the polygon paths contribute their vertices.
function tracedReach(shape: NodeShape, r: number): number {
  let max = 0;
  const at = (px: number, py: number) => {
    max = Math.max(max, Math.hypot(px, py));
  };
  const ctx = {
    beginPath() {},
    closePath() {},
    moveTo: at,
    lineTo: at,
    rect(x: number, y: number, w: number, h: number) {
      at(x, y);
      at(x + w, y);
      at(x, y + h);
      at(x + w, y + h);
    },
    arc(x: number, y: number, radius: number) {
      max = Math.max(max, Math.hypot(x, y) + radius);
    },
  } as unknown as CanvasRenderingContext2D;
  traceNodeShape(ctx, shape, 0, 0, r);
  return max;
}

const nodeOf = (shape: NodeShape, r: number) => ({ kind: KIND_OF[shape], r }) as GNode;

for (const shape of SHAPES) {
  test(`nodeReach matches the drawn outline: ${shape}`, () => {
    for (const r of [4, 5.3, 24]) {
      const drawn = tracedReach(shape, r);
      const claimed = nodeReach(nodeOf(shape, r));
      assert.ok(
        Math.abs(drawn - claimed) < 1e-9,
        `${shape} at r=${r}: draws to ${drawn}, nodeReach claims ${claimed}`,
      );
      assert.ok(claimed >= r, `${shape} at r=${r}: reach ${claimed} is inside the circle radius`);
    }
  });
}

test("nodeReach: an unknown kind falls back to the circle", () => {
  assert.equal(nodeReach({ kind: "nonesuch", r: 7 } as GNode), 7);
});

test("nodeClass: a marker is classed by its family, every other kind by its kind", () => {
  const cases: [Pick<GNode, "kind" | "attrs">, string][] = [
    [{ kind: "marker", attrs: { family: "calls", verb: "point" } }, "marker: calls"],
    [{ kind: "marker", attrs: { family: "diagram" } }, "marker: diagram"],
    [{ kind: "marker" }, "marker"],
    [{ kind: "marker", attrs: { family: "" } }, "marker"],
    [{ kind: "dir", attrs: { family: "calls" } }, "dir"],
  ];
  for (const [n, want] of cases) assert.equal(nodeClass(n), want, JSON.stringify(n));
});

test("declaredCall: only a declared calls edge, with its transport", () => {
  const link = (over: Partial<GLink>): GLink => ({
    source: "a",
    target: "b",
    relation: "calls",
    ...over,
  });
  const cases: [GLink, { transport: string } | null][] = [
    [
      link({ confidence: "declared", attrs: { transport: "http,grpc" } }),
      { transport: "http,grpc" },
    ],
    [link({ confidence: "declared" }), { transport: "" }],
    [link({ confidence: "extracted", attrs: { transport: "http" } }), null],
    [link({ relation: "imports", confidence: "declared", attrs: { transport: "http" } }), null],
  ];
  for (const [e, want] of cases) assert.deepEqual(declaredCall(e), want, JSON.stringify(e));
});

test("the marker kind draws as a declaration", () => {
  assert.equal(shapeOfNode({ kind: "marker" } as GNode), "triangle");
});
