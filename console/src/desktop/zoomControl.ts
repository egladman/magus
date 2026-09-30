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

// SINGLE OWNER. Exactly one status bar is in the document at a time (the shell attaches the active
// tab's and detaches the rest), so the stepper's slot is a resource with one holder - and mounting is
// an ACQUIRE that preempts whoever held it. The previous holder is released rather than left in place,
// which is what makes every way of getting this wrong harmless:
//
//   - the log viewer is a cached singleton whose activate() runs again on every reopen, so it mounts
//     without having removed. Preemption collapses that to one stepper instead of a stack of them,
//     each older one silently driving a dead surface.
//   - a surface built while another tab is active resolves to that tab's bar. It takes the slot, and
//     hands it straight back on setVisible(false) - the holder at rest is always the visible surface.
//
// A registry rather than a DOM sweep, because "remove any .console-zoom you find" cannot tell a stale
// node from the live one and would clobber a legitimate holder. release() is idempotent and only ever
// clears the slot it still owns, so a late remove() from a surface that was already preempted cannot
// take down its successor.
let holder: { readonly el: HTMLElement; readonly release: () => void } | null = null;

export function mountZoomControl(opts: ZoomControlOpts): ZoomControl | null {
  const right = document.querySelector("#console-statusbar .console-shell-statusbar__right");
  if (!right) return null;
  holder?.release();
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
  const release = (): void => {
    ctl.remove();
    if (holder?.el === ctl) holder = null;
  };
  holder = { el: ctl, release };
  return {
    sync,
    // Only releases while this control is still the holder: a surface preempted by a later mount
    // must not remove its successor's stepper when it eventually tears down.
    remove: () => {
      if (holder?.el === ctl) release();
    },
  };
}
