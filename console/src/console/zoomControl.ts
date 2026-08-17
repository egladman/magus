// zoomControl.ts - the console's ONE zoom stepper: minus, a percent readout that doubles as reset,
// plus. It docks in the shared status bar's right cluster, which is where a reader of any surface
// already looks for it.
//
// Extracted from the log viewer, which had the only copy. The Plan surface needed the same control
// and the alternative was a second one that looked and behaved almost the same - the failure mode
// the console has hit before (see console.css on the chrome that used to be pasted into logs.css).
// A surface supplies what zooming MEANS to it and gets the control; nothing about the stepper is
// per-surface.
//
// The styles live in console.css rather than beside any one surface, because every surface loads
// that sheet and only the log viewer loads logs.css.

export interface ZoomControl {
  // Repaint the readout. Call after changing zoom by any other route (a command, ctrl+wheel) so the
  // percent never disagrees with the view.
  sync(): void;
  // Remove the control from the status bar. A surface that unmounts must call this: the status bar
  // outlives it, so a control left behind would drive a dead surface.
  remove(): void;
}

export interface ZoomControlOpts {
  // Reading the CURRENT factor rather than being told it: the surface owns the number, and a copy
  // here is a copy that can drift.
  get: () => number;
  // The surface steps its own way, because the two that use this do not agree: the log viewer adds
  // a tenth (its zoom is a text scale, and readers want the same increment at every size) while the
  // Plan multiplies (a drawing at 4x needs a bigger absolute step than one at 1x). Sharing the
  // CONTROL should not mean imposing a stepping policy on both.
  zoomIn: () => void;
  zoomOut: () => void;
  reset: () => void;
  label?: string;
}

// mountZoomControl inserts the stepper and returns its handle, or null when there is no status bar
// to dock into (a surface mounted outside the console shell).
//
// LIFETIME. The status bar is per-TAB and only the active tab's is in the document, so this resolves
// to whichever bar is on screen - which is the right one exactly while the calling surface is the
// visible one. Callers therefore mount when they become visible and remove when they stop being, NOT
// once at construction: a surface built while another tab is active would otherwise dock its stepper
// in that tab's bar.
//
// It also evicts any stepper already in that bar before adding its own. A caller that mounts twice
// without removing is a bug, but it is an INVISIBLE one - two steppers stack silently and the second
// drives a dead surface - and the log viewer is a cached singleton whose activate() runs again on
// every reopen, which is precisely how that happens. Making the mount self-replacing means no caller
// can produce the duplicate, rather than every caller having to remember not to.
export function mountZoomControl(opts: ZoomControlOpts): ZoomControl | null {
  const right = document.querySelector("#console-statusbar .console-shell-statusbar__right");
  if (!right) return null;
  for (const stale of right.querySelectorAll(".console-zoom")) stale.remove();
  const ctl = document.createElement("div");
  ctl.className = "console-zoom console-shell-statusbar__item";
  ctl.setAttribute("role", "group");
  ctl.setAttribute("aria-label", opts.label ?? "Zoom");

  const seg = (key: string, text: string, aria: string): HTMLButtonElement => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = key === "reset" ? "console-zoom__readout" : "console-zoom__btn";
    b.dataset.zoom = key;
    b.textContent = text;
    b.setAttribute("aria-label", aria);
    b.title = aria;
    return b;
  };

  const out = seg("out", "-", "Zoom out");
  const readout = seg("reset", "100%", "Reset zoom");
  const inn = seg("in", "+", "Zoom in");
  ctl.append(out, readout, inn);

  const sync = (): void => {
    readout.textContent = `${Math.round(opts.get() * 100)}%`;
  };
  sync();

  // Delegated: buttons fire click on Enter and Space natively, so this covers pointer and keyboard
  // both without a second handler.
  ctl.addEventListener("click", (ev) => {
    const t = (ev.target as HTMLElement).closest("[data-zoom]") as HTMLElement | null;
    if (!t) return;
    const k = t.dataset.zoom;
    if (k === "in") opts.zoomIn();
    else if (k === "out") opts.zoomOut();
    else opts.reset();
    sync();
  });

  right.prepend(ctl);
  return {
    sync,
    remove: () => ctl.remove(),
  };
}
