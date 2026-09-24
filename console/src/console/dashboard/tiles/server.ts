// server.ts - `magus server`: the person-started process this console is talking to. It serves
// MCP, the console and the APIs, runs background jobs, and keeps each watched workspace's graph and
// symbol indexes current. The card answers which build is answering, since a server left running
// across an upgrade serves the older code, and where else it can be reached.

import { relTime, type DashboardState, type ServerView } from "../state";
import { Card, h, type Tile } from "./card";

function fact(label: string, value: string): HTMLElement {
  const li = h("li", "console-dashboard-row");
  li.append(
    h("code", "console-dashboard-row__cmd", label),
    h("span", "console-dashboard-row__meta", value),
  );
  return li;
}

export function serverTile(): Tile {
  const card = new Card("server", "Server", {
    term: "Server",
    label: "server",
    why:
      "The process behind this console, MCP and background jobs. A version older than the magus" +
      " you run means an upgrade has not reached it yet: restart it to serve the new build.",
  });
  const facts = h("ul", "console-dashboard-rowlist");
  card.body.append(facts);

  function render(sv: ServerView | null): void {
    card.el.hidden = false;
    if (!sv) {
      facts.replaceChildren(fact("state", "did not report itself"));
      return;
    }
    const rows = [fact("process", "pid " + sv.pid), fact("version", sv.version || "unknown")];
    const up = relTime(sv.startTime);
    if (up) rows.push(fact("up", up));
    for (const l of sv.listeners) rows.push(fact(l.kind || "listener", l.address));
    if (sv.watch.length)
      rows.push(
        fact("watching", sv.watch.length === 1 ? sv.watch[0] : sv.watch.length + " workspaces"),
      );
    facts.replaceChildren(...rows);
  }

  return {
    el: card.el,
    update(s: DashboardState) {
      if (s.status) render(s.status.server);
      else card.el.hidden = true;
    },
    destroy() {},
  };
}
