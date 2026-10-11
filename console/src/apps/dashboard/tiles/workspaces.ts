// workspaces.ts - loaded workspaces, each with its own cache tallies. Heading
// deep-links the Workspace glossary term.

import type { DashboardState, WorkspaceView } from "../state";
import { publishWorkspaces } from "../../../lib/scope";
import { relTime } from "../state";
import { Card, countBadge, h, type Tile } from "./card";
import { fitRows } from "./density";
// The scope this tile highlights against. Narrowed to what it actually reads - a value and a
// subscription - so it can be fed either a persisted cell or the shell's per-tab workspace scope
// without either having to pretend to be the other.
export interface WorkspaceReader {
  get(): string;
  subscribe(fn: (value: string) => void): () => void;
}

// activeWorkspace, when given, is the workspace THIS BROWSER TAB is scoped to (lib/scope.ts, driven
// by the title bar's scope picker): the matching row gets [data-active], so the tab's scope has a
// visible anchor here - the one place per-workspace data (this server's cache tallies) actually
// exists to scope to. Optional: the standalone case renders unhighlighted.
export function workspacesTile(activeWorkspace?: WorkspaceReader): Tile {
  const card = new Card("workspaces", "Workspaces", {
    term: "Workspace",
    label: "workspaces",
    why:
      "Which checkouts this server holds warm, and how well the cache serves each. One with a far" +
      " worse hit rate is usually a worktree whose absolute paths differ, so it reuses nothing.",
  });
  const count = countBadge("workspaces");
  card.noteNode().replaceWith(count.el);
  const list = h("ul", "console-dashboard-rowlist");
  card.body.append(list);

  function render(wss: WorkspaceView[]): void {
    // Tell the shell which workspaces exist. This tile is the one place that always has the list -
    // live from the status stream, and synthetic in the demo - so the title bar's scope picker can
    // offer them without a second call, and can offer them offline at all.
    publishWorkspaces(wss.map((w) => w.root).filter((r) => r !== ""));
    count.set(wss.length);
    card.setEmpty(wss.length === 0 ? "No workspaces are loaded." : null);
    list.replaceChildren();
    const active = activeWorkspace?.get();
    for (const w of wss) {
      const li = h("li", "console-dashboard-row");
      const isActive = !!active && w.root === active;
      if (isActive) li.dataset.active = "";
      const root = h("code", "console-dashboard-row__cmd", w.root);
      const meta = h("span", "console-dashboard-row__wscache");
      if (w.hits != null) {
        const mk = (cache: string, label: string, v: number): HTMLElement => {
          const s = h("span", undefined, label + " " + (v || 0));
          s.dataset.cache = cache;
          return s;
        };
        meta.append(mk("hit", "hits", w.hits), mk("miss", "misses", w.misses ?? 0));
        if ((w.errors ?? 0) > 0) meta.append(mk("err", "errors", w.errors ?? 0));
      } else {
        meta.textContent = relTime(w.lastAccessTime);
      }
      // The scope marker is a word, beside the row's border colour: the tab's workspace is the one
      // row here the reader has chosen, and colour alone cannot say which.
      if (isActive) {
        const tag = h("span", "pf-v6-c-label pf-m-compact pf-m-outline");
        tag.append(h("span", "pf-v6-c-label__content", "active"));
        li.append(tag);
      }
      li.append(root, meta);
      // A declared secret provider is named in words on its own line: enough to say credential
      // resolution is wired up and through what, without a panel for it.
      //
      // Shown ONLY when a magusfile declared one. The built-in environment provider always
      // applies, so naming it too would put a line on every row and stop meaning anything.
      // Absence here reads as "nothing declared", which is the truth.
      if (w.secretProvider) {
        li.append(h("p", "console-dashboard-row__detail", "secrets via " + w.secretProvider));
      }
      list.append(li);
    }
  }

  // Repaint the highlight the instant the switcher's pick changes, not on the next status tick.
  let lastStatus: DashboardState["status"] = null;
  // The subscription is a window listener (lib/scope.ts), so it outlives this tile unless destroy
  // drops it: the dashboard remounts its tiles on every reopen, and a leaked one holds the dead tile's
  // DOM and re-publishes the workspace list on every scope change.
  const unsubscribe =
    activeWorkspace?.subscribe(() => {
      if (lastStatus) render(lastStatus.workspaces);
    }) ?? ((): void => {});

  // On the board this list simply grows its card and the page scrolls. In Big Picture the card is a
  // fixed grid slot with no scrollbar, so a longer list would lose its tail behind overflow: hidden
  // and read as though the server had fewer workspaces than it has. fitRows trims to fit and says
  // what it dropped; it is a no-op whenever everything already fits, which is the board's case.
  const unfit = fitRows(card.body, list, (hidden) => "+" + String(hidden) + " more");

  return {
    el: card.el,
    update(s: DashboardState) {
      lastStatus = s.status;
      if (s.status) render(s.status.workspaces);
    },
    destroy() {
      unsubscribe();
      unfit();
    },
  };
}
