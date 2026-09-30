import type { AppManifest } from "../manifest";

export const runs: AppManifest = {
  id: "runs",
  label: "Runs",
  hint: "Every run this workspace kept, no ref needed",
  // A stopwatch, not another stack of rows: the Log Viewer owns "lines of text" and Activity owns
  // the trail. The hand sweeps a FULL turn so the one-shot animation ends where it began.
  glyph:
    '<path d="M10 2.5h4"/><path d="M12 2.5v2.2"/><circle cx="12" cy="13.5" r="7.2"/><path data-motion="sweep" d="M12 13.5V9"/>',
  motion: "sweep",
  path: "runs",
  // Plum reads as neither neighbour: not Activity's rust trail, not the Log Viewer's indigo output.
  accent: "--console-plum",
  server: { purpose: "Runs reads the runs your local server has kept." },
  load: { kind: "module", css: "runs/runs.css" },
};
