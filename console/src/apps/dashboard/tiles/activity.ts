// activity.ts - the live-activity / log preview: "what is actually running right now",
// with a deep-link into the full log viewer. It renders one row per running target
// (command + current step + elapsed), each deep-linking to that run's live log when the
// dashboard is connected. When a raw-output buffer is present (the demo feed synthesizes
// one, see demo.ts), a rolling preview streams captured lines beneath the list so the
// board looks alive.
//
// Live-stream limitation: the server feed the dashboard holds (/api/v1/events) carries
// STATUS frames - pool, health, running targets - not a raw-output journal. The log viewer
// tails that journal from a DIFFERENT per-run endpoint. So in live mode this tile shows the
// running targets and their current step as the activity preview and links out to the log
// viewer for the actual output; a real in-dashboard tail would need a journal SSE consumer
// (future work), which we deliberately do not invent here.

import type { DashboardState, RunningTargetView } from "../state";
import {
  ALL_WORKSPACES,
  inScope,
  onWorkspaceScope,
  shortName,
  workspaceScope,
} from "../../../lib/scope";
import { fmtArgs, relTime } from "../state";
import { glossaryLink } from "../../../lib/glossary";
import { logsLink } from "../../../lib/server";
import { renderLine } from "../../../render/sections";
import { Card, countBadge, h, scrollRegion, type Tile } from "./card";
import { menuToggle } from "../../../ui/menu-toggle";
import { menuButton, type MenuAction } from "./menu";

const PREVIEW_LINES = 120; // most recent captured lines kept in the streaming preview

// externalIcon is the "opens elsewhere" mark: a box with an arrow leaving it. Same construction as
// bigPictureIcon and shareGlyph (24-unit viewBox, 14px, currentColor stroke) so every button icon in
// the console is drawn the one way.
function externalIcon(): SVGElement {
  const NS = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("width", "14");
  svg.setAttribute("height", "14");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.7");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  const path = (d: string): void => {
    const p = document.createElementNS(NS, "path");
    p.setAttribute("d", d);
    svg.appendChild(p);
  };
  path("M13 4h7v7"); // arrowhead
  path("M20 4 11 13"); // shaft
  path("M18 14v4a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4"); // the box it leaves
  return svg;
}

// openButton builds the "Open in log viewer" secondary button. A real <button> with the same
// component classes and icon-then-text children as the Big Picture button, so the two are the same
// element rather than an anchor dressed as one.
function openButton(): HTMLButtonElement {
  const button = h(
    "button",
    "pf-v6-c-button pf-m-secondary console-dashboard-activity__open",
  ) as HTMLButtonElement;
  button.type = "button";
  const icon = h("span", "pf-v6-c-button__icon pf-m-start");
  icon.append(externalIcon());
  button.append(icon, h("span", "pf-v6-c-button__text", "Open in log viewer"));
  return button;
}

export function activityTile(): Tile {
  const card = new Card("activity", "Live activity", {
    why:
      "What magus is executing right now, with the tail of its captured output. A target that sits" +
      " here with its step unchanged is hung rather than slow, and the tail usually says why.",
  });

  // Header note: a running-count badge plus the way into the log viewer. With one target running
  // that is a button straight to it; with several it is a menu of them, because the viewer has no
  // single answer to "which one" (see renderOpenMenu).
  const noteWrap = h("span", "console-dashboard-activity__note");
  noteWrap.dataset.controlSize = "compact";
  const count = countBadge("running targets");
  // Where a plain click goes when there is nothing to choose between.
  let openHref = "../logs/";
  const openOne = openButton();
  openOne.addEventListener("click", () => window.open(openHref, "_blank", "noopener"));
  const picker = menuButton(
    menuToggle({
      variant: "secondary",
      icon: externalIcon(),
      text: "Open in log viewer",
      classes: "console-dashboard-activity__open",
    }),
    [],
  );
  picker.el.hidden = true;
  noteWrap.append(count.el, openOne, picker.el);
  card.noteNode().replaceWith(noteWrap);

  // A one-line guide: each running target is a trace an operator can open.
  const caption = h("p", "console-dashboard-activity__caption");
  caption.append(document.createTextNode("Each running target is a live "));
  caption.append(glossaryLink("Trace"));
  caption.append(document.createTextNode(". Open it to read the full output."));

  const list = h("ul", "console-dashboard-rowlist");
  // A <div>, not a <pre>: every line is rendered through the SHARED renderLine() the log viewer and
  // the activity trail use, so the preview carries the same ANSI colors, [pass]/[fail] status
  // badges, and line markup rather than being a flat monospace dump of the same bytes. The styles
  // come from render/render.css, which dashboard.css imports for exactly this.
  //
  // It scrolls, so it is a named, focusable log. aria-live is off: the tail is rebuilt on every
  // frame, and a live region around that would read the whole buffer out again each time.
  const preview = h("div", "console-dashboard-activity__log");
  preview.hidden = true;
  preview.dataset.keepEmpty = "";
  scrollRegion(preview, "Streaming output preview");
  preview.setAttribute("role", "log");
  preview.setAttribute("aria-live", "off");
  // Zebra striping (render.css). This tail has no line-number gutter to track along - a rolling
  // window has no stable numbering to anchor one to - so the stripes are the only thing giving the
  // eye a rail across a wrapped line.
  preview.dataset.zebra = "";
  card.body.append(caption, list, preview);

  // Auto-follow the tail UNTIL the operator scrolls up to read - then freeze in place and let them
  // take control (like the log viewer's livePaused). Scrolling back to the bottom re-arms the follow.
  // The threshold absorbs sub-pixel rounding so "resting at the bottom" reliably counts as pinned.
  let pinned = true;
  preview.addEventListener("scroll", () => {
    pinned = preview.scrollHeight - preview.scrollTop - preview.clientHeight < 8;
  });

  // The picker's rows are rewritten only when the set of running targets changes: it is rendered on
  // every frame, and rebuilding the items of an open menu takes the reader's focus with them.
  let pickerSignature = "";

  function renderOpenMenu(
    targets: RunningTargetView[],
    liveHost: string | null,
    base: string,
  ): void {
    // One target (or none): a plain button straight to it. Nothing to choose between.
    if (targets.length < 2) {
      openOne.hidden = false;
      picker.el.hidden = true;
      if (targets.length === 1 && liveHost && targets[0].invocation) {
        openHref = logsLink(liveHost, { inv: targets[0].invocation });
      }
      return;
    }
    openOne.hidden = true;
    picker.el.hidden = false;
    const signature = targets.map(rowKey).join("\n") + "|" + base;
    if (signature === pickerSignature) return;
    pickerSignature = signature;
    const actions: MenuAction[] = targets.map((c) => ({
      label: fmtArgs(c.args),
      run: () => {
        const href = liveHost && c.invocation ? logsLink(liveHost, { inv: c.invocation }) : base;
        window.open(href, "_blank", "noopener");
      },
    }));
    // The aggregate view stays REACHABLE, just not the default. It is the right answer sometimes -
    // when the question is about how concurrent runs interleave - and removing it outright would
    // trade one wrong default for another.
    actions.push({
      label: "All runs together",
      run: () => window.open(base, "_blank", "noopener"),
    });
    picker.setActions(actions);
  }

  // Rows are RECONCILED BY KEY rather than rebuilt, so a target that finishes can leave rather than
  // simply not being in the next frame.
  //
  // The list used to be replaceChildren() per status frame, which meant every row was a new element
  // roughly once a second. Nothing could animate, because nothing persisted: a finished target
  // vanished between frames and the rows below jumped up to fill the gap.
  //
  // WHEN A ROW IS DISMISSED: the instant the server stops reporting the target as running. That is
  // the only honest trigger available - the status frame carries what is running now, with no
  // "finished" event to hang a longer-lived "just completed" state on. It is deliberately not a
  // timed shelf: that would mean the panel titled Live activity was showing work that is not live,
  // and the run timeline beside it already keeps the history.
  const rows = new Map<string, HTMLElement>();
  const LEAVE_MS = 260;

  // A running call's identity: its invocation plus its argv. Not the array index - reordering would
  // then look like every row changing at once - and not the invocation alone, since one invocation
  // fans out into several concurrent targets.
  function rowKey(c: RunningTargetView): string {
    return c.invocation + "|" + c.args.join(" ");
  }

  function reconcileRows(
    targets: RunningTargetView[],
    liveHost: string | null,
    base: string,
  ): void {
    const seen = new Set<string>();
    for (const c of targets) {
      const key = rowKey(c);
      seen.add(key);
      let row = rows.get(key);
      if (!row) {
        // Every row is a link. Without a specific invocation to deep-link, the row still opens the
        // log viewer at `base`, which is a worse destination than the run itself but an infinitely
        // better one than nothing happening.
        const href = liveHost && c.invocation ? logsLink(liveHost, { inv: c.invocation }) : base;
        row = h("a", "console-dashboard-row");
        (row as HTMLAnchorElement).href = href;
        row.append(h("code", "console-dashboard-row__cmd", fmtArgs(c.args)));
        row.append(h("span", "console-dashboard-row__meta"));
        // data-entering for one frame, then cleared: the transition runs from the entering style to
        // the resting one. Set-then-clear rather than a keyframe, so a row whose transition never
        // runs is simply already in its resting state (see --console-motion in tokens.css).
        row.dataset.entering = "";
        rows.set(key, row);
        list.append(row);
        // Force a style flush between setting the entering state and clearing it. Without this the
        // browser is free to coalesce both into one recalculation, never compute the entering style,
        // and therefore never run the transition - the row would simply appear.
        void row.offsetHeight;
        requestAnimationFrame(() => row?.removeAttribute("data-entering"));
      }
      // The meta line is the only part that changes tick to tick (step, elapsed), so only it is
      // rewritten - the row element itself, and therefore its animation state, survives.
      const bits: string[] = [];
      if (c.step) bits.push(c.step);
      const t = relTime(c.startTime);
      if (t) bits.push(t);
      const meta = row.querySelector(".console-dashboard-row__meta");
      if (meta) meta.textContent = bits.join(", ");
    }

    for (const [key, row] of rows) {
      if (seen.has(key)) continue;
      rows.delete(key); // dropped from the map immediately so a re-start gets a fresh row
      if (row.hasAttribute("data-leaving")) continue;
      // Pin the height before collapsing it: max-height cannot transition from `none`, so the row
      // would otherwise snap shut instead of sliding the stack up.
      row.style.maxHeight = row.getBoundingClientRect().height + "px";
      requestAnimationFrame(() => {
        row.dataset.leaving = "";
        row.style.maxHeight = "0px";
      });
      window.setTimeout(() => row.remove(), LEAVE_MS + 60);
    }
  }

  function render(
    targets: RunningTargetView[],
    liveHost: string | null,
    logLines: string[],
    demo: boolean,
    idleText: string,
  ): void {
    count.set(targets.length);
    // Live: deep-link to the host's stream. Demo: stay inside the unified demo (../logs/#demo)
    // instead of dropping into the empty log viewer (demo has no live host). Otherwise plain.
    const base = liveHost ? logsLink(liveHost, {}) : demo ? "../logs/#demo" : "../logs/";
    openHref = base;
    // With more than one thing running, "open in log viewer" has no single answer, so it becomes a
    // CHOICE. Following it used to drop the reader into the viewer unfiltered, showing every
    // concurrent invocation interleaved, which is the one place a specific run is hardest to read.
    renderOpenMenu(targets, liveHost, base);

    card.setEmpty(targets.length === 0 ? idleText : null);
    reconcileRows(targets, liveHost, base);

    // Streaming preview: only when a raw-output buffer is present (the demo feed). Keep the last
    // PREVIEW_LINES and follow the newest line so it reads as a live tail - BUT only while pinned.
    // When the operator has scrolled up we leave the preview frozen (content and position both) so
    // they can read without being yanked back down; scrolling to the bottom re-arms the follow.
    if (logLines.length > 0) {
      preview.hidden = false;
      if (pinned) {
        // No line-number gutter (null): the numbers are the log viewer's deep-link anchors, and a
        // rolling tail of the last PREVIEW_LINES has no stable numbering to anchor to.
        preview.replaceChildren(
          ...logLines.slice(-PREVIEW_LINES).map((raw) => renderLine(raw, null)),
        );
        // Ease down to the tail rather than snapping to it: a new line arriving otherwise reads as
        // the whole buffer flinching upward. Guarded on prefers-reduced-motion, since this is
        // content that moves on its own - the exact case that setting exists for.
        const reduce =
          typeof matchMedia === "function" &&
          matchMedia("(prefers-reduced-motion: reduce)").matches;
        preview.scrollTo({ top: preview.scrollHeight, behavior: reduce ? "auto" : "smooth" });
      }
    } else {
      preview.hidden = true;
      preview.replaceChildren();
      pinned = true; // reset so the next stream starts following again
    }
  }

  // Repaint when the browser tab's workspace scope changes, not only when the server pushes: the
  // scope decides which of these running targets belong to you, and it can change with no new frame.
  let latest: DashboardState | null = null;
  const repaint = (): void => {
    if (!latest?.status) return;
    const all = latest.status.runningTargets;
    // Read the scope once. inScope defaults it per call, so leaving it off costs one sessionStorage
    // read per running target per frame on a stream that pushes about once a second.
    const scope = workspaceScope();
    const mine = all.filter((t) => inScope(t.workspace, scope));
    // "Where is my stuff" is the failure a scope invites, and this is the moment someone asks it: a
    // scoped tile that is empty while the server is busy looks identical to an idle server. Saying
    // how much is running ELSEWHERE turns a dead end into a signpost.
    const elsewhere = all.length - mine.length;
    const scoped = scope !== ALL_WORKSPACES;
    const idleText =
      mine.length === 0 && scoped && elsewhere > 0
        ? "Nothing running in " +
          shortName(scope) +
          ". " +
          (elsewhere === 1 ? "1 target is" : elsewhere + " targets are") +
          " running in other workspaces."
        : "The pool is idle. Nothing is running right now.";
    render(mine, latest.liveHost, latest.logLines, latest.conn.state === "demo", idleText);
  };
  const offScope = onWorkspaceScope(repaint);

  return {
    el: card.el,
    update(s: DashboardState) {
      latest = s;
      repaint();
    },
    destroy() {
      offScope();
      picker.dispose();
    },
  };
}
