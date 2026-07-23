// radial.ts - ego layout: BFS rings around a center node over the UNDIRECTED
// adjacency, deterministic angles. Writes fx/fy (and x/y) onto the passed
// GNode objects, the same convention layout.ts uses for the layered DAG.
// Pure: no module state, no DOM. main.ts owns the stateful switchLayout
// wrapper that decides the center, the visible subset, and what to do with
// nodes this function leaves unplaced (park them).
//
// Algorithm:
//   1. BFS from centerId over `adj`, restricted to the ids present in
//      `nodes`. Neighbors are iterated in sorted-id order so the first-seen
//      parent (and therefore depth) is deterministic. Nodes more than
//      RADIAL_MAX_RINGS hops away, or unreachable, are left unplaced - they
//      do not appear in any ring.
//   2. Ring 0 is the center alone, at the origin.
//   3. Ring 1 (parent = center for every member) distributes evenly over the
//      full circle, sorted by id, starting at -PI/2 (12 o'clock).
//   4. Ring d >= 2 groups members by parent, in the angular order the parent
//      ring was laid out; each parent's group gets a contiguous angular span
//      proportional to its member count, members spaced evenly within the
//      span (sorted by id), spans placed back to back starting at the first
//      parent's angle.
//   5. fx = depth * RADIAL_RING_R * cos(angle), fy = ... * sin(angle); x/y
//      mirror fx/fy so the first draw is immediate, before any tick.

import type { GNode } from "./types.js";

export const RADIAL_RING_R = 150; // world units between rings
export const RADIAL_MAX_RINGS = 4; // deepest ring placed (0 = center)

function sortedIds(ids: Set<string>): string[] {
  return [...ids].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
}

export function layoutRadial(
  centerId: string,
  nodes: GNode[],
  adj: Map<string, Set<string>>,
): { rings: string[][] } {
  const visibleIds = new Set(nodes.map((n) => n.id));
  const byId = new Map(nodes.map((n) => [n.id, n]));

  // ---- Step 1: BFS from center, sorted-neighbor order for determinism ------
  const depthOf = new Map<string, number>();
  const parentOf = new Map<string, string | null>();
  depthOf.set(centerId, 0);
  parentOf.set(centerId, null);
  const queue: string[] = [centerId];
  for (let qi = 0; qi < queue.length; qi++) {
    const id = queue[qi];
    const depth = depthOf.get(id) ?? 0;
    if (depth >= RADIAL_MAX_RINGS) continue; // do not discover beyond the cap
    const neighbors = sortedIds(adj.get(id) ?? new Set());
    for (const nb of neighbors) {
      if (!visibleIds.has(nb) || depthOf.has(nb)) continue;
      depthOf.set(nb, depth + 1);
      parentOf.set(nb, id);
      queue.push(nb);
    }
  }

  // ---- Step 2-4: assign angles ring by ring ---------------------------------
  const angleOf = new Map<string, number>();
  angleOf.set(centerId, 0);
  const rings: string[][] = [[centerId]];

  const maxDepth = Math.max(0, ...[...depthOf.values()]);
  for (let d = 1; d <= maxDepth; d++) {
    const members: string[] = [];
    for (const [id, depth] of depthOf) {
      if (depth === d) members.push(id);
    }
    if (!members.length) break;

    let ordered: string[];
    if (d === 1) {
      // Every member's parent is the center: distribute evenly over the full
      // circle, sorted by id, starting at 12 o'clock.
      ordered = members.sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
      const step = (2 * Math.PI) / ordered.length;
      ordered.forEach((id, i) => angleOf.set(id, -Math.PI / 2 + i * step));
    } else {
      // Group by parent, in the angular order the parent ring was laid out.
      const groups = new Map<string, string[]>();
      for (const id of members) {
        const parent = parentOf.get(id) ?? centerId;
        if (!groups.has(parent)) groups.set(parent, []);
        groups.get(parent)?.push(id);
      }
      for (const group of groups.values()) group.sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));

      const prevRing = rings[d - 1];
      let cursor = angleOf.get(prevRing[0]) ?? -Math.PI / 2;
      const total = members.length;
      ordered = [];
      for (const parent of prevRing) {
        const group = groups.get(parent);
        if (!group || !group.length) continue;
        const span = (group.length / total) * 2 * Math.PI;
        const step = span / group.length;
        group.forEach((id, j) => angleOf.set(id, cursor + j * step));
        ordered.push(...group);
        cursor += span;
      }
    }
    rings.push(ordered);
  }

  // ---- Step 5: write positions -----------------------------------------------
  for (let d = 0; d < rings.length; d++) {
    for (const id of rings[d]) {
      const n = byId.get(id);
      if (!n) continue;
      const angle = angleOf.get(id) ?? 0;
      n.fx = d === 0 ? 0 : d * RADIAL_RING_R * Math.cos(angle);
      n.fy = d === 0 ? 0 : d * RADIAL_RING_R * Math.sin(angle);
      n.x = n.fx;
      n.y = n.fy;
    }
  }

  return { rings };
}
