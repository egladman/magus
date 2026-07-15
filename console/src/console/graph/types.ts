// types.ts - the shared domain shapes for the graph explorer's TS modules.
// Extracted verbatim from the original graph-explorer.js monolith, which kept
// nodes/links as untyped plain objects. The interfaces below name the fields the
// code reads; the permissive `any` index signatures preserve the monolith's
// dynamic behavior (d3-force pins fx/fy and swaps link source/target from id
// strings to the resolved node objects in place, and several passes stash scratch
// fields on nodes) without forcing every scratch access to be enumerated here.

// GNode is a graph node as it flows through the explorer. prepareGraph builds the
// plain objects; d3-force then mutates x/y/vx/vy, and layered mode pins fx/fy.
export interface GNode {
  id: string;
  kind: string;
  label: string;
  doc?: string;
  attrs?: Record<string, string>;
  // The d3-force simulation + layout fields (x/y/vx/vy/fx/fy) and rendering/traversal
  // scratch the monolith stashes on nodes fall under this permissive index rather than
  // being enumerated: d3 mutates them dynamically and the draw math treats them as plain
  // numbers, so `any` keeps that verbatim without undefined-narrowing noise.
  [k: string]: any;
}

// GLink is a graph edge. source/target start as id strings from the loader and
// d3-force swaps in the resolved GNode objects in place, so the code reads them as
// `e.source.id || e.source`; typing them `any` keeps that idiom verbatim.
export interface GLink {
  source: any;
  target: any;
  relation: string;
  confidence?: string;
  score?: number;
  dashed?: boolean;
  cycle?: boolean;
  layoutReversed?: boolean;
}

// GraphFlavor is the two graph shapes the CLI emits and the explorer renders.
export type GraphFlavor = "knowledge" | "targets";
