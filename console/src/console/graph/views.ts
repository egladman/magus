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
