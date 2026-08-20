// workspaces.ts - loaded workspaces, each with its own cache tallies. Heading
// deep-links the Workspace glossary term.

import type { DashboardState, WorkspaceView } from "../state";
import { publishWorkspaces } from "../../../lib/scope";
import { relTime } from "../state";
import { Card, h, type Tile } from "./card";
import { fitRows } from "./density";
// The scope this tile highlights against. Narrowed to what it actually reads - a value and a
// subscription - so it can be fed either a persisted cell or the shell's per-tab workspace scope
// without either having to pretend to be the other.
export interface ScopeReader {
  get(): string;
  subscribe(fn: (value: string) => void): () => void;
}

// activeWorkspace, when given, is the workspace THIS BROWSER TAB is scoped to (lib/scope.ts, driven
// by the title bar's scope picker): the matching row gets [data-active], so the tab's scope has a
// visible anchor here - the one place per-workspace data (this daemon's cache tallies) actually
// exists to scope to. Optional: the standalone case renders unhighlighted.
export function workspacesTile(activeWorkspace?: ScopeReader): Tile {
  const card = new Card("workspaces", "Workspaces", {
    term: "Workspace",
    label: "workspaces",
    why:
      "Which checkouts this daemon holds warm, and how well the cache serves each. One with a far" +
      " worse hit rate is usually a worktree whose absolute paths differ, so it reuses nothing.",
  });
  const countLabel = h("span", "pf-v6-c-label pf-m-compact");
  const count = h("span", "pf-v6-c-label__content", "0");
  countLabel.append(count);
  card.noteNode().replaceWith(countLabel);
  const list = h("ul", "console-dashboard-rowlist");
  const empty = h("p", "console-dashboard-row__empty", "No workspaces loaded.");
  card.body.append(list, empty);

  function render(wss: WorkspaceView[]): void {
    // Tell the shell which workspaces exist. This tile is the one place that always has the list -
    // live from the status stream, and synthetic in the demo - so the title bar's scope picker can
    // offer them without a second call, and can offer them offline at all.
    publishWorkspaces(wss.map((w) => w.root).filter((r) => r !== ""));
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
      // A declared secret provider gets a one-glyph slot before the path: enough to say
      // credential resolution is wired up and through what, without a panel for it.
      //
      // Shown ONLY when a magusfile declared one. The built-in environment provider always
      // applies, so marking it too would put a badge on every row and stop meaning
      // anything. Absence here reads as "nothing declared", which is the truth.
      //
      // The initial, not a vendor logo: a provider is whatever spell the workspace names,
      // so there is no bounded set to ship art for - and a letter carries no trademark.
      // Square and monospaced on purpose; a round one reads as a person's avatar.
      if (w.secretProvider) {
        const slot = h("span", "console-dashboard-row__secret", w.secretProvider[0]);
        slot.title = "secrets via " + w.secretProvider;
        li.append(slot);
      }
      li.append(root, meta);
      list.append(li);
    }
  }

  // Repaint the highlight the instant the switcher's pick changes, not on the next status tick.
  let lastStatus: DashboardState["status"] = null;
  activeWorkspace?.subscribe(() => {
    if (lastStatus) render(lastStatus.workspaces);
  });

  // On the board this list simply grows its card and the page scrolls. In Big Picture the card is a
  // fixed grid slot with no scrollbar, so a longer list would lose its tail behind overflow: hidden
  // and read as though the daemon had fewer workspaces than it has. fitRows trims to fit and says
  // what it dropped; it is a no-op whenever everything already fits, which is the board's case.
  const unfit = fitRows(card.body, list, (hidden) => "+" + String(hidden) + " more");

  return {
    el: card.el,
    update(s: DashboardState) {
      lastStatus = s.status;
      if (s.status) render(s.status.workspaces);
    },
    destroy() {
      unfit();
    },
  };
}
