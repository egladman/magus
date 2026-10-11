// locks.ts - the per-project workspace locks held right now, with the process holding each. A lock
// serializes MUTATING magus invocations against one another, so one being held is what a working
// run looks like: the card is never styled as a fault, and it says plainly when nothing is held,
// which is the common case.
//
// It exists because the failure it makes visible is otherwise invisible. An OS file lock carries no
// identity of its own, and it lives for exactly as long as the process holding it, so a run nobody
// remembers starting holds one indefinitely while every other run is refused by a holder nobody sees.
// Age is the column that separates the two readings: seconds is a peer mid-run, days is abandoned.

import { relTime, tsMillisOrNow, type DashboardState, type LockView } from "../state";
import { statusIcon } from "../../../ui/status";
import { Card, countBadge, h, type Tile } from "./card";
import { fitRows } from "./density";

export function locksTile(): Tile {
  const card = new Card("locks", "Workspace locks", {
    term: "Lock",
    label: "workspace locks",
    why:
      "A held lock is just a mutating run, so this is normal. Age is what matters. Seconds means a" +
      " peer is working; hours means an orphan is blocking every other run.",
  });
  const count = countBadge("locks held");
  card.noteNode().replaceWith(count.el);
  const list = h("ul", "console-dashboard-rowlist");
  card.body.append(list);

  function render(locks: LockView[]): void {
    count.set(locks.length);
    card.setEmpty(locks.length === 0 ? "No workspace locks are held." : null);
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
      // exists means the holder outlived its worktree and will never release on its own. It wraps
      // onto its own line rather than scrolling or clipping, so the whole path is always readable.
      if (l.dir) detail.push(l.dir);
      meta.textContent = detail.join(", ");
      li.append(name, meta);

      // The command is the other half of naming a holder from another worktree.
      if (l.command) li.append(h("code", "console-dashboard-row__detail", l.command));

      // The threshold comes from the server, not from a constant here: it was decided
      // in two places once, and a CLI warning sat beside a dashboard row styled healthy.
      const staleAfterMs = l.staleAfterSeconds * 1000;
      const heldMs = l.acquireTime ? Date.now() - tsMillisOrNow(l.acquireTime) : 0;
      if (staleAfterMs > 0 && heldMs > staleAfterMs) {
        // Marks the row rather than the card: one abandoned holder among several busy ones should
        // stand out without recolouring the whole tile. The border is the colour half; the icon
        // and the sentence are the shape and the word.
        li.dataset.stale = "true";
        const note = h("p", "console-dashboard-row__warn");
        note.append(
          statusIcon("warning"),
          document.createTextNode(
            " Held long enough that the holder may be abandoned rather than busy.",
          ),
        );
        li.append(note);
      }
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
    },
  };
}
