// workspaces.ts - loaded workspaces, each with its own cache tallies. Heading
// deep-links the Workspace glossary term.

import type { DashboardState, WorkspaceView } from "../state";
import { relTime } from "../state";
import { Card, h, type Tile } from "./card";

export function workspacesTile(): Tile {
  const card = new Card("workspaces", "Workspaces", { term: "Workspace", label: "workspaces" });
  const count = h("span", "tile-count", "0");
  card.noteNode().replaceWith(count);
  const list = h("ul", "row-list");
  const empty = h("p", "row-empty", "No workspaces loaded.");
  card.body.append(list, empty);

  function render(wss: WorkspaceView[]): void {
    count.textContent = String(wss.length);
    empty.hidden = wss.length > 0;
    list.replaceChildren();
    for (const w of wss) {
      const li = h("li", "row");
      const root = h("code", "row-cmd", w.root);
      const meta = h("span", "row-ws-cache");
      if (w.hits != null) {
        const mk = (cls: string, label: string, v: number): HTMLElement => h("span", cls, label + " " + (v || 0));
        meta.append(mk("h", "H", w.hits), mk("m", "M", w.misses ?? 0));
        if ((w.errors ?? 0) > 0) meta.append(mk("e", "E", w.errors ?? 0));
      } else {
        meta.textContent = relTime(w.lastAccessTime);
      }
      li.append(root, meta);
      list.append(li);
    }
  }

  return {
    el: card.el,
    update(s: DashboardState) { if (s.status) render(s.status.workspaces); },
    destroy() {},
  };
}
