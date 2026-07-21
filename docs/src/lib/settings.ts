// settings.ts - client-side console preferences, persisted via the durable-cell primitive. Pure
// read/write with no DOM: the gear panel (console-settings.ts) edits these, and the dashboard
// transport / boot read them. Distinct from the daemon's own resolved config (which the status API
// reports read-only) - these are BROWSER-side UI prefs the operator controls and never leave the
// machine. The clamp/trim validation lives in the getters, not the storage layer.
import { persisted } from "./persist";

// The insight/refresh poll interval, in ms. The live pool/health/runs arrive over the status SSE
// (push, not polled); this governs the one pollable feed, the VCS insight lenses. Default 20s, the
// value the transport used before this was configurable. Clamped to a sane range on read.
const DEFAULT_POLL_MS = 20000;
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

// An explicit default daemon host (host:port) to connect to when the URL carries no #live / no
// remembered daemon - the "loopback URL override" (implicit 127.0.0.1 otherwise). "" means unset.
export function getDefaultHost(): string {
  return host.get().trim();
}

export function setDefaultHost(value: string): void {
  host.set(value.trim());
}
