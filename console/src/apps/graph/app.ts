import type { AppManifest } from "../manifest";

export const graph: AppManifest = {
  id: "graph",
  label: "Graph Explorer",
  hint: "Start exploring the knowledge graph",
  // Three connected nodes; the lead node pulses on hover.
  glyph:
    '<circle data-motion="pulse" cx="6" cy="7" r="2.2"/><circle cx="18" cy="6" r="2.2"/><circle cx="15" cy="18" r="2.2"/><path d="M8 8l6 9M8 7l8-1"/>',
  motion: "pulse",
  path: "graph",
  modes: { diagrams: "figures" },
  accent: "--console-slate",
  // No server mark: the explorer opens a file or a snapshot offline.
  load: { kind: "page", css: "graph/graph.css" },
};
