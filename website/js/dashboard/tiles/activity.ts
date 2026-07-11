// activity.ts - the live-activity / log preview: "what is actually running right now",
// with a deep-link into the full log viewer. It renders one row per running target
// (command + current step + elapsed), each deep-linking to that run's live log when the
// dashboard is connected. When a raw-output buffer is present (the demo feed synthesizes
// one, see demo.ts), a rolling <pre> preview streams captured lines beneath the list so the
// board looks alive.
//
// Live-stream limitation: the daemon feed the dashboard holds (/api/v1/events) carries
// STATUS frames - pool, health, running targets - not a raw-output journal. The log viewer
// tails that journal from a DIFFERENT per-run endpoint. So in live mode this tile shows the
// running targets and their current step as the activity preview and links out to the log
// viewer for the actual output; a real in-dashboard tail would need a journal SSE consumer
// (future work), which we deliberately do not invent here.

import type { DashboardState, RunningTargetView } from "../state";
import { fmtArgs, relTime } from "../state";
import { glossaryLink } from "../../lib/glossary";
import { Card, h, type Tile } from "./card";

const PREVIEW_LINES = 120; // most recent captured lines kept in the streaming preview

export function activityTile(): Tile {
  const card = new Card("activity", "Live activity");

  // Header note: a running-count chip plus an "Open in log viewer" deep-link (repointed at
  // the live host on each render). Replaces the plain note span.
  const noteWrap = h("span", "activity-note");
  const count = h("span", "tile-count", "0");
  const open = h("a", "activity-open", "Open in log viewer");
  open.setAttribute("href", "../logs/");
  noteWrap.append(count, open);
  card.noteNode().replaceWith(noteWrap);

  // A one-line guide: each running target is a trace an operator can open.
  const caption = h("p", "activity-caption");
  caption.append(document.createTextNode("Each running target is a live "));
  caption.append(glossaryLink("Trace"));
  caption.append(document.createTextNode(" - open it to read the full output."));

  const list = h("ul", "row-list");
  const empty = h("p", "row-empty", "Pool is idle. Nothing running right now.");
  const preview = h("pre", "activity-log");
  preview.hidden = true;
  preview.setAttribute("aria-label", "Streaming output preview");
  card.body.append(caption, list, empty, preview);

  function render(targets: RunningTargetView[], liveHost: string | null, logLines: string[]): void {
    count.textContent = String(targets.length);
    open.setAttribute("href", liveHost ? "../logs/#live=" + encodeURIComponent(liveHost) : "../logs/");

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
      row.append(cmd, h("span", "row-meta", bits.join(" - ")));
      list.append(row);
    }

    // Streaming preview: only when a raw-output buffer is present (the demo feed). Keep the
    // last PREVIEW_LINES and pin the scroll to the newest line so it reads as a live tail.
    if (logLines.length > 0) {
      preview.hidden = false;
      preview.textContent = logLines.slice(-PREVIEW_LINES).join("\n");
      preview.scrollTop = preview.scrollHeight;
    } else {
      preview.hidden = true;
      preview.textContent = "";
    }
  }

  return {
    el: card.el,
    update(s: DashboardState) { if (s.status) render(s.status.runningTargets, s.liveHost, s.logLines); },
    destroy() {},
  };
}
