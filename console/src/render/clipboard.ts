// clipboard.ts - the one copy-to-clipboard helper for the render apps. A copy that fails is a failure
// the reader must see, so it raises a toast as well as flashing the control: a label that reads
// "Copy failed" for a second is gone before anyone looks.

import { reportFailure } from "../lib/notifications";
import { showToast } from "../lib/refresh-toast";
import { errMessage } from "../lib/guards";

const LABEL = ".console-render-btn__label, .pf-v6-c-menu__item-text";
const FLASH_MS = 1500;

// flashLabel swaps a control's text for a moment and then restores it. A control with a label slot
// (toolbar and menu buttons keep an icon beside it) changes only that slot.
export function flashLabel(btn: HTMLElement | null, text: string): void {
  if (!btn) return;
  const slot = btn.querySelector(LABEL) ?? btn;
  const prev = slot.textContent;
  slot.textContent = text;
  setTimeout(() => {
    slot.textContent = prev;
  }, FLASH_MS);
}

export interface CopyOptions {
  // The app raising a failure toast, e.g. "Log Viewer".
  source: string;
  // What was copied, for the failure message: "the log", "this section".
  what: string;
  // The control to flash with the outcome.
  button?: HTMLElement | null;
  // A confirmation toast for a control the reader cannot watch, such as a menu item whose menu
  // has already closed.
  confirm?: string;
}

// copyText writes text to the clipboard and reports the outcome on the control and, on failure, in a
// toast. It resolves to whether the write happened.
export async function copyText(text: string, opts: CopyOptions): Promise<boolean> {
  try {
    if (!navigator.clipboard || !navigator.clipboard.writeText) {
      throw new Error("the browser does not offer clipboard access here");
    }
    await navigator.clipboard.writeText(text);
    flashLabel(opts.button ?? null, "Copied");
    if (opts.confirm) showToast(opts.source, opts.confirm, "ok", { ms: 2500 });
    return true;
  } catch (e) {
    flashLabel(opts.button ?? null, "Copy failed");
    reportFailure(
      opts.source,
      "Could not copy " +
        opts.what +
        ": " +
        errMessage(e) +
        ". Select the text and copy it by hand.",
      "copy:" + opts.what,
    );
    return false;
  }
}
