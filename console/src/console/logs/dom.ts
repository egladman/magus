// dom.ts - the shared DOM handles and the small button/status/clipboard helpers the log viewer's
// modules reuse. The handles are resolved by resolveDom(), called by the boot BEFORE anything uses
// them - DEFERRED, not resolved at import, so the viewer can boot standalone against logs.html OR be
// mounted into a console host whose scaffold is injected first. The exports are `let` bindings, so
// every importer sees the resolved value through the live ES-module binding with no change of its
// own. Global getElementById is kept (not scoped to a root) so shared status-bar elements that live
// OUTSIDE the scaffold (console-conn, console-count) still resolve when mounted.

export const el = (id: string): HTMLElement | null => document.getElementById(id);

// bodyEl is the render root, used unguarded throughout; boot gates on it (and scrollEl) before any
// render runs, so it is typed non-null and assigned by resolveDom. The rest stay nullable.
export let bodyEl: HTMLElement;
export let scrollEl: HTMLElement | null;
export let refEl: HTMLElement | null;
export let refLabelEl: HTMLElement | null;
export let emptyEl: HTMLElement | null;
export let panelEl: HTMLElement | null;
// statusEl targets the status strip if the page has one. The current scaffold has none, so this is
// a safe no-op (the guard the original relied on); kept so a re-added #log-status lights up.
export let statusEl: HTMLElement | null;

// resolveDom (re)reads the handles from the document. Called once at boot, after the scaffold is in
// place - always so for the standalone page; the console injects it before calling. Idempotent.
export function resolveDom(): void {
  bodyEl = document.getElementById("log-body") as HTMLElement;
  scrollEl = el("log-scroll");
  refEl = el("log-ref");
  refLabelEl = el("log-ref-label");
  emptyEl = el("log-empty");
  panelEl = document.querySelector(".console-render-panel") as HTMLElement | null;
  statusEl = el("log-status");
}

// setBtnLabel sets a toolbar button's text label without disturbing its icon: the label
// lives in a .console-render-btn__label span next to the SVG, so we can't just set button.textContent.
export function setBtnLabel(btn: HTMLElement | null, text: string): void {
  if (!btn) return;
  const label = btn.querySelector(".console-render-btn__label");
  if (label) label.textContent = text;
  else btn.textContent = text;
}

// setRefIdentity fills the file-bar identity strip. A real ref gets a "Reference ID:" label
// (the codebase term, per docs/glossary.md) before the value; a non-ref state (a live run, a
// pasted log) shows just the value with no label.
export function setRefIdentity(value: string, labeled: boolean): void {
  if (refLabelEl) { refLabelEl.hidden = !labeled; refLabelEl.textContent = labeled ? "Reference ID:" : ""; }
  if (refEl) refEl.textContent = value;
}

export function setStatus(msg: string, isErr?: boolean): void {
  if (!statusEl) return;
  statusEl.textContent = msg || "";
  statusEl.classList.toggle("err", !!isErr);
  // The separate live event-count pill is a live-mode thing; keep it out of ref/error status.
  const countEl = el("log-count");
  if (countEl) { countEl.textContent = ""; countEl.hidden = true; }
}

// flashBtnLabel swaps a toolbar button's label to a transient message (e.g. "Copied") and reverts
// it after ~1.5s, without disturbing the icon (setBtnLabel touches only .console-render-btn__label).
export function flashBtnLabel(btn: HTMLElement | null, text: string): void {
  if (!btn) return;
  const label = btn.querySelector(".console-render-btn__label");
  const prev = label ? label.textContent : btn.textContent;
  setBtnLabel(btn, text);
  setTimeout(() => setBtnLabel(btn, prev ?? ""), 1500);
}

export function copyToClipboard(text: string, btn: HTMLElement | null): void {
  const done = (ok: boolean): void => {
    if (!btn) return;
    const prev = btn.textContent;
    btn.textContent = ok ? "copied" : "failed";
    setTimeout(() => { btn.textContent = prev; }, 1200);
  };
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(() => done(true), () => done(false));
  } else {
    done(false);
  }
}

export function isTyping(node: EventTarget | null): boolean {
  const t = (node && (node as HTMLElement).tagName) || "";
  return t === "INPUT" || t === "TEXTAREA" || (node !== null && (node as HTMLElement).isContentEditable);
}
