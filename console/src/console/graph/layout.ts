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
// layoutWaves shares steps 1-2 (via computeLayers) but replaces steps 3-4: no
// barycenter (waves emphasize membership and parallelism, not crossing
// reduction), strict column grid, and within-wave ordering by (project, id) so
// a project's targets cluster. It returns the per-wave membership so main.ts
// can label columns and report parallelism.
//
// Both layouts route depends_on edges whose layer span is more than one hop
// through synthetic "dummy" nodes at each intermediate layer (never pushed
// into the real `nodes` array): in layoutLayered the dummies join the
// barycenter ordering to pull crossings down; in both layouts the dummies'
// final coordinates become the edge's `points` route so draw() can curve
// through the intermediate columns instead of slicing through them.
//
// The force simulation is NOT ticked in layered/waves mode. d3-zoom, drag,
// hover, and selection operate on the same draw() function unchanged. Drag
// updates n.fx/n.fy directly.

import { type GLink, type GNode, endpointId } from "./types.js";

export const LAYERED_COL_W = 180; // horizontal spacing between layers (columns)
export const LAYERED_ROW_H = 48; // vertical spacing between nodes within a layer
export const LAYERED_MAX = 500; // scale guard: refuse layered above this count

// A DummyChain is the bend-point route for one long depends_on edge: dummy ids
// standing in for the edge at each intermediate layer, ordered ascending by
// layer - which is also ascending x, since e.s (the dependent) always sits at
// a strictly higher layer than e.t (the dependency) once cycle-break has run.
interface DummyChain {
  ids: string[];
  layers: number[];
}

type DepEdge = { s: string; t: string; linkRef: GLink };

// computeLayers runs the shared cycle-break + longest-path layering pass.
// Both layoutLayered and layoutWaves consume its output; neither may change
// its behavior (layoutLayered's fx/fy and layoutReversed flags must stay
// byte-identical to the pre-extraction implementation for graphs where every
// edge spans exactly one layer - the case every existing caller relies on).
function computeLayers(
  nodes: GNode[],
  links: GLink[],
): { layerOf: Map<string, number>; depEdges: DepEdge[] } {
  // Work on the visible subset by id.
  const ids = new Set(nodes.map((n) => n.id));

  // Collect depends_on edges (only those within the visible subset).
  // We work on index arrays to avoid mutating the real link objects (except
  // the layoutReversed flag, which IS written back for the draw pass).
  const depEdges: DepEdge[] = [];
  for (const e of links) {
    if (e.relation !== "depends_on") continue;
    const s = endpointId(e.source);
    const t = endpointId(e.target);
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
  const outSnap = new Map<string, { origTarget: string; edgeRef: DepEdge }[]>();
  for (const id of ids) outSnap.set(id, []);
  for (const e of depEdges) outSnap.get(e.s)?.push({ origTarget: e.t, edgeRef: e });
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
    predMap.get(e.s)?.add(e.t);
  }

  const layerOf = new Map<string, number>(); // nodeId -> layer index
  function getLayer(id: string): number {
    const cached = layerOf.get(id);
    if (cached !== undefined) return cached;
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

  return { layerOf, depEdges };
}

// buildDummyChains finds every depends_on edge whose layer span is more than
// one hop and synthesizes one dummy id per intermediate layer. Dummy ids are
// never real nodes - they exist only to (a) optionally join the barycenter
// ordering so long edges pull crossings down, and (b) carry the x/y that
// becomes the edge's route once coordinates are assigned. layer(e.s) is
// always strictly greater than layer(e.t) once computeLayers has run (e.s is
// the dependent, e.t the dependency), so `ids`/`layers` come out already
// ascending by layer - i.e. ascending x, dependency end to dependent end.
function buildDummyChains(
  depEdges: DepEdge[],
  layerOf: Map<string, number>,
): Map<GLink, DummyChain> {
  const chains = new Map<GLink, DummyChain>();
  for (const e of depEdges) {
    const ls = layerOf.get(e.s) ?? 0;
    const lt = layerOf.get(e.t) ?? 0;
    if (ls - lt <= 1) continue;
    const ids: string[] = [];
    const layers: number[] = [];
    for (let layer = lt + 1; layer < ls; layer++) {
      // The leading space guarantees no collision with any real node id and
      // sorts dummies before real ids within a layer for stable ordering.
      ids.push(` dummy:${e.s}->${e.t}:${layer}`);
      layers.push(layer);
    }
    chains.set(e.linkRef, { ids, layers });
  }
  return chains;
}

// chainSegments expands one long edge into its unit-length replacement path
// (dependent -> ... -> dependency, matching depEdges' s/t convention, each
// hop exactly one layer) so the barycenter sweep sees a chain of adjacent
// hops instead of a long-range edge that would otherwise skip past the
// intermediate layers uncounted.
function chainSegments(e: { s: string; t: string }, chain: DummyChain): { s: string; t: string }[] {
  const seq = [e.s, ...[...chain.ids].reverse(), e.t]; // high layer -> low layer
  const segs: { s: string; t: string }[] = [];
  for (let i = 0; i < seq.length - 1; i++) segs.push({ s: seq[i], t: seq[i + 1] });
  return segs;
}

// routeEdges resolves each long edge's dummy chain to world-space points via
// the just-computed coordinate map and writes them back onto the link;
// short edges (span <= 1, no chain) have any stale route cleared.
function routeEdges(
  depEdges: DepEdge[],
  chains: Map<GLink, DummyChain>,
  dummyCoord: Map<string, { x: number; y: number }>,
): void {
  for (const e of depEdges) {
    const chain = chains.get(e.linkRef);
    if (chain) {
      e.linkRef.points = chain.ids.map((id) => dummyCoord.get(id) as { x: number; y: number });
    } else {
      delete e.linkRef.points;
    }
  }
}

export function layoutLayered(
  nodes: GNode[],
  links: GLink[],
  opts?: { colW?: number; rowH?: number },
): void {
  const colW = opts?.colW ?? LAYERED_COL_W;
  const rowH = opts?.rowH ?? LAYERED_ROW_H;
  const ids = new Set(nodes.map((n) => n.id));
  const { layerOf, depEdges } = computeLayers(nodes, links);

  // Group nodes by layer and sort within each layer by id for initial order.
  const layerGroups = new Map<number, string[]>(); // layer -> [nodeId, ...]
  for (const [id, l] of layerOf) {
    if (!layerGroups.has(l)) layerGroups.set(l, []);
    layerGroups.get(l)?.push(id);
  }

  // ---- dummy-node insertion (Phase 1 edge routing) ---------------------------
  // Long edges (span > 1) get one dummy per intermediate layer; dummies join
  // layerGroups exactly like real nodes so the barycenter passes below see
  // them, but they are NEVER pushed into the real `nodes` array.
  const chains = buildDummyChains(depEdges, layerOf);
  for (const chain of chains.values()) {
    for (let i = 0; i < chain.ids.length; i++) {
      const layer = chain.layers[i];
      if (!layerGroups.has(layer)) layerGroups.set(layer, []);
      layerGroups.get(layer)?.push(chain.ids[i]);
    }
  }
  for (const arr of layerGroups.values()) arr.sort();

  // ---- Step 3: barycenter ordering (3 passes: down, up, down) ---------------
  // Within each layer, order nodes by the mean positional index of their
  // neighbors in adjacent layers. Ties broken by node.id (determinism).
  const sortedLayers = [...layerGroups.keys()].sort((a, b) => a - b);

  // pos[id] = current order index within its layer.
  const pos = new Map<string, number>();
  for (const l of sortedLayers) {
    layerGroups.get(l)?.forEach((id, i) => pos.set(id, i));
  }

  // Build directed edge sets for sweep (predecessor in layer l-1, successor in l+1).
  // Built from the SEGMENTED edge list (long edges expanded through their dummy
  // chain), not the original depEdges, so the sweep sees unit-length hops only.
  const idsAll = new Set<string>(ids);
  for (const chain of chains.values()) for (const id of chain.ids) idsAll.add(id);

  const succMap = new Map<string, string[]>(); // nodeId -> [nodeId]  (target of depends_on)
  const prevMap = new Map<string, string[]>(); // nodeId -> [nodeId]  (source of depends_on)
  for (const id of idsAll) {
    succMap.set(id, []);
    prevMap.set(id, []);
  }
  for (const e of depEdges) {
    const chain = chains.get(e.linkRef);
    const segs = chain ? chainSegments(e, chain) : [{ s: e.s, t: e.t }];
    for (const seg of segs) {
      succMap.get(seg.s)?.push(seg.t);
      prevMap.get(seg.t)?.push(seg.s);
    }
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
      const arr = layerGroups.get(l);
      if (!arr) continue;
      const sorted = barycentricSort(arr, neighborFn);
      layerGroups.set(l, sorted);
      sorted.forEach((id, i) => pos.set(id, i));
    }
  }

  // ---- Step 4: assign coordinates -------------------------------------------
  // x = layer index * colW (left = layer 0 = roots/sources)
  // y = order index * rowH, centered vertically within the layer.
  const maxOccupancy = Math.max(...[...layerGroups.values()].map((a) => a.length), 1);
  const totalH = maxOccupancy * rowH;
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const dummyCoord = new Map<string, { x: number; y: number }>();

  for (const l of sortedLayers) {
    const arr = layerGroups.get(l);
    if (!arr) continue;
    const layerH = arr.length * rowH;
    const yOffset = (totalH - layerH) / 2 + rowH / 2;
    for (let i = 0; i < arr.length; i++) {
      const id = arr[i];
      const x = l * colW + colW / 2;
      const y = yOffset + i * rowH;
      const n = byId.get(id);
      if (n) {
        n.fx = x;
        n.fy = y;
        // Also set x/y so the initial draw is immediate (before any tick).
        n.x = x;
        n.y = y;
      } else {
        dummyCoord.set(id, { x, y });
      }
    }
  }

  routeEdges(depEdges, chains, dummyCoord);
}

// layoutWaves lays out the visible subset into strict topological-order
// columns ("waves"): wave N is every node whose longest dependency chain is
// exactly N hops deep - "everything magus can run in parallel once wave N-1
// finished". Unlike layoutLayered there is no barycenter pass (waves
// emphasize membership and parallelism, not crossing reduction); within a
// wave, nodes sort by (project, id) so a project's targets cluster.
export function layoutWaves(
  nodes: GNode[],
  links: GLink[],
  opts?: { colW?: number; rowH?: number },
): { waves: string[][] } {
  const colW = opts?.colW ?? LAYERED_COL_W;
  const rowH = opts?.rowH ?? LAYERED_ROW_H;
  const { layerOf, depEdges } = computeLayers(nodes, links);
  const byId = new Map(nodes.map((n) => [n.id, n]));

  // Group real nodes by wave and sort within each wave by (project, id).
  const waveGroups = new Map<number, string[]>();
  for (const [id, l] of layerOf) {
    if (!waveGroups.has(l)) waveGroups.set(l, []);
    waveGroups.get(l)?.push(id);
  }
  const projectOf = (id: string): string => byId.get(id)?.attrs?.project ?? "";
  for (const arr of waveGroups.values()) {
    arr.sort((a, b) => {
      const pa = projectOf(a);
      const pb = projectOf(b);
      if (pa !== pb) return pa < pb ? -1 : 1;
      return a < b ? -1 : a > b ? 1 : 0;
    });
  }

  // Snapshot the per-wave real-node membership (for main.ts) before dummies
  // are appended below - dummies are routing scaffolding, not wave members.
  // waves[i] = the ids in wave i; the array index equals the wave number
  // because longest-path layering guarantees no empty middle layer (reaching
  // layer k requires a real predecessor chain 0..k-1), so the sorted layer
  // keys are already the contiguous run 0..max.
  const waves: string[][] = [...waveGroups.entries()]
    .sort((a, b) => a[0] - b[0])
    .map(([, waveIds]) => [...waveIds]);

  // ---- dummy-node insertion: same routing as layoutLayered, but dummies are
  // simply appended (no barycenter to join - waves use a strict grid).
  const chains = buildDummyChains(depEdges, layerOf);
  for (const chain of chains.values()) {
    for (let i = 0; i < chain.ids.length; i++) {
      const layer = chain.layers[i];
      if (!waveGroups.has(layer)) waveGroups.set(layer, []);
      waveGroups.get(layer)?.push(chain.ids[i]);
    }
  }

  // ---- assign coordinates: strict column grid --------------------------------
  const sortedWaves = [...waveGroups.keys()].sort((a, b) => a - b);
  const maxOccupancy = Math.max(...[...waveGroups.values()].map((a) => a.length), 1);
  const totalH = maxOccupancy * rowH;
  const dummyCoord = new Map<string, { x: number; y: number }>();

  for (const l of sortedWaves) {
    const arr = waveGroups.get(l);
    if (!arr) continue;
    const layerH = arr.length * rowH;
    const yOffset = (totalH - layerH) / 2 + rowH / 2;
    for (let i = 0; i < arr.length; i++) {
      const id = arr[i];
      const x = l * colW + colW / 2;
      const y = yOffset + i * rowH;
      const n = byId.get(id);
      if (n) {
        n.fx = x;
        n.fy = y;
        n.x = x;
        n.y = y;
      } else {
        dummyCoord.set(id, { x, y });
      }
    }
  }

  routeEdges(depEdges, chains, dummyCoord);

  return { waves };
}
