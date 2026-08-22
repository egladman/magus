// views.ts - the node sets behind the two connectivity questions, "what is most depended on"
// and "what is dead". Pure: no module state, no DOM.
//
// Two facts about the graph decide the whole file.
//
// DIRECTION. e.source is the DEPENDENT and e.target is the DEPENDENCY (the same convention
// main.ts's draw() points its arrows by). "How many things depend on X" is therefore X's
// IN-degree, and an undirected count answers a different question than the label promises.
//
// RELATION. `contains` is structural, not dependency: a project contains every one of its
// targets. Counting it ranks project nodes above everything by construction, and makes every
// target that merely belongs to a project look like something is depending on it. So both
// questions run over DEPENDENCY relations only, and a target's `contains` edge to its project
// no longer disqualifies it from being dead.

import { type GLink, type GNode, endpointId } from "./types.js";

// The relations that mean "needs". `contains` is structural; `documents` and `rationale_for`
// are annotational - a doc pointing at a target is not a dependency on it.
const DEPENDENCY_RELATIONS = new Set(["depends_on", "imports", "calls", "uses", "references"]);

export interface DependencyDegree {
  /** How many nodes depend on this one (incoming dependency edges). */
  dependents: number;
  /** How many nodes this one depends on (outgoing dependency edges). */
  dependencies: number;
}

/**
 * dependencyDegrees counts each node's incoming and outgoing DEPENDENCY edges, ignoring
 * structural and annotational relations. Every id in `nodes` gets an entry, including zeros.
 * Self-edges count on both sides. An edge whose endpoint is absent from `nodes` is skipped.
 */
export function dependencyDegrees(
  nodes: readonly GNode[],
  links: readonly GLink[],
): Map<string, DependencyDegree> {
  const out = new Map<string, DependencyDegree>();
  for (const n of nodes) out.set(n.id, { dependents: 0, dependencies: 0 });
  for (const e of links) {
    if (!DEPENDENCY_RELATIONS.has(e.relation)) continue;
    const dependent = out.get(endpointId(e.source));
    const dependency = out.get(endpointId(e.target));
    if (!dependent || !dependency) continue;
    dependent.dependencies++;
    dependency.dependents++;
  }
  return out;
}

/**
 * mostDependedOn returns up to `limit` node ids ranked by how many nodes depend on them,
 * highest first, dropping anything nothing depends on. Ties break by id so the answer is
 * stable across reloads of the same graph. Fewer than `limit` ids come back when fewer
 * qualify - and none at all for a graph with no dependency edges, which is the honest answer
 * rather than an arbitrary top slice of an unranked list.
 */
export function mostDependedOn(
  nodes: readonly GNode[],
  links: readonly GLink[],
  limit: number,
): string[] {
  const deg = dependencyDegrees(nodes, links);
  return nodes
    .map((n) => ({ id: n.id, dependents: deg.get(n.id)?.dependents ?? 0 }))
    .filter((e) => e.dependents > 0)
    .sort((a, b) => b.dependents - a.dependents || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))
    .slice(0, limit)
    .map((e) => e.id);
}

/**
 * disconnected returns the ids with no dependency edge in either direction - nothing depends on
 * them and they depend on nothing - among the kinds that carry dependencies AT ALL in this
 * graph. These are the dead-or-unconfigured candidates.
 *
 * Two structural traps sit either side of this answer, and the kind filter is what threads
 * between them. Testing raw degree instead finds almost nothing, because in a knowledge graph
 * every target carries a `contains` edge to its project. Dropping the kind filter finds far too
 * much, because whole kinds - diagnostics, directories, methods, rationales - never sit on a
 * dependency edge in the first place, and reporting each of them as dead code says nothing
 * about the workspace.
 *
 * Which kinds qualify is read off THIS graph rather than hardcoded: a kind counts when at least
 * one node of it sits on a dependency edge. That keeps the answer meaningful across both graph
 * flavors and any kind added later, without a list to maintain.
 */
export function disconnected(nodes: readonly GNode[], links: readonly GLink[]): string[] {
  const deg = dependencyDegrees(nodes, links);
  const bearing = new Set<string>();
  for (const n of nodes) {
    const d = deg.get(n.id);
    if (d && d.dependents + d.dependencies > 0) bearing.add(n.kind);
  }
  return nodes
    .filter((n) => {
      if (!bearing.has(n.kind)) return false;
      const d = deg.get(n.id);
      return !!d && d.dependents === 0 && d.dependencies === 0;
    })
    .map((n) => n.id);
}

/**
 * projectOwners maps each node to the project that CONTAINS it, following `contains` edges.
 * Nodes no project contains - workspace-global things like spells, diagnostics and modules -
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
 * workspace has - tens, not thousands - so the product stays small.
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
    for (let i = 0; i < queue.length; i++) {
      for (const next of children.get(queue[i]) ?? []) {
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
