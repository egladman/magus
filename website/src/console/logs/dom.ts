// dom.ts - the shared DOM handles and the small button/status/clipboard helpers the log
// viewer's modules reuse. The element handles are resolved once at module load (the same as
// the original monolith's top-level lookups); bodyEl is the render root (asserted present -
// boot() gates on it), the rest stay nullable so every handler keeps its DOM guard and no-ops
// when its target is absent.

export const el = (id: string): HTMLElement | null => document.getElementById(id);

// bodyEl is the render root, used unguarded throughout; boot() only calls init() when it
// (and scrollEl) resolve, so it is asserted non-null here.
export const bodyEl = document.getElementById("log-body") as HTMLElement;
export const scrollEl = el("log-scroll");
export const refEl = el("log-ref");
export const refLabelEl = el("log-ref-label");
export const emptyEl = el("log-empty");
export const panelEl = document.querySelector(".panel") as HTMLElement | null;
// setStatus targets the status strip if the page has one. The current scaffold has none, so
// this is a safe no-op (the guard the original relied on); kept so a re-added #log-status lights up.
export const statusEl = el("log-status");

// setBtnLabel sets a toolbar button's text label without disturbing its icon: the label
// lives in a .btn-label span next to the SVG, so we can't just set button.textContent.
export function setBtnLabel(btn: HTMLElement | null, text: string): void {
  if (!btn) return;
  const label = btn.querySelector(".btn-label");
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

// flashBtnLabel swaps a toolbar button's label to a transient message (e.g. "Copied") and
// reverts it after ~1.5s, without disturbing the icon (setBtnLabel touches only .btn-label).
export function flashBtnLabel(btn: HTMLElement | null, text: string): void {
  if (!btn) return;
  const label = btn.querySelector(".btn-label");
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
