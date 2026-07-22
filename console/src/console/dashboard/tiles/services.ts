// services.ts - the long-running shared services the daemon is hosting right now: containers or
// processes kept warm across runs and deduped daemon-wide, each with its published ports, run state,
// and the count of targets currently depending on it. This belongs to the DAEMON scope (services are
// daemon-global, not per-workspace). The card hides itself when nothing is hosted, since most
// workspaces run no services. Heading deep-links the Service glossary term.

import type { DashboardState, ServiceView } from "../state";
import { Card, h, type Tile } from "./card";

export function servicesTile(): Tile {
  const card = new Card("services", "Shared services", {
    term: "Service",
    label: "shared services",
  });
  const countLabel = h("span", "pf-v6-c-label pf-m-compact");
  const count = h("span", "pf-v6-c-label__content", "0");
  countLabel.append(count);
  card.noteNode().replaceWith(countLabel);
  const list = h("ul", "console-dashboard-rowlist");
  card.body.append(list);

  function render(svcs: ServiceView[]): void {
    // No services is the common case, so the whole card steps aside rather than showing an empty row.
    card.el.hidden = svcs.length === 0;
    count.textContent = String(svcs.length);
    list.replaceChildren();
    for (const s of svcs) {
      const li = h("li", "console-dashboard-row");
      const name = h("code", "console-dashboard-row__cmd", s.label || s.command);
      const meta = h("span", "console-dashboard-row__meta console-dashboard-service__meta");
      const state = h("span", "console-dashboard-service__state", s.state || "unknown");
      state.dataset.state = s.state;
      const detail: string[] = [];
      if (s.ports.length) detail.push(s.ports.join(", "));
      detail.push(s.dependents + (s.dependents === 1 ? " dependent" : " dependents"));
      meta.append(state, document.createTextNode(" " + detail.join(" - ")));
      li.append(name, meta);
      list.append(li);
    }
  }

  return {
    el: card.el,
    update(s: DashboardState) {
      if (s.status) render(s.status.services);
    },
    destroy() {},
  };
}
