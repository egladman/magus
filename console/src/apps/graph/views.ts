// views.ts - pure helpers over the loaded graph: which project owns a node, and whether a cold
// load opens projected. No module state, no DOM.
//
// `contains` is structural, not dependency: a project contains every one of its targets.

import { type GLink, type GNode, endpointId } from "./types.js";

/**
 * projectOwners maps each node to the project that CONTAINS it, following `contains` edges.
 * Nodes no project contains (workspace-global things like spells, diagnostics and modules)
 * are absent from the result rather than attributed to a root project that merely sits above
 * them in the tree.
 *
 * The nesting order is what makes the answer useful, so projects are flooded DEEPEST FIRST and
 * each claims only nodes still unclaimed. Distance cannot decide it: a root project often
 * reaches a file in one hop while the nested project that actually owns it reaches the same
 * file through a directory, and the more specific project is the right answer either way.
 *
 * This has to come from the EDGES: no node in a knowledge graph carries an `attrs.project`, so
 * anything deriving project membership from ids alone sees a project plus its targets and misses
 * the dirs, docs and functions that make up the rest of it.
 *
 * Cost is O(projects x containsEdges) in the worst case. Projects are the coarsest entity a
 * workspace has (tens, not thousands), so the product stays small.
 */
export function projectOwners(
  nodes: readonly GNode[],
  links: readonly GLink[],
): Map<string, string> {
  const children = new Map<string, string[]>();
  for (const e of links) {
    if (e.relation !== "contains") continue;
    const s = endpointId(e.source);
    const list = children.get(s);
    if (list) list.push(endpointId(e.target));
    else children.set(s, [endpointId(e.target)]);
  }
  const projects = nodes.filter((n) => n.kind === "project").map((n) => n.id);
  const projectSet = new Set(projects);

  // reach(root) walks contains from root; `stop` ends a branch, which keeps a project's flood
  // out of a nested project's subtree and makes the walk terminate on a containment cycle.
  const reach = (root: string, stop: (id: string) => boolean, visit: (id: string) => void) => {
    const seen = new Set([root]);
    const queue = [root];
    for (const at of queue) {
      for (const next of children.get(at) ?? []) {
        if (seen.has(next)) continue;
        seen.add(next);
        visit(next);
        if (!stop(next)) queue.push(next);
      }
    }
  };

  // How many projects contain this one; deeper means more specific.
  const depth = new Map<string, number>(projects.map((id) => [id, 0]));
  for (const p of projects) {
    reach(
      p,
      () => false,
      (id) => {
        if (projectSet.has(id)) depth.set(id, (depth.get(id) ?? 0) + 1);
      },
    );
  }

  const owner = new Map<string, string>();
  for (const p of [...projects].sort((a, b) => (depth.get(b) ?? 0) - (depth.get(a) ?? 0))) {
    if (!owner.has(p)) owner.set(p, p); // a project owns itself unless a nested one already did
    reach(
      p,
      (id) => projectSet.has(id), // a nested project owns its own subtree
      (id) => {
        if (!owner.has(id)) owner.set(id, p);
      },
    );
  }
  return owner;
}

// LEGIBLE_NODE_COUNT is the graph size above which a cold load opens on the projects-only
// projection instead of the whole graph.
//
// The number is a legibility judgement and not a measurement: it is roughly where a force layout
// stops separating clusters and starts stacking them. Below it the whole graph is still worth
// showing, and collapsing a 40-node workspace to four project dots would be the worse first
// impression.
//
// It replaced a threshold of 2500 that was chosen as a PERFORMANCE guard - the size above which a
// cold layout would jank the reveal. That made it useless for legibility twice over: it answers a
// question about frame budget rather than about reading, and magus's own graph, the one most
// readers meet first, is 2374 nodes. The flagship case cleared the guard by 5% and opened as fog.
export const LEGIBLE_NODE_COUNT = 250;

// opensProjected decides whether a cold load shows the projection. A fragment directive
// (view/q/node, and the data/src pair that names where a graph came from) always wins: it asks for
// a specific thing to look at, and overriding that with a summary answers a question nobody asked.
export function opensProjected(nodeCount: number, hasFragmentDirective: boolean): boolean {
  return !hasFragmentDirective && nodeCount > LEGIBLE_NODE_COUNT;
}
