// confirm.ts - a PF danger Modal for an action that cannot be undone. The native confirm() it
// replaces cannot be styled, names no consequence in the page's voice, and blocks the thread.

import { h } from "../../desktop/view";
import { statusGlyph, statusText } from "../../ui/status";

export interface ConfirmOptions {
  title: string;
  message: string;
  confirmLabel: string;
  cancelLabel?: string;
}

let seq = 0;

// confirmDanger shows the modal and resolves true on confirm, false on cancel, Escape or a click on
// the backdrop. Focus starts on Cancel, so a stray Enter does not destroy anything, stays inside the
// dialog while it is open, and returns to the control that opened it.
export function confirmDanger(opts: ConfirmOptions): Promise<boolean> {
  const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const id = "console-settings-confirm-" + ++seq;

  const backdrop = h("div", "pf-v6-c-backdrop");
  const bullseye = h("div", "pf-v6-l-bullseye");
  const box = h("div", "pf-v6-c-modal-box pf-m-sm pf-m-danger");
  box.setAttribute("role", "dialog");
  box.setAttribute("aria-modal", "true");
  box.setAttribute("aria-labelledby", id + "-title");
  box.setAttribute("aria-describedby", id + "-body");

  const header = h("header", "pf-v6-c-modal-box__header");
  const main = h("div", "pf-v6-c-modal-box__header-main");
  const title = h("h1", "pf-v6-c-modal-box__title pf-m-icon");
  title.id = id + "-title";
  const icon = h("span", "pf-v6-c-modal-box__title-icon");
  icon.append(statusGlyph("danger"));
  title.append(
    icon,
    statusText("danger", "Danger alert:"),
    h("span", "pf-v6-c-modal-box__title-text", opts.title),
  );
  main.append(title);
  header.append(main);

  const body = h("div", "pf-v6-c-modal-box__body", opts.message);
  body.id = id + "-body";

  const footer = h("footer", "pf-v6-c-modal-box__footer");
  const confirm = h("button", "pf-v6-c-button pf-m-danger", opts.confirmLabel);
  confirm.type = "button";
  const cancel = h("button", "pf-v6-c-button pf-m-link", opts.cancelLabel ?? "Cancel");
  cancel.type = "button";
  footer.append(confirm, cancel);

  box.append(header, body, footer);
  bullseye.append(box);
  backdrop.append(bullseye);

  return new Promise<boolean>((resolve) => {
    const finish = (answer: boolean): void => {
      document.removeEventListener("keydown", onKey, true);
      backdrop.remove();
      opener?.focus();
      resolve(answer);
    };
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        finish(false);
      } else if (e.key === "Tab") {
        const order = [confirm, cancel];
        const at = order.indexOf(document.activeElement as HTMLButtonElement);
        e.preventDefault();
        const next = e.shiftKey ? (at <= 0 ? order.length - 1 : at - 1) : (at + 1) % order.length;
        order[next]?.focus();
      }
    };
    confirm.addEventListener("click", () => finish(true));
    cancel.addEventListener("click", () => finish(false));
    backdrop.addEventListener("click", (e) => {
      if (e.target === backdrop || e.target === bullseye) finish(false);
    });
    document.addEventListener("keydown", onKey, true);
    document.body.append(backdrop);
    cancel.focus();
  });
}
