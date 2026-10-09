// modal.ts - the console's PF modal box, built once. The keyboard-shortcuts sheet and the keybinding
// editor were each hand-assembling the same backdrop and box, and each missed the same things: the
// dialog role sat on the backdrop rather than the box, the title was a span nothing named the dialog
// by, focus did not move in or come back, and Tab walked out into the page behind. The structure
// follows PatternFly's Modal: a backdrop, a bullseye, a modal box that carries role=dialog and
// aria-modal and is labelled by its h1 title, a close button in the box, and an svg close icon.

import { closeGlyph } from "./statusbar";
import { h } from "./view";

export interface ModalOptions {
  // The backdrop's id, which the stylesheets and the tests address the dialog by.
  id: string;
  title: string;
  // Extra classes for the box, beside PF's.
  boxClass?: string;
  // Called by the close button. Escape and backdrop dismissal stay with the caller, which knows what
  // else has to stop when the dialog goes.
  onClose: () => void;
}

export interface Modal {
  readonly overlay: HTMLElement;
  readonly box: HTMLElement;
  readonly closeBtn: HTMLButtonElement;
  readonly body: HTMLElement;
  readonly footer: HTMLElement;
}

let seq = 0;

export function buildModal(opts: ModalOptions): Modal {
  const overlay = h("div", "pf-v6-c-backdrop");
  overlay.id = opts.id;
  overlay.hidden = true;

  const bullseye = h("div", "pf-v6-l-bullseye");
  const box = h("div", "pf-v6-c-modal-box pf-m-md" + (opts.boxClass ? " " + opts.boxClass : ""));
  // Focusable so the dialog owns the keyboard the moment it opens, even before a control is chosen.
  box.tabIndex = -1;
  box.setAttribute("role", "dialog");
  box.setAttribute("aria-modal", "true");

  const close = h("div", "pf-v6-c-modal-box__close");
  const closeBtn = h("button", "pf-v6-c-button pf-m-plain");
  closeBtn.type = "button";
  closeBtn.setAttribute("aria-label", "Close");
  const icon = h("span", "pf-v6-c-button__icon");
  icon.append(closeGlyph(16));
  closeBtn.append(icon);
  closeBtn.addEventListener("click", opts.onClose);
  close.append(closeBtn);

  const head = h("header", "pf-v6-c-modal-box__header");
  const title = h("h1", "pf-v6-c-modal-box__title");
  title.id = "console-modal-title-" + ++seq;
  title.append(h("span", "pf-v6-c-modal-box__title-text", opts.title));
  head.append(title);
  box.setAttribute("aria-labelledby", title.id);

  const body = h("div", "pf-v6-c-modal-box__body");
  const footer = h("footer", "pf-v6-c-modal-box__footer");

  box.append(close, head, body, footer);
  bullseye.append(box);
  overlay.append(bullseye);
  return { overlay, box, closeBtn, body, footer };
}

const FOCUSABLE =
  'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

// keepFocus makes a dialog behave as one: opening moves focus into the box, Tab and Shift+Tab cycle
// inside it, and closing hands focus back to what had it, if that is still on the page. The Tab trap
// is a keydown on the box, so it only acts while focus is inside.
export interface FocusKeeper {
  opened(): void;
  closed(): void;
}

export function keepFocus(box: HTMLElement): FocusKeeper {
  let returnTo: HTMLElement | null = null;

  box.addEventListener("keydown", (ev) => {
    if (ev.key !== "Tab") return;
    const stops = [...box.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => !el.hidden);
    if (stops.length === 0) {
      ev.preventDefault();
      box.focus();
      return;
    }
    const first = stops[0];
    const last = stops[stops.length - 1];
    const at = document.activeElement;
    if (ev.shiftKey && (at === first || at === box)) {
      ev.preventDefault();
      last.focus();
    } else if (!ev.shiftKey && at === last) {
      ev.preventDefault();
      first.focus();
    }
  });

  return {
    opened() {
      const at = document.activeElement;
      returnTo = at instanceof HTMLElement && !box.contains(at) ? at : null;
      box.focus();
    },
    closed() {
      const back = returnTo;
      returnTo = null;
      // Focus returns only if the page still has somewhere for it to go, and only if the reader has
      // not already moved it on: a dialog closed by clicking elsewhere should not yank it back.
      const lost =
        !document.activeElement ||
        document.activeElement === document.body ||
        box.contains(document.activeElement);
      if (back?.isConnected && lost) back.focus();
    },
  };
}
