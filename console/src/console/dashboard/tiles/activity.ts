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
import { glossaryLink } from "../../../lib/glossary";
import { logsLink } from "../../../lib/daemon";
import { Card, h, type Tile } from "./card";

const PREVIEW_LINES = 120; // most recent captured lines kept in the streaming preview

export function activityTile(): Tile {
  const card = new Card("activity", "Live activity");

  // Header note: a running-count chip plus an "Open in log viewer" deep-link (repointed at
  // the live host on each render). Replaces the plain note span.
  const noteWrap = h("span", "console-dashboard-activity__note");
  const countLabel = h("span", "pf-v6-c-label pf-m-compact");
  const count = h("span", "pf-v6-c-label__content", "0");
  countLabel.append(count);
  const open = h("a", "console-dashboard-activity__open", "Open in log viewer");
  open.setAttribute("href", "../logs/");
  noteWrap.append(countLabel, open);
  card.noteNode().replaceWith(noteWrap);

  // A one-line guide: each running target is a trace an operator can open.
  const caption = h("p", "console-dashboard-activity__caption");
  caption.append(document.createTextNode("Each running target is a live "));
  caption.append(glossaryLink("Trace"));
  caption.append(document.createTextNode(" - open it to read the full output."));

  const list = h("ul", "console-dashboard-rowlist");
  const empty = h("p", "console-dashboard-row__empty", "Pool is idle. Nothing running right now.");
  const preview = h("pre", "console-dashboard-activity__log");
  preview.hidden = true;
  preview.setAttribute("aria-label", "Streaming output preview");
  card.body.append(caption, list, empty, preview);

  // Auto-follow the tail UNTIL the operator scrolls up to read - then freeze in place and let them
  // take control (like the log viewer's livePaused). Scrolling back to the bottom re-arms the follow.
  // The threshold absorbs sub-pixel rounding so "resting at the bottom" reliably counts as pinned.
  let pinned = true;
  preview.addEventListener("scroll", () => {
    pinned = preview.scrollHeight - preview.scrollTop - preview.clientHeight < 8;
  });

  function render(
    targets: RunningTargetView[],
    liveHost: string | null,
    logLines: string[],
    demo: boolean,
  ): void {
    count.textContent = String(targets.length);
    // Live: deep-link to the host's stream. Demo: stay inside the unified demo (../logs/#demo)
    // instead of dropping into the empty log viewer (demo has no live host). Otherwise plain.
    open.setAttribute(
      "href",
      liveHost ? logsLink(liveHost, {}) : demo ? "../logs/#demo" : "../logs/",
    );

    empty.hidden = targets.length > 0;
    list.replaceChildren();
    for (const c of targets) {
      const clickable = liveHost && c.invocation;
      const row = clickable ? h("a", "console-dashboard-row") : h("li", "console-dashboard-row");
      if (clickable) (row as HTMLAnchorElement).href = logsLink(liveHost, { inv: c.invocation });
      const cmd = h("code", "console-dashboard-row__cmd", fmtArgs(c.args));
      const bits: string[] = [];
      if (c.step) bits.push(c.step);
      const t = relTime(c.startTime);
      if (t) bits.push(t);
      row.append(cmd, h("span", "console-dashboard-row__meta", bits.join(" - ")));
      list.append(row);
    }

    // Streaming preview: only when a raw-output buffer is present (the demo feed). Keep the last
    // PREVIEW_LINES and follow the newest line so it reads as a live tail - BUT only while pinned.
    // When the operator has scrolled up we leave the preview frozen (content and position both) so
    // they can read without being yanked back down; scrolling to the bottom re-arms the follow.
    if (logLines.length > 0) {
      preview.hidden = false;
      if (pinned) {
        preview.textContent = logLines.slice(-PREVIEW_LINES).join("\n");
        preview.scrollTop = preview.scrollHeight;
      }
    } else {
      preview.hidden = true;
      preview.textContent = "";
      pinned = true; // reset so the next stream starts following again
    }
  }

  return {
    el: card.el,
    update(s: DashboardState) {
      if (s.status)
        render(s.status.runningTargets, s.liveHost, s.logLines, s.conn.state === "demo");
    },
    destroy() {},
  };
}
