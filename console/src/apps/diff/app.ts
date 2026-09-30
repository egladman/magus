import type { AppManifest } from "../manifest";

export const diff: AppManifest = {
  id: "diff",
  label: "Diff",
  hint: "Read what you have changed but not committed",
  // A split view; the left column grows on hover, the one motion that reads as text arriving.
  glyph:
    '<path d="M12 4v16"/><path data-motion="bars" d="M4 8h5M4 12h6M4 16h4"/><path d="M15 8h5M15 12h4M15 16h5"/>',
  motion: "bars",
  path: "diff",
  // Spruce, kept clear of the dashboard's moss so it does not read as health.
  accent: "--console-spruce",
  server: { purpose: "Diff reads the working tree through a local server." },
  load: { kind: "module", css: "diff/diff.css" },
};
