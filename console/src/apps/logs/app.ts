import type { AppManifest } from "../manifest";

export const logs: AppManifest = {
  id: "logs",
  label: "Log Viewer",
  hint: "Read a run's captured output",
  glyph: '<path d="M4 5h16M4 10h10M4 15h13M4 19h7"/>',
  motion: null,
  path: "logs",
  accent: "--console-indigo",
  // No server mark: the viewer opens a file or a link's payload offline.
  load: { kind: "page", css: "logs/logs.css" },
};
