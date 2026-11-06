import type { AppManifest } from "../manifest";

export const pulls: AppManifest = {
  id: "pulls",
  label: "Pulls",
  hint: "Open pull requests, their checks, and what blocks them",
  glyph:
    '<circle cx="6" cy="6" r="2.2"/><circle cx="6" cy="18" r="2.2"/><circle cx="18" cy="18" r="2.2"/><path d="M6 8.2v7.6"/><path d="M18 15.8V9.5a3 3 0 0 0-3-3h-3.5" data-motion="sweep"/><path d="m13.5 4.2-2.2 2.3 2.2 2.3"/>',
  motion: "sweep",
  path: "pulls",
  // Indigo, not a green: the greens mean a healthy check.
  accent: "--console-indigo",
  server: {
    purpose:
      "Pulls shows the open pull requests of this workspace's forge repository, read from the server's last sync.",
  },
  load: { kind: "module", css: "pulls/pulls.css" },
};
