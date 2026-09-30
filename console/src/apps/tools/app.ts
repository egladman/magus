import type { AppManifest } from "../manifest";

export const tools: AppManifest = {
  id: "tools",
  label: "Tools",
  hint: "The binaries this workspace drives, their versions and support",
  // A toolbox: the latch presses on hover, which is all a box needs to move. A wrench alone would
  // read as Settings.
  glyph:
    '<path d="M9 7V5.5A1.5 1.5 0 0 1 10.5 4h3A1.5 1.5 0 0 1 15 5.5V7"/><rect x="3" y="7" width="18" height="13" rx="2"/><path d="M3 13h18"/><rect data-motion="press" x="10" y="11.5" width="4" height="3" rx="0.8"/>',
  motion: "press",
  path: "tools",
  // Sage is the one palette slot no app has taken.
  accent: "--console-sage",
  server: { purpose: "Tools reads the binaries your local server probes for this workspace." },
  load: { kind: "module", css: "tools/tools.css" },
};
