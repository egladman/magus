// services.ts - the long-running shared services the broker is hosting right now: containers or
// processes kept warm across runs and deduped machine-wide, each with its published ports, run state,
// and the count of targets currently depending on it. This belongs to the machine scope (services are
// broker-global, not per-workspace). The card hides itself when nothing is hosted, since most
// workspaces run no services. Heading deep-links the Service glossary term.

import type { DashboardState, ServiceView } from "../state";
import { statusIcon, type Status } from "../../../ui/status";
import { Card, countBadge, h, type Tile } from "./card";

// The service states the broker reports, as the shape that goes beside the word.
const SERVICE_STATUS: Record<string, Status> = {
  running: "success",
  starting: "warning",
  failed: "danger",
  idle: "neutral",
};

export function servicesTile(): Tile {
  const card = new Card("services", "Shared services", {
    term: "Service",
    label: "shared services",
    why:
      "Processes magus keeps warm between runs, so tests do not restart a database every time." +
      " Read the dependent count: zero dependents means something holds a port for nobody.",
  });
  const count = countBadge("services");
  card.noteNode().replaceWith(count.el);
  const list = h("ul", "console-dashboard-rowlist");
  card.body.append(list);

  function render(svcs: ServiceView[]): void {
    // No services is the common case; the card keeps its place and says so, so the board does not
    // change shape when one starts.
    count.set(svcs.length);
    card.setEmpty(svcs.length === 0 ? "No shared services are hosted right now." : null);
    list.replaceChildren();
    for (const s of svcs) {
      const li = h("li", "console-dashboard-row");
      const name = h("code", "console-dashboard-row__cmd", s.label || s.command);
      const meta = h("span", "console-dashboard-row__meta console-dashboard-service__meta");
      const state = h("span", "console-dashboard-service__state");
      state.dataset.state = s.state;
      state.append(statusIcon(SERVICE_STATUS[s.state] ?? "neutral"), s.state || "unknown");
      const detail: string[] = [];
      if (s.ports.length) detail.push(s.ports.join(", "));
      detail.push(s.dependents + (s.dependents === 1 ? " dependent" : " dependents"));
      meta.append(state, document.createTextNode(" " + detail.join(", ")));
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
