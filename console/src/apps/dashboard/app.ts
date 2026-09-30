import type { AppManifest } from "../manifest";

export const dashboard: AppManifest = {
  id: "dashboard",
  label: "Dashboard",
  hint: "What magus is doing right now",
  // A small bar chart on a baseline; the tall bar grows on hover.
  glyph:
    '<path d="M3 21h18"/><rect x="5" y="11" width="4" height="8" rx="1"/><rect data-motion="bars" x="10" y="6" width="4" height="13" rx="1"/><rect x="15" y="14" width="4" height="5" rx="1"/>',
  motion: "bars",
  path: "dashboard",
  // Every `magus job` console link prints /console/plan/, which is the Jobs view.
  modes: { plan: "jobs" },
  accent: "--console-moss",
  server: { purpose: "The dashboard streams a running server's pool, cache, and health." },
  load: { kind: "page", css: "dashboard/dashboard.css" },
};
