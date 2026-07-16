// runningTargets.ts - the running targets. One row per running target; when the dashboard
// is connected AND the target carries an invocation id, the row deep-links to that run's
// live log (host + invocation ride the log viewer's #live fragment).

import type { DashboardState, RunningTargetView } from "../state";
import { fmtArgs, relTime } from "../state";
import { Card, h, type Tile } from "./card";

export function runningTargetsTile(): Tile {
  const card = new Card("running-targets", "Running");
  // The count chip lives in the head as a PatternFly Label; it replaces the note slot.
  const countLabel = h("span", "pf-v6-c-label pf-m-compact");
  const count = h("span", "pf-v6-c-label__content", "0");
  countLabel.append(count);
  card.noteNode().replaceWith(countLabel);
  const list = h("ul", "console-dashboard-rowlist");
  const empty = h("p", "console-dashboard-row__empty", "Pool is idle.");
  card.body.append(list, empty);

  function render(targets: RunningTargetView[], liveHost: string | null): void {
    count.textContent = String(targets.length);
    empty.hidden = targets.length > 0;
    list.replaceChildren();
    for (const c of targets) {
      const clickable = liveHost && c.invocation;
      const row = clickable ? h("a", "console-dashboard-row") : h("li", "console-dashboard-row");
      if (clickable) (row as HTMLAnchorElement).href = "../logs/#live=" + encodeURIComponent(liveHost) + "&inv=" + encodeURIComponent(c.invocation);
      const cmd = h("code", "console-dashboard-row__cmd", fmtArgs(c.args));
      const bits: string[] = [];
      if (c.step) bits.push(c.step);
      const t = relTime(c.startTime);
      if (t) bits.push(t);
      const meta = h("span", "console-dashboard-row__meta", bits.join(" - "));
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
