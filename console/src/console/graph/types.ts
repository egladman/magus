// types.ts - the shared domain shapes for the graph explorer's TS modules.
// Extracted from the original graph-explorer.js monolith, which kept nodes/links
// as untyped plain objects. The interfaces below name the fields the code reads;
// the permissive `unknown` index signature on GNode preserves the monolith's
// dynamic behavior (several passes stash scratch fields on nodes) without forcing
// every scratch access to be enumerated here.

import type { SimulationNodeDatum } from "d3-force";

// GNodeInput is a node as a loader or the target-adapter produces it, before
// prepareGraph computes degree/r. DurationMs and duration_ms are optional timing
// fields only some exports carry. Any other scratch the monolith stashes on a node
// falls under the `unknown` index rather than being enumerated, and is narrowed at
// its use site.
export interface GNodeInput extends SimulationNodeDatum {
  id: string;
  kind: string;
  label: string;
  doc?: string;
  source?: string;
  attrs?: Record<string, string>;
  DurationMs?: number;
  duration_ms?: number;
  [k: string]: unknown;
}

// GNode is a prepared graph node: prepareGraph has filled in degree and r (which
// drive node radius and label priority), and by the time any reader runs d3-force
// (or the layered layout) has assigned x/y and fx/fy. Those position fields are
// narrowed from SimulationNodeDatum's optional numbers to definite ones so the hot
// draw/fit/hit-test math reads them without per-access null-narrowing; the surviving
// `x == null` guards still fire at runtime for the brief pre-simulation window.
export interface GNode extends GNodeInput {
  degree: number;
  r: number;
  x: number;
  y: number;
  fx: number | null;
  fy: number | null;
}

// GEndpoint is a GLink endpoint: it starts as an id string from the loader and
// d3-force's forceLink swaps in the resolved GNode object in place. endpointId
// reads the id back out of either form (the monolith's `e.source.id || e.source`).
export type GEndpoint = string | GNode;

export function endpointId(e: GEndpoint): string {
  return typeof e === "string" ? e : e.id;
}

// GLink is a graph edge. source/target are GEndpoints (see above).
export interface GLink {
  source: GEndpoint;
  target: GEndpoint;
  relation: string;
  confidence?: string;
  score?: number;
  dashed?: boolean;
  cycle?: boolean;
  layoutReversed?: boolean;
}

// GraphFlavor is the two graph shapes the CLI emits and the explorer renders.
export type GraphFlavor = "knowledge" | "targets";

// ---- CLI target-graph wire types (verified against types/describe.go) --------
// Every field the target-adapter reads is enumerated; all are optional because the
// adapter guards each with `|| []` / truthiness, matching the monolith's defensive
// reads.
export interface TargetSpellUse {
  spell: string;
  ops?: string[];
}
export interface CrossTargetRef {
  project: string;
  target: string;
}
export interface TargetGraphNode {
  name: string;
  doc?: string;
  dependencies?: string[];
  charms?: string[];
  spells?: TargetSpellUse[];
  cross_dependencies?: CrossTargetRef[];
}
export interface TargetGraphProject {
  path: string;
  engine?: string;
  nodes?: TargetGraphNode[];
  cycle?: string[];
  depends_on?: string[];
}
export interface TargetGraphOutput {
  definition?: string;
  projects?: TargetGraphProject[];
}

// GraphPayload is a parsed graph JSON as it arrives from any loader, before flavor
// detection. It is the superset of the knowledge shape (nodes/links/edges/source_base)
// and the target shape (definition/projects); which fields are present depends on the
// flavor, so all are optional. node_count rides skeleton (?level=projects) responses.
export interface GraphPayload {
  nodes?: GNodeInput[];
  links?: GLink[];
  edges?: GLink[];
  source_base?: string;
  definition?: string;
  projects?: TargetGraphProject[];
  node_count?: number;
}
