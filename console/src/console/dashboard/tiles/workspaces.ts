// workspaces.ts - loaded workspaces, each with its own cache tallies. Heading
// deep-links the Workspace glossary term.

import type { DashboardState, WorkspaceView } from "../state";
import { relTime } from "../state";
import { Card, h, type Tile } from "./card";
import type { Persisted } from "../../../lib/persist";

// activeWorkspace, when given, is the dashboard header's active-workspace picker pick
// (tiles/bigPicture.ts): the matching row gets [data-active] so picking a workspace there has a
// visible effect here - the one place per-workspace data (this daemon's cache tallies) actually
// exists to scope to. Optional: the standalone case (no picker wired) renders unhighlighted.
export function workspacesTile(activeWorkspace?: Persisted<string>): Tile {
  const card = new Card("workspaces", "Workspaces", { term: "Workspace", label: "workspaces" });
  const countLabel = h("span", "pf-v6-c-label pf-m-compact");
  const count = h("span", "pf-v6-c-label__content", "0");
  countLabel.append(count);
  card.noteNode().replaceWith(countLabel);
  const list = h("ul", "console-dashboard-rowlist");
  const empty = h("p", "console-dashboard-row__empty", "No workspaces loaded.");
  card.body.append(list, empty);

  function render(wss: WorkspaceView[]): void {
    count.textContent = String(wss.length);
    empty.hidden = wss.length > 0;
    list.replaceChildren();
    const active = activeWorkspace?.get();
    for (const w of wss) {
      const li = h("li", "console-dashboard-row");
      if (active && w.root === active) li.dataset.active = "";
      const root = h("code", "console-dashboard-row__cmd", w.root);
      const meta = h("span", "console-dashboard-row__wscache");
      if (w.hits != null) {
        const mk = (cache: string, label: string, v: number): HTMLElement => {
          const s = h("span", undefined, label + " " + (v || 0));
          s.dataset.cache = cache;
          return s;
        };
        meta.append(mk("hit", "H", w.hits), mk("miss", "M", w.misses ?? 0));
        if ((w.errors ?? 0) > 0) meta.append(mk("err", "E", w.errors ?? 0));
      } else {
        meta.textContent = relTime(w.lastAccessTime);
      }
      li.append(root, meta);
      list.append(li);
    }
  }

  // Repaint the highlight the instant the switcher's pick changes, not on the next status tick.
  let lastStatus: DashboardState["status"] = null;
  activeWorkspace?.subscribe(() => { if (lastStatus) render(lastStatus.workspaces); });

  return {
    el: card.el,
    update(s: DashboardState) { lastStatus = s.status; if (s.status) render(s.status.workspaces); },
    destroy() {},
  };
}
