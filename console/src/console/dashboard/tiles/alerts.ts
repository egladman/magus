// alerts.ts - how Big Picture tells you something just happened.
//
// The console raises notifications through lib/notifications' NOTIFY_EVENT, and the shell records
// them against the title-bar bell. Big Picture hides the title bar, so without this module every
// alert the dashboard raises - a target failing, the daemon going degraded - would fire into a
// surface nobody can see. The mode would be at its least useful exactly when it matters most.
//
// It is deliberately NOT the shell's toast relocated. A toast is designed for someone at a
// keyboard: small, brief, dismissible, stacking. Every one of those properties is wrong here.
// Nobody is close enough to read small type, nobody is going to dismiss anything, a four-second
// dwell is gone before a person across the room has looked up, and a failing pipeline raises a
// dozen at once - which as a stack of toasts is a wall of noise that buries the one line that
// mattered. So the design inverts each of those:
//
//   Docked, not floating. A rail welded to the bottom edge of the canvas, square, no shadow, no
//   corner radius. It reads as part of the board, the way a status line on a broadcast graphic
//   does, rather than as a web app's popup pasted over one.
//
//   Bottom, never top. The verdict is the one thing on this screen that must be readable at all
//   times; an alert that covers it defeats the display. Docking at the bottom edge makes occluding
//   it structurally impossible rather than a thing to be careful about.
//
//   Coalesced, not stacked. One CI run failing six targets is six notifications with six distinct
//   dedupe keys. Six lines is unreadable at distance and says nothing the first line did not, so
//   past two from the same source they collapse to a count.
//
//   Dwelling, not blinking. The dwell is scaled to reading-at-distance and grows with the queue,
//   because the cost of an alert leaving too early is that it is missed entirely.
//
//   Severity only. History-tier notifications ("ok") never reach the rail. A wall board that
//   announces routine success trains everyone to ignore it, and then it cannot announce a failure.
//
// It renders ONLY in Big Picture. On the board the bell is visible and is the right home; a second
// channel there would just be two places to look.

import { NOTIFY_EVENT, type NotifyInput, type NotifyKind } from "../../../lib/notifications";
import { viewMode } from "./bigPicture";
import { h } from "../../view";

// How long one alert holds the rail, and how much each additional queued alert extends it. Both are
// tuned for reading across a room rather than for glancing at a laptop: the shell's toast dwell
// would be gone before a person had turned their head.
const BASE_DWELL_MS = 12_000;
const PER_QUEUED_MS = 4_000;
const MAX_DWELL_MS = 30_000;

// How many distinct lines the rail shows before collapsing the rest into a count. Three is the most
// that stays scannable at distance; past that the eye reads a block of text, not a list.
const MAX_LINES = 3;

interface Alert {
  source: string;
  kind: NotifyKind;
  message: string;
  at: number;
  // The deep link the notification already carried and this rail used to throw away. The dashboard
  // sets one on every failing target (main.ts's wireNotifications points it at that target's
  // captured output), so the single most useful thing an alert could offer was being discarded at
  // the boundary - the rail announced that something broke and gave no way to go and look.
  //
  // href only, never the `run` callback variant: those cannot cross the NOTIFY_EVENT boundary (a
  // function does not survive a CustomEvent from another bundle), so a rail that honored them
  // would work for shell-raised alerts and silently do nothing for everyone else's.
  href: string;
  linkLabel: string;
}

// admits keeps the rail to the tiers that justify interrupting a room. `important` is the bell tier
// and defaults to `kind === "error"` upstream, so this matches what lights the bell - one rule, not
// a second opinion about what counts as urgent.
function admits(input: NotifyInput): boolean {
  const kind = input.kind ?? "ok";
  return input.important ?? kind === "error";
}

export interface AlertRail {
  el: HTMLElement;
  destroy(): void;
}

// mountAlertRail builds the rail and wires it to NOTIFY_EVENT. The caller appends `el` to the
// panels container and calls destroy() on teardown.
export function mountAlertRail(): AlertRail {
  const root = h("div", "console-dashboard-alertrail");
  root.setAttribute("role", "status"); // polite: it announces, it never steals focus
  root.setAttribute("aria-live", "polite");
  // Shown/hidden by a data attribute rather than the `hidden` property, so the rail can TRANSITION
  // in and out. `hidden` resolves to display:none (overrides.css enforces it with !important), and
  // display is not an animatable property - toggling it makes the rail snap in and out, which on a
  // board being watched from across a room reads as the whole screen flinching. The attribute keeps
  // it in the layout and lets opacity and transform carry the change.
  //
  // Deliberately NOT a keyframe animation: see --console-motion in tokens.css. A transition that
  // never gets to run leaves the element at its final style (visible); an animation that never runs
  // leaves it on its `from` frame (invisible), which is how this rail once ended up parked below the
  // fold.
  root.setAttribute("aria-hidden", "true");

  const lines = h("div", "console-dashboard-alertrail__lines");
  const more = h("div", "console-dashboard-alertrail__more");
  more.hidden = true;
  root.append(lines, more);

  let queue: Alert[] = [];
  let timer: number | null = null;
  // The rail's own dedupe. The notification store drops a repeated key before it ever reaches the
  // bell, and this listener sits alongside that store rather than downstream of it, so without a
  // matching key set a surface that re-detects the same failure on every status frame would
  // re-alert the room every second.
  const seen = new Set<string>();

  function dwell(): number {
    return Math.min(MAX_DWELL_MS, BASE_DWELL_MS + Math.max(0, queue.length - 1) * PER_QUEUED_MS);
  }

  // syncHeight publishes the rail's height so the canvas can give back exactly that much padding
  // (dashboard.css reads --bp-alert-h). Without it the rail - which is position: fixed - simply
  // covers the bottom row of panels, and it does so at the one moment their contents matter most.
  //
  // Called synchronously at the end of every render RATHER than left to the ResizeObserver alone.
  // The observer's callback is delivered asynchronously, so between the rail appearing and that
  // callback landing there is at least one frame in which the canvas has not yet reflowed and the
  // panels underneath are covered. getBoundingClientRect forces the layout that closes that gap;
  // this fires only when an alert arrives or expires, so the cost is irrelevant.
  //
  // isConnected guards a detached rail. mountTiles() rebuilds the whole panel host on re-mount
  // without destroying the previous tiles, so an older rail's observer stays live pointing at an
  // orphaned node; once detached it measures 0 and would otherwise race the live rail to publish a
  // height of zero, silently undoing the reservation.
  function syncHeight(): void {
    if (!root.isConnected) return;
    const height = root.hasAttribute("data-shown") ? root.getBoundingClientRect().height : 0;
    document.documentElement.style.setProperty("--bp-alert-h", height + "px");
  }

  function render(): void {
    if (queue.length === 0) {
      root.removeAttribute("data-shown");
      root.setAttribute("aria-hidden", "true");
      // The canvas gives its padding back FIRST, then the rail fades out over the same window, so
      // the panels grow into the space as the rail leaves rather than after it has gone.
      syncHeight();
      // Content is cleared only after the transition, or the rail would empty and collapse to a
      // hairline mid-fade - the jolt the transition exists to avoid.
      window.setTimeout(() => {
        if (!root.hasAttribute("data-shown")) {
          lines.replaceChildren();
          more.hidden = true;
        }
      }, 220);
      return;
    }
    root.setAttribute("data-shown", "");
    root.setAttribute("aria-hidden", "false");

    // Coalesce by source: a run that fails six targets should read as one line about that run, not
    // six near-identical ones. The newest message stands in for the group, because on a live board
    // the most recent event is the one being reacted to.
    const bySource = new Map<string, Alert[]>();
    for (const a of queue) {
      const list = bySource.get(a.source);
      if (list) list.push(a);
      else bySource.set(a.source, [a]);
    }

    const groups = [...bySource.entries()].slice(0, MAX_LINES);
    lines.replaceChildren(
      ...groups.map(([source, group]) => {
        const newest = group[group.length - 1];
        // A CARD, not a line of text. The rail began as one-line rows and read as a status strip -
        // something scrolling past rather than something addressed to you. At the size a wall
        // display needs, a bordered card with its own severity edge is what makes an alert look
        // like an object worth doing something about.
        const card = h("div", "console-dashboard-alertrail__card");
        card.dataset.kind = newest.kind;

        const head = h("div", "console-dashboard-alertrail__head");
        head.append(h("span", "console-dashboard-alertrail__source", source));
        // The count is the whole point of coalescing: it must say how many were folded away, or
        // the collapse is just quiet data loss with extra steps.
        if (group.length > 1) {
          head.append(h("span", "console-dashboard-alertrail__count", "x" + String(group.length)));
        }
        card.append(head, h("div", "console-dashboard-alertrail__msg", newest.message));

        // The action. Opened in a new tab so following it never takes the board itself down - on a
        // shared or cast screen, navigating away is not recoverable by whoever is watching.
        if (newest.href) {
          const a = h("a", "console-dashboard-alertrail__action", newest.linkLabel);
          (a as HTMLAnchorElement).href = newest.href;
          (a as HTMLAnchorElement).target = "_blank";
          (a as HTMLAnchorElement).rel = "noopener";
          card.append(a);
        }
        return card;
      }),
    );

    const overflow = bySource.size - groups.length;
    more.hidden = overflow <= 0;
    more.textContent = overflow > 0 ? "+" + String(overflow) + " more" : "";
    syncHeight();
  }

  // expire drops the whole queue at once rather than ageing entries out one by one. A rail that
  // shortens line by line drags the eye back to it repeatedly to watch nothing happen; clearing in
  // one step means the board is either saying something or it is not.
  function schedule(): void {
    if (timer !== null) clearTimeout(timer);
    timer = window.setTimeout(() => {
      timer = null;
      queue = [];
      render();
    }, dwell());
  }

  function onNotify(ev: Event): void {
    if (viewMode.get() !== "bigPicture") return;
    const input = (ev as CustomEvent<NotifyInput>).detail;
    if (!input || !admits(input)) return;
    const key = input.key || input.source + ":" + input.message;
    if (seen.has(key)) return;
    seen.add(key);
    const link = typeof input.link === "string" ? { label: "Open", href: input.link } : input.link;
    queue.push({
      source: input.source,
      kind: input.kind ?? "error",
      message: input.message,
      at: input.at ?? Date.now(),
      href: link?.href ?? "",
      linkLabel: link?.label || "Open",
    });
    render();
    schedule();
  }

  document.addEventListener(NOTIFY_EVENT, onNotify);

  // The observer catches the height changing WITHOUT a render: a window resize, the viewport
  // rotating, or a webfont landing all change --bp-u and so the rail's line height. render() owns
  // the common case synchronously (see syncHeight); this is the long tail.
  const railObs = new ResizeObserver(syncHeight);
  railObs.observe(root);

  // Leaving the mode clears the rail: the bell is visible again on the board and is where these
  // belong, and a stale alert left hanging over the board would be a second, contradictory channel.
  const unbind = viewMode.subscribe((mode) => {
    if (mode === "bigPicture") return;
    if (timer !== null) clearTimeout(timer);
    timer = null;
    queue = [];
    render();
  });

  return {
    el: root,
    destroy(): void {
      document.removeEventListener(NOTIFY_EVENT, onNotify);
      unbind();
      railObs.disconnect();
      document.documentElement.style.removeProperty("--bp-alert-h");
      if (timer !== null) clearTimeout(timer);
    },
  };
}
