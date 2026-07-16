// versions.ts - the footer strip: magus + daemon versions. Deep-links the Daemon term.

import type { DashboardState } from "../state";
import { glossaryLink } from "../../../lib/glossary";
import { h, type Tile } from "./card";

export function versionsTile(): Tile {
  const footer = h("footer", "console-dashboard-versions");
  const magus = h("span");
  const daemon = h("span");
  footer.append(magus, daemon);

  return {
    el: footer,
    update(s: DashboardState) {
      if (!s.status) return;
      magus.textContent = s.status.magusVersion ? "magus " + s.status.magusVersion : "";
      daemon.replaceChildren();
      if (s.status.daemonVersion) {
        daemon.append(glossaryLink("Daemon", { label: "daemon" }), document.createTextNode(" " + s.status.daemonVersion));
      }
    },
    destroy() {},
  };
}
