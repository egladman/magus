// locks.ts - the per-project workspace locks held right now, with the process holding each. A lock
// serializes MUTATING magus invocations against one another, so one being held is what a working
// run looks like: the card is never styled as a fault, and it hides itself when nothing is held,
// which is the common case.
//
// It exists because the failure it makes visible is otherwise invisible. An OS file lock carries no
// identity of its own, and it lives for exactly as long as the process holding it, so a run nobody
// remembers starting holds one indefinitely while every other run simply waits with no explanation.
// Age is the column that separates the two readings: seconds is a peer mid-run, days is abandoned.

import { relTime, tsMillis, type DashboardState, type LockView } from "../state";
import { Card, h, type Tile } from "./card";

// STALE_AFTER_MS is when a held lock stops reading as normal work. Well past any honest build, so a
// busy peer is never flagged; past it, "still building" stops being the likely explanation.
const STALE_AFTER_MS = 10 * 60 * 1000;

export function locksTile(): Tile {
  const card = new Card("locks", "Workspace locks", {
    term: "Lock",
    label: "workspace locks",
  });
  const countLabel = h("span", "pf-v6-c-label pf-m-compact");
  const count = h("span", "pf-v6-c-label__content", "0");
  countLabel.append(count);
  card.noteNode().replaceWith(countLabel);
  const list = h("ul", "console-dashboard-rowlist");
  card.body.append(list);

  function render(locks: LockView[]): void {
    card.el.hidden = locks.length === 0;
    count.textContent = String(locks.length);
    list.replaceChildren();
    for (const l of locks) {
      const li = h("li", "console-dashboard-row");
      const name = h("code", "console-dashboard-row__cmd", l.project || ".");
      const meta = h("span", "console-dashboard-row__meta");

      const age = relTime(l.acquireTime);
      const detail: string[] = [];
      if (age) detail.push("held " + age);
      if (l.pid) detail.push("pid " + l.pid);
      // The holder's directory is what settles an ambiguous case: a path that no longer
      // exists means the holder outlived its worktree and will never release on its own.
      if (l.dir) detail.push(l.dir);
      meta.textContent = detail.join(" - ");

      // Marks the row rather than the card: one abandoned holder among several busy ones
      // should stand out without recolouring the whole tile.
      const tip: string[] = [];
      if (l.command) tip.push(l.command);
      if (l.acquireTime && Date.now() - tsMillis(l.acquireTime) > STALE_AFTER_MS) {
        li.dataset.stale = "true";
        tip.push("Held long enough that the holder may be abandoned rather than busy.");
      }
      if (tip.length) li.title = tip.join("\n");

      li.append(name, meta);
      list.append(li);
    }
  }

  return {
    el: card.el,
    update(s: DashboardState) {
      if (s.status) render(s.status.locks);
    },
    destroy() {},
  };
}
