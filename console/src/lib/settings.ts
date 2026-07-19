// settings.ts - client-side console settings, persisted via the durable-cell primitive. Pure
// read/write with no DOM: the Settings surface edits these, and the dashboard
// transport / boot read them. Distinct from the daemon's own resolved config (which the status API
// reports read-only) - these are BROWSER-side UI prefs the operator controls and never leave the
// machine. The clamp/trim validation lives in the getters, not the storage layer.
import { persisted } from "./persist";

// The insight/refresh poll interval, in ms. The live pool/health/runs arrive over the status SSE
// (push, not polled); this governs the one pollable feed, the VCS insight lenses. Default 20s, the
// value the transport used before this was configurable. Clamped to a sane range on read.
export const DEFAULT_POLL_MS = 20000;
const MIN_POLL_MS = 2000;
const MAX_POLL_MS = 300000;

const pollMs = persisted<number>("console-poll-ms", DEFAULT_POLL_MS);
const host = persisted<string>("console-host", "");

export function getPollMs(): number {
  const v = pollMs.get();
  if (Number.isFinite(v) && v >= MIN_POLL_MS && v <= MAX_POLL_MS) return v;
  return DEFAULT_POLL_MS;
}

export function setPollMs(ms: number): void {
  pollMs.set(ms);
}

// Durably save the poll interval WITHOUT applying it to the running session (Settings "Save"): it
// takes effect on the next load. See persist.persistOnly.
export function savePollMs(ms: number): void {
  pollMs.persistOnly(ms);
}

// An explicit default daemon host (host:port) to connect to when the URL carries no explicit attach
// (#port) and no remembered daemon - the "loopback URL override". Stored canonical (a bare port typed
// in Settings is expanded to 127.0.0.1:port before it lands here). "" means unset.
export function getDefaultHost(): string {
  return host.get().trim();
}

export function setDefaultHost(value: string): void {
  host.set(value.trim());
}

// Durably save the default host WITHOUT applying it to the running session (Settings "Save").
export function saveDefaultHost(value: string): void {
  host.persistOnly(value.trim());
}

// Whether the split-pane focus outline is always shown, or only during keyboard navigation (the
// default). Off matches the browser's native :focus-visible behavior (no outline after a mouse click);
// On adds :root[data-focus-ring="always"] so the focused pane's outline shows regardless of input
// method. Distinct from theme (a repaint) - this only toggles an attribute, so it never needs a reload.
const focusRing = persisted<boolean>("console-focus-ring", false);

export function getFocusRing(): boolean {
  return focusRing.get();
}

export function setFocusRing(on: boolean): void {
  focusRing.set(on);
}

// Durably save the focus-ring preference WITHOUT applying it to the running session (Settings "Save"):
// it takes effect on the next load. See persist.persistOnly.
export function saveFocusRing(on: boolean): void {
  focusRing.persistOnly(on);
}

// applyFocusRing reflects the preference onto the document root so the CSS keyed on
// :root[data-focus-ring="always"] takes effect. Call once at boot with the persisted value
// (getFocusRing()), and again whenever Settings applies a change live (Save & Apply).
export function applyFocusRing(on: boolean): void {
  if (on) document.documentElement.setAttribute("data-focus-ring", "always");
  else document.documentElement.removeAttribute("data-focus-ring");
}
