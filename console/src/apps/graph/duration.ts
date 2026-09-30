// duration.ts - pure timing helpers for the graph explorer's duration overlay
// (cards, the "Color by duration" preset, and critical-path totals). Node
// timing arrives from the loader in three spellings depending on the source
// (`magus graph deps -o json` vs hand-authored fixtures): a numeric DurationMs,
// a numeric duration_ms, or a stringly attrs.DurationMs. nodeDurationMs is the
// single place that reconciles them so main.ts and tests never re-probe the
// three spellings themselves. No module state, no DOM - safe to import from
// both main.ts and plain node tests.

import type { GNode } from "./types.js";

// nodeDurationMs reads a node's duration in milliseconds, covering all three
// spellings in the wild. Prefers the first present and positive candidate in
// order (DurationMs, duration_ms, attrs.DurationMs); a candidate that is
// missing, non-finite, or <= 0 is skipped in favor of the next one. Returns 0
// when no candidate qualifies - never NaN.
export function nodeDurationMs(n: GNode): number {
  const candidates: unknown[] = [n.DurationMs, n.duration_ms, n.attrs?.DurationMs];
  for (const c of candidates) {
    if (c == null) continue;
    const v = typeof c === "string" ? Number(c) : c;
    if (typeof v === "number" && Number.isFinite(v) && v > 0) return v;
  }
  return 0;
}

// formatDuration renders a millisecond duration as plain-ASCII, human-scale
// text: "<n>ms" under a second, "<n.n>s" under a minute (one decimal), and
// "<m>m <ss>s" (zero-padded seconds) at a minute or above. Deterministic and
// total: non-finite or non-positive input renders as "0ms".
export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "0ms";
  if (ms < 1000) return Math.round(ms) + "ms";
  const totalSec = ms / 1000;
  if (totalSec < 59.95) return totalSec.toFixed(1) + "s";
  const whole = Math.round(totalSec);
  const m = Math.floor(whole / 60);
  const s = whole % 60;
  return m + "m " + String(s).padStart(2, "0") + "s";
}
