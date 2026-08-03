// rotator.ts - the one slot on the Big Picture canvas that cycles.
//
// The canvas gives eight panels a permanent home. The board has roughly twenty, and the rest -
// utilization history, cache-rate history, per-target stats, the remote cache, hosted services -
// are worth seeing on a wall display and cannot all be on it at once. Rotation is how they get a
// turn without any of them getting a slot.
//
// == The discipline, which is most of the design ==
//
// A board where everything rotates cannot be read. If the answer to "is anything wrong" might be
// off-screen for twenty seconds, the display has failed at the only job it definitely has. So:
//
//   ONE slot rotates. The verdict, the counts, the run timeline, the live output and the bottom
//   rail are all permanent. What cycles is strictly the material you would look at second.
//
//   IT FREEZES WHEN SOMETHING IS WRONG. A board that shuffles panels while a target is failing is
//   actively working against the person trying to read it, and the moment it is most likely to be
//   looked at is the moment it should be most still.
//
//   IT FREEZES WHILE SOMEONE IS READING IT. Pointer over the slot, or keyboard focus inside it,
//   means a human is engaged - advancing underneath them is the same failure at a smaller scale.
//
//   IT CROSSFADES, and only that. No slide, no marquee: text that moves horizontally is hard to
//   read at distance and is a known accessibility problem. Under prefers-reduced-motion it cuts.
//
// The fade is a TRANSITION, never a keyframe animation - see --console-motion in tokens.css. An
// element whose transition never runs is at its final style; one whose animation never completes is
// stuck on its `from` frame, which for a fade means invisible. On a wall display, where the browser
// may never consider the tab visible, that distinction is the difference between a panel and a
// blank rectangle.

import { viewMode } from "./bigPicture";

// How long each panel holds the slot. Long enough to actually read a chart from across a room -
// anything under about ten seconds reads as a slideshow rather than as a display.
const DWELL_MS = 18_000;

export interface RotatorOptions {
  // Returns true when rotation should hold still. main.ts passes the attention hero's verdict
  // state, so "anything is failing" freezes the board.
  paused(): boolean;
}

export interface Rotator {
  destroy(): void;
}

// mountRotator takes the panels that share the rotating slot and cycles them.
//
// Every member is marked with [data-rotate]; the active one additionally carries
// [data-rotate-active], and dashboard.css shows exactly that one. The panels themselves are
// untouched otherwise - they are ordinary board tiles that happen to share a grid area, which is
// the same "one renderer per concept" rule the rest of the canvas follows.
export function mountRotator(panels: HTMLElement[], opts: RotatorOptions): Rotator {
  for (const p of panels) p.dataset.rotate = "";
  let index = 0;
  let hovering = false;
  let timer: number | null = null;

  function paint(): void {
    panels.forEach((p, i) => p.toggleAttribute("data-rotate-active", i === index));
  }

  // A panel that has nothing to show must not take a turn. Several tiles hide THEMSELVES when they
  // have no data (services, remote and config all set their own .hidden inside update()), and
  // rotating to one of those would park the slot on an empty rectangle for a full dwell - which
  // reads as the board being broken rather than as the panel being empty.
  function nextIndex(from: number): number {
    for (let step = 1; step <= panels.length; step++) {
      const candidate = (from + step) % panels.length;
      if (!panels[candidate].hidden) return candidate;
    }
    return from; // every member is empty: hold where we are rather than flicker
  }

  function tick(): void {
    if (viewMode.get() !== "bigPicture") return;
    if (hovering) return;
    if (opts.paused()) return;
    const next = nextIndex(index);
    if (next === index) return;
    index = next;
    paint();
  }

  // A plain interval rather than a self-rescheduling timeout: a skipped tick (paused, hovered, or
  // not in the mode) must not shift the cadence for every tick after it. The panel simply holds and
  // the next boundary comes at the same wall-clock rhythm.
  timer = window.setInterval(tick, DWELL_MS);

  const onEnter = (): void => {
    hovering = true;
  };
  const onLeave = (): void => {
    hovering = false;
  };
  for (const p of panels) {
    p.addEventListener("pointerenter", onEnter);
    p.addEventListener("pointerleave", onLeave);
    p.addEventListener("focusin", onEnter);
    p.addEventListener("focusout", onLeave);
  }

  // Entering the mode restarts from the first panel, so a display that has been sitting on the
  // board does not resume mid-cycle on whatever happened to be showing an hour ago.
  const unbind = viewMode.subscribe((mode) => {
    if (mode !== "bigPicture") return;
    index = panels.findIndex((p) => !p.hidden);
    if (index < 0) index = 0;
    paint();
  });

  paint();

  return {
    destroy(): void {
      if (timer !== null) window.clearInterval(timer);
      unbind();
      for (const p of panels) {
        p.removeEventListener("pointerenter", onEnter);
        p.removeEventListener("pointerleave", onLeave);
        p.removeEventListener("focusin", onEnter);
        p.removeEventListener("focusout", onLeave);
      }
    },
  };
}
