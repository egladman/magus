// cast.ts - the one time a workspace's sigil is drawn for you.
//
// The first time this console sees a workspace, the sigil is TRACED rather than simply appearing: the
// ground dims, and the figure draws itself once, turning as it goes. It is a closed continuous curve
// by construction (sigil.ts joins each wedge to the next), which is exactly what makes a single
// stroke-dashoffset sweep trace the whole thing in one pass.
//
// ONCE. Ever, per workspace. A flourish that repeats is an interruption, and this one has to earn its
// single showing - so it is recorded only when it actually ran to completion, and any click or key
// ends it early.
//
// TRANSITIONS, NOT KEYFRAMES, per the rule in tokens.css: a browser does not advance animations in a
// tab it considers hidden, so an animation that never runs degrades to "it is not there at all" -
// which for a once-ever event would mean the user silently spends their only showing on a background
// tab. Transitions degrade the other way, to "it just appeared". Belt and braces on top of that:
// shouldCast defers while hidden and the record is written on completion rather than on start.

export type CastDecision = "cast" | "skip" | "defer";

// shouldCast decides without touching the DOM, so the rule is testable on its own.
//
// - already seen -> skip, and never again
// - reduced motion -> skip: someone who has asked for less movement has not asked for a ceremony, and
//   the sigil is still there afterwards. Recorded as seen, because they HAVE been shown it, just not
//   drawn.
// - hidden tab -> DEFER, never skip. This is the whole hazard: burning the one showing on a tab
//   nobody is looking at.
export function shouldCast(opts: {
  seen: boolean;
  hidden: boolean;
  reducedMotion: boolean;
}): CastDecision {
  if (opts.seen) return "skip";
  if (opts.reducedMotion) return "skip";
  if (opts.hidden) return "defer";
  return "cast";
}

// How long the trace takes. Long enough to read as deliberate, short enough that nobody waits for it -
// and it is interruptible throughout, so this is a ceiling rather than a cost.
export const CAST_MS = 1400;

// castSigil runs the flourish and resolves TRUE only if it ran to completion - the caller records the
// workspace as seen on that, never on having started.
//
// Not a modal and not focus-trapping: it takes no focus, answers any click or key by ending early, and
// the console underneath is never disabled. The house rule is that agents and flourishes REQUEST
// attention and never seize it.
export function castSigil(svg: string, ms = CAST_MS): Promise<boolean> {
  if (typeof document === "undefined") return Promise.resolve(false);
  return new Promise((resolve) => {
    const veil = document.createElement("div");
    veil.className = "console-cast";
    veil.setAttribute("aria-hidden", "true"); // decorative; the sigil is announced nowhere
    const holder = document.createElement("div");
    holder.className = "console-cast__mark";
    holder.innerHTML = svg;
    veil.append(holder);
    document.body.append(veil);

    const path = veil.querySelector<SVGPathElement>("path");
    let len = 0;
    try {
      len = path?.getTotalLength() ?? 0;
    } catch {
      // happy-dom and older engines have no geometry; the sigil still appears, just undrawn.
      len = 0;
    }
    if (path && len > 0) {
      path.style.strokeDasharray = String(len);
      path.style.strokeDashoffset = String(len);
    }

    let done = false;
    const finish = (completed: boolean): void => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      document.removeEventListener("keydown", onEnd, true);
      document.removeEventListener("pointerdown", onEnd, true);
      veil.remove();
      resolve(completed);
    };
    const onEnd = (): void => finish(false);
    document.addEventListener("keydown", onEnd, true);
    document.addEventListener("pointerdown", onEnd, true);
    const timer = setTimeout(() => finish(true), ms + 260);

    // One frame between the initial state and the target, so the transition has something to
    // interpolate FROM. Setting both in the same frame computes only the final value and the trace
    // never happens.
    requestAnimationFrame(() => {
      veil.dataset.on = "";
      if (path && len > 0) {
        path.style.transition = "stroke-dashoffset " + ms + "ms ease-in-out";
        path.style.strokeDashoffset = "0";
      }
    });
  });
}
