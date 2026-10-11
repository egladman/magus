// panel.ts - the right-docked side panel the shell uses for Share and Activity (and that the
// notification panel's markup follows): docked between the top chrome and the status bar, a head with
// an h2 title and a close button, a scrolling body. Three copies of this lived side by side
// with diverging semantics - a span for a title, a text multiplication sign for the
// close, aria-hidden toggled beside `hidden`, a dialog role on one and a region on another. This is
// the one pattern: the structure and the behaviour a panel owes a keyboard and a screen reader.
//
// A panel is a peek, not a modal: it docks over the content, the page behind stays usable, and
// Escape closes it. Whether a click outside also closes it is the panel's call - a task panel that
// holds a half-made choice does, a readout the reader glances at while working somewhere else does not.

import { closeGlyph } from "./statusbar";

export interface PanelOptions {
  id: string;
  title: string;
  // "dialog" for a panel the reader works in (focus moves in); "region" for a readout that never takes
  // focus.
  role: "dialog" | "region";
}

export interface Panel {
  readonly el: HTMLElement;
  readonly body: HTMLElement;
  readonly closeBtn: HTMLButtonElement;
}

export function buildPanel(opts: PanelOptions): Panel {
  const el = document.createElement("section");
  el.className = "console-shell-panel";
  el.id = opts.id;
  el.hidden = true;
  el.setAttribute("role", opts.role);

  const head = document.createElement("div");
  head.className = "console-shell-panel__head";
  const title = document.createElement("h2");
  title.className = "console-shell-panel__title";
  title.id = opts.id + "-title";
  title.textContent = opts.title;
  el.setAttribute("aria-labelledby", title.id);

  const closeBtn = document.createElement("button");
  closeBtn.type = "button";
  closeBtn.className = "pf-v6-c-button pf-m-plain console-shell-panel__close";
  closeBtn.setAttribute("aria-label", "Close " + opts.title.toLowerCase());
  const icon = document.createElement("span");
  icon.className = "pf-v6-c-button__icon";
  icon.append(closeGlyph(16));
  closeBtn.append(icon);
  head.append(title, closeBtn);

  const body = document.createElement("div");
  body.className = "console-shell-panel__body";
  el.append(head, body);
  return { el, body, closeBtn };
}

export interface PanelBehavior {
  isOpen(): boolean;
  open(): void;
  close(): void;
  toggle(): void;
  // Removes every listener this added. The panel element itself stays the caller's.
  destroy(): void;
}

export interface BehaviorOptions {
  // Selector of the status-bar buttons that toggle this panel. A press on one is that button's own
  // toggle, so the outside-click close ignores it rather than closing and reopening.
  toggles: string;
  // Whether a pointerdown outside the panel closes it.
  closeOnOutside: boolean;
  // Where focus lands on open, or null to leave it where it is.
  focusOnOpen?: () => HTMLElement | null;
  // Runs after every open or close, for the owner's timers and for the toggles' aria-expanded.
  onChange?: (open: boolean) => void;
}

export function panelBehavior(panel: Panel, opts: BehaviorOptions): PanelBehavior {
  const listeners = new AbortController();
  const { signal } = listeners;
  let open = false;
  // The control that had focus when the panel opened, so closing hands it back.
  let opener: HTMLElement | null = null;

  const set = (v: boolean, restoreFocus: boolean): void => {
    if (v === open) return;
    open = v;
    if (v) {
      const at = document.activeElement;
      opener = at instanceof HTMLElement ? at : null;
      panel.el.hidden = false;
      const target = opts.focusOnOpen?.();
      if (target) requestAnimationFrame(() => target.focus());
    } else {
      // Read before hiding: hiding drops focus out of the panel.
      const active = document.activeElement;
      const inside = panel.el.contains(active);
      const lost = !active || active === document.body;
      const back = opener;
      opener = null;
      panel.el.hidden = true;
      // Hand focus back when it was inside, or when it has nowhere else to be after a keyboard or
      // button close. Focus the reader has already moved to the page stays where they put it.
      if ((inside || (restoreFocus && lost)) && back?.isConnected) back.focus();
    }
    opts.onChange?.(v);
  };

  panel.closeBtn.addEventListener("click", () => set(false, true), { signal });
  document.addEventListener(
    "keydown",
    (e: KeyboardEvent) => {
      if (e.key === "Escape" && open) set(false, true);
    },
    { signal },
  );
  if (opts.closeOnOutside) {
    document.addEventListener(
      "pointerdown",
      (e) => {
        if (!open) return;
        const t = e.target;
        if (!(t instanceof Node) || panel.el.contains(t)) return;
        if (t instanceof Element && t.closest(opts.toggles)) return;
        set(false, false);
      },
      { signal },
    );
  }

  return {
    isOpen: () => open,
    open: () => set(true, false),
    close: () => set(false, true),
    toggle: () => set(!open, true),
    destroy: () => listeners.abort(),
  };
}
