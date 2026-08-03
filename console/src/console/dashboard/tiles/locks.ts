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
import { fitRows, marqueeOnOverflow } from "./density";

export function locksTile(): Tile {
  const card = new Card("locks", "Workspace locks", {
    term: "Lock",
    label: "workspace locks",
    why:
      "A held lock is just a mutating run, so this is normal. Age is what matters. Seconds means a" +
      " peer is working; hours means an orphan is blocking every other run.",
  });
  const countLabel = h("span", "pf-v6-c-label pf-m-compact");
  const count = h("span", "pf-v6-c-label__content", "0");
  countLabel.append(count);
  card.noteNode().replaceWith(countLabel);
  const list = h("ul", "console-dashboard-rowlist");
  card.body.append(list);

  // One teardown per rendered row's marquee. The list is rebuilt wholesale on every status frame, so
  // the previous rows' ResizeObservers have to be disconnected with them or the tile accumulates one
  // live observer per row per second, all pointed at detached nodes.
  const marquees: (() => void)[] = [];

  function render(locks: LockView[]): void {
    card.el.hidden = locks.length === 0;
    count.textContent = String(locks.length);
    for (const stop of marquees.splice(0)) stop();
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
      // Surfaced next to the holder because a lock with nobody waiting costs nothing;
      // the count is what turns "held" into "blocking".
      if (l.waiters.length) {
        detail.push(l.waiters.length + (l.waiters.length === 1 ? " waiting" : " waiting"));
      }
      // The text goes in an inner span so the marquee has something to translate that is not the
      // clipping box (density.ts). The holder's directory is the reason: it is the longest value on
      // the row and the one that decides whether a lock is a peer mid-run or an abandoned process.
      const scroll = h("span", "console-dashboard-row__scroll", detail.join(" - "));
      meta.append(scroll);
      marquees.push(marqueeOnOverflow(meta, scroll));

      // Marks the row rather than the card: one abandoned holder among several busy ones
      // should stand out without recolouring the whole tile.
      const tip: string[] = [];
      if (l.command) tip.push(l.command);
      // The threshold comes from the daemon, not from a constant here: it was decided
      // in two places once, and a CLI warning sat beside a dashboard row styled healthy.
      const staleAfterMs = l.staleAfterSeconds * 1000;
      const heldMs = l.acquireTime ? Date.now() - tsMillis(l.acquireTime) : 0;
      if (staleAfterMs > 0 && heldMs > staleAfterMs) {
        li.dataset.stale = "true";
        tip.push("Held long enough that the holder may be abandoned rather than busy.");
      }
      if (tip.length) li.title = tip.join("\n");

      li.append(name, meta);
      list.append(li);
    }
  }

  // Trim to the slot in Big Picture, where the card cannot scroll. The residual line reports the
  // STALE count alongside the plain one, because that is the fact a truncated lock list can hide
  // most expensively: a bare "+6 more" reads as routine, while "+6 more, 2 stale" is the whole
  // reason to walk over to the machine. A no-op wherever the list already fits.
  const unfit = fitRows(card.body, list, (hidden, rows) => {
    const stale = rows.filter((r) => r.dataset.stale === "true").length;
    return "+" + String(hidden) + " more" + (stale > 0 ? ", " + String(stale) + " stale" : "");
  });

  return {
    el: card.el,
    update(s: DashboardState) {
      if (s.status) render(s.status.locks);
    },
    destroy() {
      unfit();
      for (const stop of marquees.splice(0)) stop();
    },
  };
}
