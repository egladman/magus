import type { AppManifest } from "../manifest";

export const activity: AppManifest = {
  id: "activity",
  // The bare noun, never "Trail": "audit trail" frames the app as governance, which it is not.
  // It also matches the service behind it (magus.activity.v1alpha1).
  label: "Activity",
  hint: "Everything that happened here, and what led to it",
  glyph: '<path data-motion="wave" d="M3 12h3l2-5 3 10 3-8 2 3h5"/>',
  motion: "wave",
  path: "activity",
  accent: "--console-rust",
  server: { purpose: "Activity records what the server did: MCP calls, jobs, config changes." },
  // The trail renders through the log viewer's sheet.
  load: { kind: "module", css: "logs/logs.css" },
};
