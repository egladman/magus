// calls.ts - in-flight calls. One row per running call; when the dashboard is
// connected AND the call carries an invocation id, the row deep-links to that run's
// live log (host + invocation ride the log viewer's #live fragment).

import type { DashboardState, CallView } from "../state";
import { fmtArgs, relTime } from "../state";
import { Card, h, type Tile } from "./card";

export function callsTile(): Tile {
  const card = new Card("calls", "In-flight");
  // The tile-count chip lives in the head; reuse the note slot styled as a count.
  const count = h("span", "tile-count", "0");
  const headNote = card.el.querySelector(".tile-note");
  if (headNote) headNote.replaceWith(count);
  const list = h("ul", "row-list");
  const empty = h("p", "row-empty", "Pool is idle.");
  card.body.append(list, empty);

  function render(calls: CallView[], liveHost: string | null): void {
    count.textContent = String(calls.length);
    empty.hidden = calls.length > 0;
    list.replaceChildren();
    for (const c of calls) {
      const clickable = liveHost && c.invocation;
      const row = clickable ? h("a", "row") : h("li", "row");
      if (clickable) (row as HTMLAnchorElement).href = "../logs/#live=" + encodeURIComponent(liveHost) + "&inv=" + encodeURIComponent(c.invocation);
      const cmd = h("code", "row-cmd", fmtArgs(c.args));
      const bits: string[] = [];
      if (c.subOp) bits.push(c.subOp);
      const t = relTime(c.startTime);
      if (t) bits.push(t);
      const meta = h("span", "row-meta", bits.join(" · "));
      row.append(cmd, meta);
      list.append(row);
    }
  }

  return {
    el: card.el,
    update(s: DashboardState) { if (s.status) render(s.status.calls, s.liveHost); },
    destroy() {},
  };
}
