// target-adapter.ts - converts the CLI's target-graph shape into the { nodes, links }
// shape the explorer's knowledge-graph path already consumes. Pure functions extracted
// from the monolith; no module state. main.ts calls detectFlavor on the raw payload and,
// for the targets flavor, runs targetGraphToNodeLink before handing off to prepareGraph.
//
// The CLI emits two graph shapes:
//   knowledge: KnowledgeGraphOutput  { definition, nodes, links, ... }
//   targets:   TargetGraphOutput     { definition, projects[] }

import type {
  GLink,
  GNodeInput,
  GraphFlavor,
  TargetGraphOutput,
  TargetGraphProject,
} from "./types.js";

// detectFlavor tells the two shapes apart so the existing knowledge-graph path stays
// byte-identical in behavior; the target path is converted client-side via
// targetGraphToNodeLink before entering prepareGraph.
export function detectFlavor(raw: { projects?: unknown; definition?: unknown }): GraphFlavor {
  return Array.isArray(raw.projects) && typeof raw.definition === "string"
    ? "targets"
    : "knowledge";
}

// targetGraphToNodeLink converts a TargetGraphOutput to the { nodes, links }
// shape prepareGraph accepts (raw.nodes + raw.links). Wire types (verified
// against types/describe.go):
//   TargetGraphProject { path, engine?, nodes?, cycle?, depends_on? }
//   TargetGraphNode    { name, doc?, dependencies?, charms?, spells?, cross_dependencies? }
//   TargetSpellUse     { spell, ops }      <- read s.spell, not s itself
//   CrossTargetRef     { project, target } <- read c.project / c.target
//
// All edges carry confidence "high" + score 1 so existing code paths that
// inspect those fields keep working.
export function targetGraphToNodeLink(tg: TargetGraphOutput): {
  nodes: GNodeInput[];
  links: GLink[];
  cycleWarnings: string[];
} {
  const nodes: GNodeInput[] = [];
  const links: GLink[] = [];
  const nodeIds = new Set<string>(); // for skipping dangling edges

  // Pass 1: build all nodes so edge validation can reference them.
  const projectPaths: string[] = [];
  for (const p of tg.projects || []) {
    // Project node.
    nodes.push({ id: p.path, kind: "project", label: p.path, attrs: { project: p.path } });
    nodeIds.add(p.path);
    projectPaths.push(p.path);

    // Target nodes.
    for (const n of p.nodes || []) {
      const id = p.path + "#" + n.name;
      nodes.push({
        id,
        kind: "target",
        label: n.name,
        doc: n.doc || undefined,
        attrs: { project: p.path },
      });
      nodeIds.add(id);
    }

    // Spell nodes (distinct by name across all targets in all projects).
    for (const n of p.nodes || []) {
      for (const s of n.spells || []) {
        const spellId = "spell:" + s.spell;
        if (!nodeIds.has(spellId)) {
          nodes.push({ id: spellId, kind: "spell", label: s.spell, attrs: {} });
          nodeIds.add(spellId);
        }
      }
    }
  }

  // Pass 2: collect same-project depends_on targets so anchor detection is correct.
  // A target is an anchor when no same-project depends_on edge points at it.
  const referencedByIntraProject = new Set<string>(); // ids that appear as dep within same project
  for (const p of tg.projects || []) {
    for (const n of p.nodes || []) {
      for (const d of n.dependencies || []) {
        referencedByIntraProject.add(p.path + "#" + d);
      }
    }
  }

  // Pass 3: build edges.
  const cycleProjects: TargetGraphProject[] = []; // projects with a non-empty cycle field
  for (const p of tg.projects || []) {
    // Containment: project -> each of its targets.
    for (const n of p.nodes || []) {
      const targetId = p.path + "#" + n.name;
      links.push({
        source: p.path,
        target: targetId,
        relation: "contains",
        confidence: "high",
        score: 1,
      });
    }

    // Build a set of cycle-edge pairs for this project (consecutive pairs in
    // the cycle array form the cycle edges).
    const cycleEdgePairs = new Set<string>();
    if (p.cycle && p.cycle.length >= 2) {
      for (let ci = 0; ci < p.cycle.length - 1; ci++) {
        cycleEdgePairs.add(p.path + "#" + p.cycle[ci] + "->" + p.path + "#" + p.cycle[ci + 1]);
      }
    }

    // Same-project depends_on edges.
    for (const n of p.nodes || []) {
      const srcId = p.path + "#" + n.name;
      for (const d of n.dependencies || []) {
        const dstId = p.path + "#" + d;
        if (!nodeIds.has(dstId)) continue; // skip dangling (prepareGraph also filters)
        const isCycle = cycleEdgePairs.has(srcId + "->" + dstId);
        links.push({
          source: srcId,
          target: dstId,
          relation: "depends_on",
          confidence: "high",
          score: 1,
          ...(isCycle ? { cycle: true } : {}),
        });
      }

      // Cross-project depends_on edges.
      for (const c of n.cross_dependencies || []) {
        const dstId = c.project + "#" + c.target;
        // The cross-project target node may or may not be in this graph; only
        // emit the edge if the destination node exists (avoid phantom nodes).
        if (!nodeIds.has(dstId)) continue;
        links.push({
          source: srcId,
          target: dstId,
          relation: "depends_on",
          confidence: "high",
          score: 1,
        });
      }

      // Spell edges: target -> spell node.
      for (const s of n.spells || []) {
        const spellId = "spell:" + s.spell;
        links.push({
          source: srcId,
          target: spellId,
          relation: "uses",
          confidence: "high",
          score: 1,
        });
      }
    }

    // Project-level depends_on edges (project -> project).
    for (const q of p.depends_on || []) {
      if (!nodeIds.has(q)) continue;
      links.push({
        source: p.path,
        target: q,
        relation: "depends_on",
        confidence: "high",
        score: 1,
      });
    }

    // Track projects with cycles for the status warning.
    if (p.cycle && p.cycle.length) cycleProjects.push(p);
  }

  // Mark anchors: targets with no incoming same-project depends_on edge.
  for (const n of nodes) {
    if (n.kind !== "target") continue;
    if (!referencedByIntraProject.has(n.id)) {
      n.attrs = n.attrs || {};
      n.attrs.anchor = "true";
    }
  }

  // Emit cycle warnings on the status line (deferred; boot reads this).
  const cycleWarnings = cycleProjects.map(
    (p) => "cycle detected in " + p.path + ": " + (p.cycle || []).join(" -> "),
  );

  return { nodes, links, cycleWarnings };
}
