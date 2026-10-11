// dom.ts - the shared DOM handles and the small button/status/clipboard helpers the log viewer's
// modules reuse. The handles are resolved by resolveDom(), called by the boot BEFORE anything uses
// them - DEFERRED, not resolved at import, so the viewer can boot standalone against logs.html OR be
// mounted into a console host whose scaffold is injected first. The exports are `let` bindings, so
// every importer sees the resolved value through the live ES-module binding with no change of its
// own. Global getElementById is kept (not scoped to a root) so shared status-bar elements that live
// OUTSIDE the scaffold (console-conn, console-count) still resolve when mounted.

import { inlineAlert } from "../../ui/alert";
import { reportFailure } from "../../lib/notifications";
import { copyText, flashLabel } from "../../render/clipboard";

export const el = (id: string): HTMLElement | null => document.getElementById(id);

// bodyEl is the render root, used unguarded throughout; boot gates on it (and scrollEl) before any
// render runs, so it is typed non-null and assigned by resolveDom. The rest stay nullable.
export let bodyEl: HTMLElement;
export let scrollEl: HTMLElement | null;
export let emptyEl: HTMLElement | null;
export let panelEl: HTMLElement | null;
// The notice strip's host. setStatus is the only writer: it swaps a whole PF Alert in and out, so
// there is no half-updated widget to keep in sync.
let statusEl: HTMLElement | null;

// resolveDom (re)reads the handles from the document. Called once at boot, after the scaffold is in
// place - always so for the standalone page; the console injects it before calling. Idempotent.
export function resolveDom(): void {
  bodyEl = document.getElementById("log-body") as HTMLElement;
  scrollEl = el("log-scroll");
  emptyEl = el("log-empty");
  panelEl = document.querySelector(".console-render-panel") as HTMLElement | null;
  statusEl = el("log-status");
}

// showLog marks the viewer as holding something to read: the cold empty state goes away and the
// toolbar, which has nothing to act on until now, appears.
export function showLog(): void {
  if (emptyEl) emptyEl.hidden = true;
  if (panelEl) panelEl.dataset.loaded = "";
}

// setBtnLabel sets a toolbar button's text label without disturbing its icon: the label
// lives in a .console-render-btn__label span next to the SVG, so we can't just set button.textContent.
export function setBtnLabel(btn: HTMLElement | null, text: string): void {
  if (!btn) return;
  const label = btn.querySelector(".console-render-btn__label");
  if (label) label.textContent = text;
  else btn.textContent = text;
}

// The reference id of the loaded output sits in the body header beside the run's name, which is
// where a reader looks to ask "which output is this". The header is built by the run browser after
// the first load can already have settled an identity, so the last value is kept and applied when
// the slot arrives.
let refSlot: HTMLElement | null = null;
let refValue = "";
let refLabeled = false;

export function bindRefSlot(slot: HTMLElement | null): void {
  refSlot = slot;
  paintRef();
}

// setRefIdentity records what the header shows. A real ref gets a "Reference ID" label before the
// value; a non-ref state (a live run, a pasted log) shows just the value. An empty value hides it.
export function setRefIdentity(value: string, labeled: boolean): void {
  refValue = value;
  refLabeled = labeled;
  paintRef();
}

function paintRef(): void {
  if (!refSlot) return;
  refSlot.hidden = !refValue;
  refSlot.replaceChildren();
  if (!refValue) return;
  if (refLabeled) {
    const label = document.createElement("span");
    label.textContent = "Reference ID";
    refSlot.append(label);
  }
  const value = document.createElement("span");
  value.className = "console-log-body__meta-value";
  value.textContent = refValue;
  value.title = refValue;
  refSlot.append(value);
}

// setStatus shows a loading or failure notice in the strip under the toolbar, as a PF Alert. A
// failure is also a toast: the strip is on screen only while the reader happens to be looking at
// this tab, and a failure that only a strip knows about is a failure nobody was told of.
export function setStatus(msg: string, isErr?: boolean): void {
  if (statusEl) {
    statusEl.hidden = !msg;
    statusEl.replaceChildren();
    if (msg) {
      statusEl.append(
        inlineAlert({ variant: isErr ? "danger" : "info", title: msg, live: !isErr }),
      );
    }
  }
  if (msg && isErr) reportFailure("Log Viewer", msg, "log:" + msg);
}

// flashBtnLabel swaps a toolbar button's label to a transient message (e.g. "Copied") and reverts
// it after ~1.5s, without disturbing the icon.
export const flashBtnLabel = flashLabel;

// copyToClipboard copies text and reports the outcome on btn; a failure is also a toast.
export function copyToClipboard(
  text: string,
  btn: HTMLElement | null,
  what = "the log",
  confirm?: string,
): void {
  void copyText(text, { source: "Log Viewer", what, button: btn, confirm });
}

// --- PF ToggleGroup switches (Log|Timeline, Pretty|Raw) -----------------------
// Each two-option PF ToggleGroup encodes a boolean: the FIRST button is false, the SECOND true.
// setToggleGroup paints the selection (pf-m-selected + aria-pressed), setToggleGroupDisabled toggles
// both buttons' disabled, and flipToggleGroup clicks the other (enabled) button so a keybinding
// drives the switch through the button's own click handler - the single source of truth.
function toggleGroupButtons(id: string): HTMLButtonElement[] {
  const g = document.getElementById(id);
  return g
    ? Array.from(g.querySelectorAll<HTMLButtonElement>(".pf-v6-c-toggle-group__button"))
    : [];
}

export function toggleGroupValue(id: string): boolean {
  const b = toggleGroupButtons(id);
  return b.length === 2 && b[1].classList.contains("pf-m-selected");
}

export function setToggleGroup(id: string, second: boolean): void {
  toggleGroupButtons(id).forEach((btn, i) => {
    const on = (i === 1) === second;
    btn.classList.toggle("pf-m-selected", on);
    btn.setAttribute("aria-pressed", on ? "true" : "false");
  });
}

export function setToggleGroupDisabled(id: string, disabled: boolean): void {
  for (const btn of toggleGroupButtons(id)) btn.disabled = disabled;
}

export function flipToggleGroup(id: string): void {
  const b = toggleGroupButtons(id);
  if (b.length !== 2) return;
  const other = b[toggleGroupValue(id) ? 0 : 1];
  if (!other.disabled) other.click();
}

export function isTyping(node: EventTarget | null): boolean {
  const t = (node && (node as HTMLElement).tagName) || "";
  return (
    t === "INPUT" || t === "TEXTAREA" || (node !== null && (node as HTMLElement).isContentEditable)
  );
}
