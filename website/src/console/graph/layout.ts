// layout.ts - the layered DAG layout (Sugiyama-style, no deps, fully deterministic).
// Extracted from the graph-explorer monolith: this is a pure algorithm - it reads the
// visible node/link subset and writes fx/fy (and layoutReversed on back-edges) onto the
// passed objects, touching no module state. main.ts owns the stateful applyLayeredMode /
// switchLayout wrappers that decide WHICH subset to lay out.
//
// layoutLayered assigns node.fx / node.fy for the visible subset.
// Only `depends_on` edges are used for layering; `uses` and `contains` edges
// render but do not influence placement. All tie-breaking is by node.id
// (lexicographic) so the same input always produces identical coordinates.
//
// Algorithm:
//   1. Cycle-break: iterative DFS on the depends_on subgraph; back-edges are
//      reversed FOR LAYOUT ONLY (e.layoutReversed = true; rendered dashed).
//   2. Longest-path layering: layer(n) = 1 + max(layer(deps)). Roots at 0.
//   3. Barycenter ordering: 3 passes (down, up, down) within each layer.
//      Ties broken by node.id (determinism).
//   4. Coordinates: fixed column width; row height scales to the max layer
//      occupancy. n.fx = col * COL_W; n.fy = order * ROW_H.
//
// The force simulation is NOT ticked in layered mode. d3-zoom, drag, hover,
// and selection operate on the same draw() function unchanged. Drag updates
// n.fx/n.fy directly.

import type { GLink, GNode } from "./types.js";

export const LAYERED_COL_W = 180; // horizontal spacing between layers (columns)
export const LAYERED_ROW_H = 48; // vertical spacing between nodes within a layer
export const LAYERED_MAX = 500; // scale guard: refuse layered above this count

export function layoutLayered(nodes: GNode[], links: GLink[]): void {
  // Work on the visible subset by id.
  const ids = new Set(nodes.map((n) => n.id));

  // Collect depends_on edges (only those within the visible subset).
  // We work on index arrays to avoid mutating the real link objects (except
  // the layoutReversed flag, which IS written back for the draw pass).
  const depEdges: { s: string; t: string; linkRef: GLink }[] = [];
  for (const e of links) {
    if (e.relation !== "depends_on") continue;
    const s = e.source.id || e.source;
    const t = e.target.id || e.target;
    if (!ids.has(s) || !ids.has(t)) continue;
    if (s === t) continue; // self-loop: skip to prevent infinite recursion in getLayer
    depEdges.push({ s, t, linkRef: e });
  }

  // ---- Step 1: cycle-break via iterative DFS --------------------------------
  // Find back-edges (edges that lead to an ancestor in DFS) and mark them
  // reversed for layout. Two-phase: (a) identify back-edges via DFS on the
  // original edges, (b) reverse those edges in depEdges and write the flag.
  // Using a snapshot of outgoing edges per node means the DFS is stable even
  // as we later reverse edges.

  // Sort entry points for determinism: process nodes in id order.
  const sortedIds = nodes.map((n) => n.id).sort();

  // Build a stable DFS snapshot: for each node, a sorted list of [targetId, edgeRef]
  // pairs. We snapshot the original target id separately from the edge object so
  // that later reversals of e.s/e.t don't corrupt the DFS traversal.
  // Sorting by original targetId gives deterministic traversal order.
  const outSnap = new Map<string, { origTarget: string; edgeRef: (typeof depEdges)[number] }[]>();
  for (const id of ids) outSnap.set(id, []);
  for (const e of depEdges) outSnap.get(e.s)!.push({ origTarget: e.t, edgeRef: e });
  for (const arr of outSnap.values())
    arr.sort((a, b) => (a.origTarget < b.origTarget ? -1 : a.origTarget > b.origTarget ? 1 : 0));

  const visited = new Set<string>();
  const inStack = new Set<string>();

  for (const startId of sortedIds) {
    if (visited.has(startId)) continue;
    // Iterative DFS: stack entries are [nodeId, childIndex].
    const dfsStack: [string, number][] = [[startId, 0]];
    while (dfsStack.length) {
      const top = dfsStack[dfsStack.length - 1];
      const [nid, idx] = top;
      if (idx === 0) {
        visited.add(nid);
        inStack.add(nid);
      }
      const children = outSnap.get(nid) || [];
      if (idx < children.length) {
        top[1]++;
        const { origTarget, edgeRef } = children[idx];
        if (inStack.has(origTarget)) {
          // Back-edge: reverse it for layout only. The snapshot key (origTarget)
          // does not change; we only mutate the edge object so predMap is correct.
          edgeRef.linkRef.layoutReversed = true;
          [edgeRef.s, edgeRef.t] = [edgeRef.t, edgeRef.s];
        } else if (!visited.has(origTarget)) {
          dfsStack.push([origTarget, 0]);
        }
      } else {
        // All children visited: pop.
        inStack.delete(nid);
        dfsStack.pop();
      }
    }
  }

  // ---- Step 2: longest-path layering ----------------------------------------
  // layer(n) = 0 if no depends_on predecessors; else 1 + max(layer(pred)).
  // We build a predecessor map from depEdges (which are now cycle-free for
  // layout purposes - back-edges have been reversed).
  const predMap = new Map<string, Set<string>>(); // nodeId -> Set of predecessor ids
  for (const id of ids) predMap.set(id, new Set());
  for (const e of depEdges) {
    // e.s = source = dependent (who has the dependency)
    // e.t = target = dependency (what is depended on)
    // A dependent's predecessor is its dependency: layer(dependent) = 1 + layer(dependency).
    // This puts dependencies at lower x (left) and dependents at higher x (right),
    // matching the Go emitter (dependency --> dependent, LR direction).
    predMap.get(e.s)!.add(e.t);
  }

  const layerOf = new Map<string, number>(); // nodeId -> layer index
  function getLayer(id: string): number {
    if (layerOf.has(id)) return layerOf.get(id)!;
    const preds = predMap.get(id) || new Set<string>();
    // Guard against any residual cycle (reversed edges should have eliminated
    // them, but be safe): if no preds, layer = 0.
    let maxPred = -1;
    for (const p of preds) {
      // Simple recursion is safe because we broke all cycles above.
      maxPred = Math.max(maxPred, getLayer(p));
    }
    const l = maxPred + 1;
    layerOf.set(id, l);
    return l;
  }
  for (const id of sortedIds) getLayer(id);

  // Group nodes by layer and sort within each layer by id for initial order.
  const layerGroups = new Map<number, string[]>(); // layer -> [nodeId, ...]
  for (const [id, l] of layerOf) {
    if (!layerGroups.has(l)) layerGroups.set(l, []);
    layerGroups.get(l)!.push(id);
  }
  for (const arr of layerGroups.values()) arr.sort();

  // ---- Step 3: barycenter ordering (3 passes: down, up, down) ---------------
  // Within each layer, order nodes by the mean positional index of their
  // neighbors in adjacent layers. Ties broken by node.id (determinism).
  const sortedLayers = [...layerGroups.keys()].sort((a, b) => a - b);

  // pos[id] = current order index within its layer.
  const pos = new Map<string, number>();
  for (const l of sortedLayers) {
    layerGroups.get(l)!.forEach((id, i) => pos.set(id, i));
  }

  // Build directed edge sets for sweep (predecessor in layer l-1, successor in l+1).
  const succMap = new Map<string, string[]>(); // nodeId -> [nodeId]  (target of depends_on)
  const prevMap = new Map<string, string[]>(); // nodeId -> [nodeId]  (source of depends_on)
  for (const id of ids) {
    succMap.set(id, []);
    prevMap.set(id, []);
  }
  for (const e of depEdges) {
    succMap.get(e.s)!.push(e.t);
    prevMap.get(e.t)!.push(e.s);
  }

  function barycentricSort(arr: string[], neighborFn: (id: string) => string[]): string[] {
    const scored = arr.map((id) => {
      const nbs = neighborFn(id);
      if (!nbs.length) return { id, score: Infinity }; // no neighbors: keep at end
      const mean = nbs.reduce((s, nb) => s + (pos.get(nb) ?? 0), 0) / nbs.length;
      return { id, score: mean };
    });
    // Stable sort by score then id (determinism for ties; codepoint < for locale-independence).
    scored.sort((a, b) => a.score - b.score || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
    return scored.map((x) => x.id);
  }

  // Sweep order: down (left-to-right layers), up (right-to-left), down again.
  const sweeps = [
    { order: sortedLayers, neighborFn: (id: string) => prevMap.get(id) || [] },
    { order: [...sortedLayers].reverse(), neighborFn: (id: string) => succMap.get(id) || [] },
    { order: sortedLayers, neighborFn: (id: string) => prevMap.get(id) || [] },
  ];

  for (const { order, neighborFn } of sweeps) {
    for (const l of order) {
      const arr = layerGroups.get(l)!;
      const sorted = barycentricSort(arr, neighborFn);
      layerGroups.set(l, sorted);
      sorted.forEach((id, i) => pos.set(id, i));
    }
  }

  // ---- Step 4: assign coordinates -------------------------------------------
  // x = layer index * COL_W (left = layer 0 = roots/sources)
  // y = order index * ROW_H, centered vertically within the layer.
  const maxOccupancy = Math.max(...[...layerGroups.values()].map((a) => a.length), 1);
  const totalH = maxOccupancy * LAYERED_ROW_H;
  const byId = new Map(nodes.map((n) => [n.id, n]));

  for (const l of sortedLayers) {
    const arr = layerGroups.get(l)!;
    const layerH = arr.length * LAYERED_ROW_H;
    const yOffset = (totalH - layerH) / 2 + LAYERED_ROW_H / 2;
    for (let i = 0; i < arr.length; i++) {
      const n = byId.get(arr[i]);
      if (!n) continue;
      n.fx = l * LAYERED_COL_W + LAYERED_COL_W / 2;
      n.fy = yOffset + i * LAYERED_ROW_H;
      // Also set x/y so the initial draw is immediate (before any tick).
      n.x = n.fx;
      n.y = n.fy;
    }
  }
}
