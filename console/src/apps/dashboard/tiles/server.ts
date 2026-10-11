// server.ts - `magus server`: the person-started process this console is talking to. It serves
// MCP, the console and the APIs, runs background jobs, and keeps each watched workspace's graph and
// symbol indexes current. The card answers which build is answering, since a server left running
// across an upgrade serves the older code, and where else it can be reached.

import { relTime, type DashboardState, type ServerView } from "../state";
import { Card, h, type Tile } from "./card";
import { factList, type Fact } from "./widgets";

export function serverTile(): Tile {
  const card = new Card("server", "Server", {
    term: "Server",
    label: "server",
    why:
      "The process behind this console, MCP and background jobs. A version older than the magus" +
      " you run means an upgrade has not reached it yet: restart it to serve the new build.",
  });
  const body = h("div", "console-dashboard-facts");
  card.body.append(body);

  let painted = "";
  function render(sv: ServerView | null): void {
    const rows: Fact[] = [];
    if (!sv) {
      rows.push({ term: "State", value: "did not report itself" });
    } else {
      rows.push({ term: "Process", value: "pid " + sv.pid });
      rows.push({ term: "Version", value: sv.version || "unknown" });
      const up = relTime(sv.startTime);
      if (up) rows.push({ term: "Up", value: up });
      for (const l of sv.listeners) rows.push({ term: l.kind || "Listener", value: l.address });
      if (sv.watch.length)
        rows.push({
          term: "Watching",
          value: sv.watch.length === 1 ? sv.watch[0] : sv.watch.length + " workspaces",
        });
    }
    const signature = JSON.stringify(rows.map((r) => [r.term, r.value]));
    if (signature === painted) return;
    painted = signature;
    body.replaceChildren(factList(rows));
  }

  return {
    el: card.el,
    update(s: DashboardState) {
      if (s.status) render(s.status.server);
    },
    destroy() {},
  };
}
