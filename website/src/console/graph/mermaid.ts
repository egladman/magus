// mermaid.ts - Phase 7: Copy as Mermaid. Pure serializer extracted from the
// graph-explorer monolith; touches no module state (main.ts owns mermaidScope /
// copyAsMermaid, which pick the node/link subset and drive the clipboard).
//
// toMermaid emits a Mermaid flowchart for a node+link subset. The output FORMAT
// is intentionally identical to the Go CLI emitters so that a diagram copied
// from the explorer would render identically to one produced by the CLI:
//
//   targets flavor  ->  WriteTargetGraphMermaid  (internal/render/targetgraph.go)
//   knowledge flavor -> WriteKnowledgeMermaid    (internal/render/knowledgegraph.go)
//
// ANTI-DRIFT: the classDef names and node shapes below MUST match the Go emitters.
// A Go test in internal/render/mermaid_drift_test.go reads this file from disk
// and asserts each name appears verbatim, so a rename on either side fails CI.
// See the KEYWORDS mirror pattern in website/src/playground/editor.js for the house idiom.
//
// Go source literals copied for each flavor:
//
//   targets  - graph_ir.go shapeRounded ("...") / shapeHexagon {{""}} / shapeSubroutine [[""]]
//              targetgraph.go targetRoleClasses: "anchor", "target"
//              targetgraph.go externalClass:     "external"
//              targetgraph.go spellClass:        "spell"
//
//   knowledge - knowledgegraph.go prefix "kind_" + mermaidID(kind)
//               kinds seen in the graph determine the set, e.g. kind_project, kind_spell, ...
//
// Node-shape mapping (matches graph_ir.go emitNode switch):
//   shapeRounded    ->  id("label")
//   shapeHexagon    ->  id{{"label"}}
//   shapeSubroutine ->  id[["label"]]
//   shapeBox        ->  id["label"]  (default)

import type { GLink, GNode, GraphFlavor } from "./types.js";

export function toMermaid(nodes: GNode[], links: GLink[], flavor: GraphFlavor): string {
  // Stable ordering: sort nodes and links by id for determinism.
  const sortedNodes = [...nodes].sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  const sortedLinks = [...links].sort((a, b) => {
    const as = a.source.id || a.source,
      at = a.target.id || a.target;
    const bs = b.source.id || b.source,
      bt = b.target.id || b.target;
    if (as < bs) return -1;
    if (as > bs) return 1;
    if (at < bt) return -1;
    if (at > bt) return 1;
    return 0;
  });

  // Build a Mermaid-safe id from a node id: replace any non-word characters.
  // Mirrors the Go mermaidID helper (internal/render/render.go).
  const mermaidID = (s: string): string => s.replace(/[^A-Za-z0-9_]/g, "_");

  // Assign stable aliases keyed by node id.
  const alias = new Map<string, string>();
  // Sort the raw ids first, then enumerate, so aliases are deterministic.
  const rawIDs = [...new Set(sortedNodes.map((n) => n.id))].sort();
  rawIDs.forEach((id, i) => alias.set(id, "n" + i));

  const lines = ["graph LR"];

  if (flavor === "targets") {
    // --- targets flavor: mirror WriteTargetGraphMermaid ---
    // Shapes per kind:
    //   project   -> shapeSubroutine [["..."]]  (cross-project / external box)
    //   target    -> shapeRounded    ("...")
    //   anchor    -> shapeRounded    ("...")     (same shape, different class)
    //   spell     -> shapeHexagon    {{"..."}}
    // Classes: anchor, target, external, spell  (exact names from targetgraph.go)
    for (const n of sortedNodes) {
      const a = alias.get(n.id);
      const lbl = (n.label || n.id).replace(/"/g, "'");
      let shape;
      if (n.kind === "spell") {
        shape = `${a}{{"${lbl}"}}`;
      } else if (n.kind === "project") {
        // INTENTIONAL adaptation: Go's WriteTargetGraphMermaid renders projects as
        // subgraphs (not nodes); the explorer synthesizes real project nodes (Phase 3)
        // so we borrow the external/subroutine vocabulary ([[...]]/external class) for them.
        shape = `${a}[["${lbl}"]]`;
      } else {
        // target and anchor nodes are both shapeRounded
        shape = `${a}("${lbl}")`;
      }
      lines.push("  " + shape);
    }
    // Edges
    const seen = new Set<string>();
    for (const e of sortedLinks) {
      const s = alias.get(e.source.id || e.source);
      const t = alias.get(e.target.id || e.target);
      if (!s || !t || s === t) continue;
      const key = s + "\x00" + t;
      if (seen.has(key)) continue;
      seen.add(key);
      const dashed = e.dashed || e.cycle || e.layoutReversed;
      const arrow = dashed ? "-.->" : "-->";
      const lbl = e.relation ? `|"${e.relation}"|` : "";
      lines.push(`  ${s} ${arrow}${lbl} ${t}`);
    }
    // classDefs - exact names from targetgraph.go targetRoleClasses / externalClass / spellClass
    lines.push("  classDef anchor fill:#2563eb,color:#ffffff,stroke:#1e40af,stroke-width:2px");
    lines.push("  classDef target fill:#e2e8f0,color:#0f172a,stroke:#94a3b8");
    lines.push("  classDef external fill:#fef9c3,color:#713f12,stroke:#ca8a04,stroke-dasharray:5 3");
    lines.push("  classDef spell fill:#ede9fe,color:#4c1d95,stroke:#a78bfa");
    // class assignments
    const byClass: Record<string, string[]> = { anchor: [], target: [], external: [], spell: [] };
    for (const n of sortedNodes) {
      const a = alias.get(n.id)!;
      if (n.kind === "spell") {
        byClass.spell.push(a);
      } else if (n.kind === "project") {
        byClass.external.push(a);
      } else if (n.attrs && n.attrs.anchor === "true") {
        byClass.anchor.push(a);
      } else {
        byClass.target.push(a);
      }
    }
    for (const [cls, ids] of Object.entries(byClass)) {
      if (ids.length) lines.push("  class " + ids.join(",") + " " + cls);
    }
  } else {
    // --- knowledge flavor: mirror WriteKnowledgeMermaid ---
    // Shapes per kind (matches knowledgeShape in knowledgegraph.go):
    //   project -> shapeHexagon    {{"..."}}
    //   spell   -> shapeRounded    ("...")
    //   doc     -> shapeSubroutine [["..."]]
    //   default -> shapeBox        ["..."]
    for (const n of sortedNodes) {
      const a = alias.get(n.id);
      const lbl = (n.label || n.id).replace(/"/g, "'");
      let shape;
      if (n.kind === "project") {
        shape = `${a}{{"${lbl}"}}`;
      } else if (n.kind === "spell") {
        shape = `${a}("${lbl}")`;
      } else if (n.kind === "doc") {
        shape = `${a}[["${lbl}"]]`;
      } else {
        shape = `${a}["${lbl}"]`;
      }
      lines.push("  " + shape);
    }
    // Edges
    const seen = new Set<string>();
    for (const e of sortedLinks) {
      const s = alias.get(e.source.id || e.source);
      const t = alias.get(e.target.id || e.target);
      if (!s || !t || s === t) continue;
      const key = s + "\x00" + t + "\x00" + (e.relation || "");
      if (seen.has(key)) continue;
      seen.add(key);
      const lbl = e.relation ? `|"${e.relation}"|` : "";
      lines.push(`  ${s} -->${lbl} ${t}`);
    }
    // classDefs: kind_<kind> for each kind present (sorted for determinism).
    // Palette keys are the FULL classDef names (kind_<kind>) mirroring
    // knowledgeKindPalette in knowledgegraph.go. The names below are the EXACT
    // strings the Go CLI emits; mermaid_drift_test.go asserts they appear here.
    const kindClassPalette: Record<string, { fill: string; text: string }> = {
      kind_project: { fill: "#00ADD8", text: "#fff" },
      kind_target: { fill: "#3178C6", text: "#fff" },
      kind_spell: { fill: "#5d4d7a", text: "#fff" },
      kind_op: { fill: "#8a7ca8", text: "#fff" },
      kind_charm: { fill: "#b5651d", text: "#fff" },
      kind_module: { fill: "#2e8b57", text: "#fff" },
      kind_method: { fill: "#3cb371", text: "#000" },
      kind_diagnostic: { fill: "#c0392b", text: "#fff" },
      kind_doc: { fill: "#d4a017", text: "#000" },
    };
    const kindsPresent = [...new Set(sortedNodes.map((n) => n.kind))].sort();
    for (const k of kindsPresent) {
      const cls = "kind_" + mermaidID(k);
      const p = kindClassPalette[cls] || { fill: "#888888", text: "#fff" };
      lines.push(`  classDef ${cls} fill:${p.fill},color:${p.text}`);
    }
    // class assignments
    const byKind = new Map<string, string[]>();
    for (const n of sortedNodes) {
      const cls = "kind_" + mermaidID(n.kind);
      if (!byKind.has(cls)) byKind.set(cls, []);
      byKind.get(cls)!.push(alias.get(n.id)!);
    }
    const sortedKinds = [...byKind.keys()].sort();
    for (const k of sortedKinds) {
      lines.push("  class " + byKind.get(k)!.join(",") + " " + k);
    }
  }

  return lines.join("\n");
}
