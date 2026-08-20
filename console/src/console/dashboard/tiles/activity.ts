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
import {
  ALL_WORKSPACES,
  inScope,
  onWorkspaceScope,
  shortName,
  workspaceScope,
} from "../../../lib/scope";
import { fmtArgs, relTime } from "../state";
import { glossaryLink } from "../../../lib/glossary";
import { logsLink } from "../../../lib/daemon";
import { renderLine } from "../../render/sections";
import { Card, h, type Tile } from "./card";

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

export function activityTile(): Tile {
  const card = new Card("activity", "Live activity", {
    why:
      "What magus is executing right now, with the tail of its captured output. A target that sits" +
      " here with its step unchanged is hung rather than slow, and the tail usually says why.",
  });

  // Header note: a running-count chip plus an "Open in log viewer" deep-link (repointed at
  // the live host on each render). Replaces the plain note span.
  const noteWrap = h("span", "console-dashboard-activity__note");
  const countLabel = h("span", "pf-v6-c-label pf-m-compact");
  const count = h("span", "pf-v6-c-label__content", "0");
  countLabel.append(count);
  // A real <button>, structurally identical to the Big Picture button in bigPicture.ts: the same
  // component classes, the same __icon pf-m-start + __text children, in the same order.
  //
  // A <button> rather than the <a> this was through two revisions, so it is the same ELEMENT as the
  // control it has to match, not an anchor dressed as one. The glyph's vertical centring is not a
  // PatternFly behaviour at all - it comes from the shared icon-button rule in dashboard.css, which
  // this button is listed in alongside the Big Picture button.
  //
  // The cost is that a middle-click no longer opens a new tab, so the click handler below opens the
  // destination in one explicitly - the same window.open the multi-target menu items already use.
  const open = h(
    "button",
    "pf-v6-c-button pf-m-secondary console-dashboard-activity__open",
  ) as HTMLButtonElement;
  open.type = "button";
  const openIcon = h("span", "pf-v6-c-button__icon pf-m-start");
  openIcon.append(externalIcon());
  open.append(openIcon, h("span", "pf-v6-c-button__text", "Open in log viewer"));
  // Where a plain click goes when there is nothing to choose between. Held here rather than on an
  // href because this is no longer an anchor.
  let openHref = "../logs/";
  noteWrap.append(countLabel, open);
  card.noteNode().replaceWith(noteWrap);

  // A one-line guide: each running target is a trace an operator can open.
  const caption = h("p", "console-dashboard-activity__caption");
  caption.append(document.createTextNode("Each running target is a live "));
  caption.append(glossaryLink("Trace"));
  caption.append(document.createTextNode(". Open it to read the full output."));

  const list = h("ul", "console-dashboard-rowlist");
  const empty = h("p", "console-dashboard-row__empty", "Pool is idle. Nothing running right now.");
  // A <div>, not a <pre>: every line is rendered through the SHARED renderLine() the log viewer and
  // the activity trail use, so the preview carries the same ANSI colors, [pass]/[fail] status
  // badges, and line markup rather than being a flat monospace dump of the same bytes. It reads as
  // the same product as the log viewer because it literally is the same renderer - the styles come
  // from render/render.css, which dashboard.css imports for exactly this. renderLine handles its
  // own pre-wrap, so the <pre> that used to be here is no longer doing anything.
  const preview = h("div", "console-dashboard-activity__log");
  preview.hidden = true;
  preview.setAttribute("aria-label", "Streaming output preview");
  // Zebra striping (render.css). This tail has no line-number gutter to track along - a rolling
  // window has no stable numbering to anchor one to - so the stripes are the only thing giving the
  // eye a rail across a wrapped line. It matters most in Big Picture, where the preview is read at
  // a distance and the content is moving underneath.
  preview.dataset.zebra = "";
  card.body.append(caption, list, empty, preview);

  // Auto-follow the tail UNTIL the operator scrolls up to read - then freeze in place and let them
  // take control (like the log viewer's livePaused). Scrolling back to the bottom re-arms the follow.
  // The threshold absorbs sub-pixel rounding so "resting at the bottom" reliably counts as pinned.
  let pinned = true;
  preview.addEventListener("scroll", () => {
    pinned = preview.scrollHeight - preview.scrollTop - preview.clientHeight < 8;
  });

  // The picker behind "Open in log viewer" when several targets are running. Anchored to the link
  // and built with the shared PF menu vocabulary, matching the failing-target chips in the hero so
  // "a choice of where to go" looks the same wherever the board offers one.
  const openMenu = h("div", "pf-v6-c-menu console-dashboard-activity__openmenu");
  openMenu.hidden = true;
  openMenu.setAttribute("role", "menu");
  const openMenuList = h("ul", "pf-v6-c-menu__list");
  const openMenuContent = h("div", "pf-v6-c-menu__content");
  openMenuContent.append(openMenuList);
  openMenu.append(openMenuContent);
  noteWrap.append(openMenu);

  const closeOpenMenu = (): void => {
    openMenu.hidden = true;
    open.setAttribute("aria-expanded", "false");
  };
  if (typeof document !== "undefined") {
    document.addEventListener("click", closeOpenMenu);
    document.addEventListener("keydown", (ev) => {
      if (ev.key === "Escape") closeOpenMenu();
    });
  }
  openMenu.addEventListener("click", (ev) => ev.stopPropagation());

  function renderOpenMenu(
    targets: RunningTargetView[],
    liveHost: string | null,
    base: string,
  ): void {
    // One target (or none): a plain link straight to it. Nothing to choose between.
    if (targets.length < 2) {
      open.removeAttribute("aria-haspopup");
      open.removeAttribute("aria-expanded");
      openMenu.hidden = true;
      if (targets.length === 1 && liveHost && targets[0].invocation) {
        openHref = logsLink(liveHost, { inv: targets[0].invocation });
      }
      return;
    }
    open.setAttribute("aria-haspopup", "menu");
    open.setAttribute("aria-expanded", "false");
    openMenuList.replaceChildren(
      ...targets.map((c) => {
        const li = h("li", "pf-v6-c-menu__list-item");
        const b = document.createElement("button");
        b.type = "button";
        b.className = "pf-v6-c-menu__item";
        b.setAttribute("role", "menuitem");
        b.append(h("span", "pf-v6-c-menu__item-main", fmtArgs(c.args)));
        const href = liveHost && c.invocation ? logsLink(liveHost, { inv: c.invocation }) : base;
        b.addEventListener("click", () => {
          closeOpenMenu();
          window.open(href, "_blank", "noopener");
        });
        li.append(b);
        return li;
      }),
      // The aggregate view stays REACHABLE, just not the default. It is the right answer sometimes -
      // when the question is about how concurrent runs interleave - and removing it outright would
      // trade one wrong default for another.
      (() => {
        const li = h("li", "pf-v6-c-menu__list-item");
        const b = document.createElement("button");
        b.type = "button";
        b.className = "pf-v6-c-menu__item";
        b.setAttribute("role", "menuitem");
        b.append(h("span", "pf-v6-c-menu__item-main", "All runs together"));
        b.addEventListener("click", () => {
          closeOpenMenu();
          window.open(base, "_blank", "noopener");
        });
        li.append(b);
        return li;
      })(),
    );
  }

  open.addEventListener("click", (ev) => {
    if (!open.hasAttribute("aria-haspopup")) {
      // Single target: go straight there, in a new tab so the live board stays put.
      window.open(openHref, "_blank", "noopener");
      return;
    }
    ev.preventDefault();
    ev.stopPropagation();
    const willOpen = openMenu.hidden;
    openMenu.hidden = !willOpen;
    open.setAttribute("aria-expanded", String(willOpen));
  });

  // Rows are RECONCILED BY KEY rather than rebuilt, so a target that finishes can leave rather than
  // simply not being in the next frame.
  //
  // The list used to be replaceChildren() per status frame, which meant every row was a new element
  // roughly once a second. Nothing could animate, because nothing persisted: a finished target
  // vanished between frames and the rows below jumped up to fill the gap. On a board being watched
  // rather than used, that jump is the most visually noticeable thing on the screen, and it carries
  // no information - it is the one moment where something ENDING deserves to be legible.
  //
  // WHEN A ROW IS DISMISSED: the instant the daemon stops reporting the target as running. That is
  // the only honest trigger available - the status frame carries what is running now, with no
  // "finished" event to hang a longer-lived "just completed" state on. So the row leaves as the work
  // leaves, and the exit animation is what gives the eye time to register it. It is deliberately not
  // a timed "keep finished rows for 10s" shelf: that would mean the panel titled Live activity was
  // showing work that is not live, and the run timeline beside it already keeps the history.
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
        // Every row is a link. It used to be one only when a live host AND an invocation id were
        // both present, which is false in the demo and on a freshly opened board - so the rows a
        // reader is most likely to try first were exactly the inert ones. Without a specific
        // invocation to deep-link, the row still opens the log viewer at `base`, which is a worse
        // destination than the run itself but an infinitely better one than nothing happening.
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
        // and therefore never run the transition - the row would simply appear. requestAnimationFrame
        // alone is not enough: it fires BEFORE the next paint, so the entering style can go
        // unpainted. Reading offsetHeight forces the layout that makes it real.
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
      if (meta) meta.textContent = bits.join(" - ");
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
  ): void {
    count.textContent = String(targets.length);
    // Live: deep-link to the host's stream. Demo: stay inside the unified demo (../logs/#demo)
    // instead of dropping into the empty log viewer (demo has no live host). Otherwise plain.
    const base = liveHost ? logsLink(liveHost, {}) : demo ? "../logs/#demo" : "../logs/";
    openHref = base;
    // With more than one thing running, "open in log viewer" has no single answer, so it stops
    // being a link and becomes a CHOICE.
    //
    // Following it used to drop the reader into the viewer unfiltered, showing every concurrent
    // invocation interleaved. The viewer can aggregate, but that is not what someone clicking from
    // a specific panel wants - they have one run in mind, and the unfiltered view is the one place
    // that run is hardest to read. One running target keeps the plain link: there is nothing to
    // disambiguate, and a menu with a single item is a worse link.
    renderOpenMenu(targets, liveHost, base);

    empty.hidden = targets.length > 0;
    reconcileRows(targets, liveHost, base);

    // Streaming preview: only when a raw-output buffer is present (the demo feed). Keep the last
    // PREVIEW_LINES and follow the newest line so it reads as a live tail - BUT only while pinned.
    // When the operator has scrolled up we leave the preview frozen (content and position both) so
    // they can read without being yanked back down; scrolling to the bottom re-arms the follow.
    if (logLines.length > 0) {
      preview.hidden = false;
      if (pinned) {
        // No line-number gutter (null): the numbers are the log viewer's deep-link anchors, and a
        // rolling tail of the last PREVIEW_LINES has no stable numbering to anchor to - a gutter
        // here would show numbers that mean nothing and shift under the reader every tick.
        preview.replaceChildren(
          ...logLines.slice(-PREVIEW_LINES).map((raw) => renderLine(raw, null)),
        );
        // Ease down to the tail rather than snapping to it. `scrollTop = scrollHeight` teleports,
        // so a new line arriving reads as the whole buffer flinching upward - which is unpleasant
        // to read at a desk and genuinely hard to follow on a wall display, where the eye is
        // already working at the edge of legibility. The distance travelled is a line or two, so
        // smooth here is a nudge, not a ride.
        //
        // Guarded on prefers-reduced-motion: this is content that moves on its own, which is the
        // exact case that setting exists for, and the instant jump is the honest fallback.
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

  // Repaint when the browser tab's workspace scope changes, not only when the daemon pushes: the
  // scope decides which of these running targets belong to you, and it can change with no new frame.
  let latest: DashboardState | null = null;
  const repaint = (): void => {
    if (!latest?.status) return;
    const all = latest.status.runningTargets;
    const mine = all.filter((t) => inScope(t.workspace));
    // "Where is my stuff" is the failure a scope invites, and this is the moment someone asks it: a
    // scoped tile that is empty while the daemon is busy looks identical to an idle daemon. Saying
    // how much is running ELSEWHERE turns a dead end into a signpost - it is the sentence AWS's
    // region picker never says, and the reason people think they have lost data rather than moved.
    const elsewhere = all.length - mine.length;
    const scoped = workspaceScope() !== ALL_WORKSPACES;
    if (mine.length === 0 && scoped && elsewhere > 0) {
      empty.textContent =
        "Nothing running in " +
        shortName(workspaceScope()) +
        ". " +
        (elsewhere === 1 ? "1 target is" : elsewhere + " targets are") +
        " running in other workspaces.";
    } else {
      empty.textContent = "Pool is idle. Nothing running right now.";
    }
    render(mine, latest.liveHost, latest.logLines, latest.conn.state === "demo");
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
    },
  };
}
