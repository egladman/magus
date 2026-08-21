// Per-family node shapes: the graph's second channel for identity, after color.
//
// Twenty categorical colors cannot satisfy WCAG SC 1.4.1 however they are derived - the palette in
// tokens.css collapses to an OKLab dE of 3.4 under simulated deuteranopia, against a threshold
// around 8. Shape has the same five-or-six category ceiling as hue, which is why it pairs with the
// palette rather than competing: one shape per FAMILY is six shapes.

import type { GNode } from "./types.js";

export type NodeShape = "circle" | "square" | "triangle" | "diamond" | "hexagon" | "ring";

// Families match the hue families in tokens.css; the two must agree or the legend teaches one
// grouping and the canvas draws another. Code keeps the circle - it is the largest family (1383
// nodes in the demo graph) and the circle is the most legible mark when small.
const SHAPE_BY_KIND: Readonly<Record<string, NodeShape>> = {
  project: "square",
  module: "square",
  dir: "square",
  file: "square",
  target: "triangle",
  spell: "triangle",
  op: "triangle",
  tool: "triangle",
  function: "circle",
  method: "circle",
  import: "circle",
  package: "circle",
  symbol: "circle",
  doc: "diamond",
  rationale: "diamond",
  note: "diamond",
  diagnostic: "hexagon",
  charm: "hexagon",
  owner: "ring",
  author: "ring",
};

export function shapeFor(kind: string): NodeShape {
  return SHAPE_BY_KIND[kind] ?? "circle";
}

// Scale factors that give every shape the same AREA as a circle of radius r. Radius encodes degree,
// so sizing these to a shared bounding box instead would make a square node read as a smaller one.
const AREA = Math.PI;
const K_SQUARE = Math.sqrt(AREA / 4);
const K_TRIANGLE = Math.sqrt(AREA / ((3 * Math.sqrt(3)) / 4));
const K_DIAMOND = Math.sqrt(AREA / 2);
const K_HEXAGON = Math.sqrt(AREA / ((3 * Math.sqrt(3)) / 2));

// traceNodeShape lays the path and nothing else, so a fill and a selection stroke share one
// definition of the outline.
export function traceNodeShape(
  ctx: CanvasRenderingContext2D,
  shape: NodeShape,
  x: number,
  y: number,
  r: number,
): void {
  ctx.beginPath();
  switch (shape) {
    case "square": {
      const s = r * K_SQUARE;
      ctx.rect(x - s, y - s, s * 2, s * 2);
      return;
    }
    case "triangle": {
      const s = r * K_TRIANGLE;
      // Nudged down because an equilateral triangle's centroid sits below its circumcentre;
      // centered on the circumcentre it looks like it is floating above its own edges.
      const cy = y + s * 0.125;
      ctx.moveTo(x, cy - s);
      ctx.lineTo(x + s * 0.866, cy + s * 0.5);
      ctx.lineTo(x - s * 0.866, cy + s * 0.5);
      ctx.closePath();
      return;
    }
    case "diamond": {
      const s = r * K_DIAMOND;
      ctx.moveTo(x, y - s);
      ctx.lineTo(x + s, y);
      ctx.lineTo(x, y + s);
      ctx.lineTo(x - s, y);
      ctx.closePath();
      return;
    }
    case "hexagon": {
      const s = r * K_HEXAGON;
      for (let i = 0; i < 6; i++) {
        const a = (Math.PI / 3) * i; // flat-top: a pointy-top hexagon is a circle at these sizes
        const px = x + s * Math.cos(a);
        const py = y + s * Math.sin(a);
        if (i === 0) ctx.moveTo(px, py);
        else ctx.lineTo(px, py);
      }
      ctx.closePath();
      return;
    }
    case "ring":
    case "circle":
    default:
      ctx.arc(x, y, r, 0, 2 * Math.PI);
      return;
  }
}

export function isRing(shape: NodeShape): boolean {
  return shape === "ring";
}

// Punches a filled mark hollow. Compositing rather than stroking, so the ring's weight tracks the
// node radius instead of turning small nodes into dots and large ones into thin hoops.
export function punchRing(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  r: number,
  background: string,
): void {
  ctx.beginPath();
  ctx.arc(x, y, r * 0.52, 0, 2 * Math.PI);
  ctx.fillStyle = background;
  ctx.fill();
}

export function shapeOfNode(n: GNode): NodeShape {
  return shapeFor(n.kind);
}
