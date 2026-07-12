// settings.ts - client-side console preferences, persisted in localStorage. Pure read/write with
// no DOM: the gear panel (console-settings.ts) edits these, and the dashboard transport / boot read
// them. Distinct from the daemon's own resolved config (which the status API reports read-only) -
// these are BROWSER-side UI prefs the operator controls and that never leave the machine.

const LS_POLL = "magus-console-poll-ms";
const LS_HOST = "magus-console-host";

// The insight/refresh poll interval, in ms. The live pool/health/runs arrive over the status SSE
// (push, not polled); this governs the one pollable feed, the VCS insight lenses. Default 20s, the
// value the transport used before this was configurable. Clamped to a sane range on read.
export const DEFAULT_POLL_MS = 20000;
const MIN_POLL_MS = 2000;
const MAX_POLL_MS = 300000;

export function getPollMs(): number {
  try {
    const v = Number(localStorage.getItem(LS_POLL));
    if (Number.isFinite(v) && v >= MIN_POLL_MS && v <= MAX_POLL_MS) return v;
  } catch { /* ignore */ }
  return DEFAULT_POLL_MS;
}

export function setPollMs(ms: number): void {
  try { localStorage.setItem(LS_POLL, String(ms)); } catch { /* ignore */ }
}

// An explicit default daemon host (host:port) to connect to when the URL carries no #live / no
// remembered daemon - the "loopback URL override" (implicit 127.0.0.1 otherwise). "" means unset.
export function getDefaultHost(): string {
  try { return (localStorage.getItem(LS_HOST) || "").trim(); } catch { return ""; }
}

export function setDefaultHost(host: string): void {
  try {
    if (host.trim()) localStorage.setItem(LS_HOST, host.trim());
    else localStorage.removeItem(LS_HOST);
  } catch { /* ignore */ }
}
