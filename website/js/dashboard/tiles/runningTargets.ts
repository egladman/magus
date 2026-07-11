// runningTargets.ts - the running targets. One row per running target; when the dashboard
// is connected AND the target carries an invocation id, the row deep-links to that run's
// live log (host + invocation ride the log viewer's #live fragment).

import type { DashboardState, RunningTargetView } from "../state";
import { fmtArgs, relTime } from "../state";
import { Card, h, type Tile } from "./card";

export function runningTargetsTile(): Tile {
  const card = new Card("running-targets", "Running");
  // The tile-count chip lives in the head; reuse the note slot styled as a count.
  const count = h("span", "tile-count", "0");
  card.noteNode().replaceWith(count);
  const list = h("ul", "row-list");
  const empty = h("p", "row-empty", "Pool is idle.");
  card.body.append(list, empty);

  function render(targets: RunningTargetView[], liveHost: string | null): void {
    count.textContent = String(targets.length);
    empty.hidden = targets.length > 0;
    list.replaceChildren();
    for (const c of targets) {
      const clickable = liveHost && c.invocation;
      const row = clickable ? h("a", "row") : h("li", "row");
      if (clickable) (row as HTMLAnchorElement).href = "../logs/#live=" + encodeURIComponent(liveHost) + "&inv=" + encodeURIComponent(c.invocation);
      const cmd = h("code", "row-cmd", fmtArgs(c.args));
      const bits: string[] = [];
      if (c.step) bits.push(c.step);
      const t = relTime(c.startTime);
      if (t) bits.push(t);
      const meta = h("span", "row-meta", bits.join(" - "));
      row.append(cmd, meta);
      list.append(row);
    }
  }

  return {
    el: card.el,
    update(s: DashboardState) { if (s.status) render(s.status.runningTargets, s.liveHost); },
    destroy() {},
  };
}
