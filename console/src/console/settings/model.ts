// model.ts - the pure export/import core for the Settings surface (no DOM, no storage). It turns
// a snapshot of the browser-side console settings into a versioned envelope (buildSettingsEnvelope) and
// merges an inbound envelope back onto the current values (importSettings), so both are unit-tested
// without a browser.
//
// Forward-compat contract: the envelope is
// { schemaVersion, settings: { poll, host, theme, focusRing, keymap } }.
// On import, unknown keys are ignored and missing/wrong-typed keys keep the current value; only a
// structurally broken envelope (not JSON, or no settings object) is a hard error.

import type { Keymap } from "../commands";
import type { Persisted } from "../../lib/persist";

// The three theme states theme.ts persists: "auto" (no stored key), or an explicit "light"/"dark".
// Mirrored here so the envelope can carry the theme without importing the pre-paint theme script.
export type ThemePref = "auto" | "light" | "dark";

// The envelope's schema version. Bump only on a breaking shape change; additive keys do not need one.
export const SETTINGS_SCHEMA_VERSION = 1;

// One full snapshot of the browser-side console settings the surface can export and import.
export interface Settings {
  poll: number; // insight/refresh poll interval, ms (settings.getPollMs)
  host: string; // explicit default daemon host, "host:port" or "" (settings.getDefaultHost)
  theme: ThemePref; // color theme override (theme.ts / localStorage "theme")
  focusRing: boolean; // always show the split-pane focus outline vs keyboard-only (settings.getFocusRing)
  keymap: Keymap; // the user's command chord overrides (the shared "keymap" cell)
}

export interface SettingsEnvelope {
  schemaVersion: number;
  settings: Partial<Settings>;
}

// buildSettingsEnvelope wraps a snapshot in the current versioned envelope. Pure - the surface
// passes the live values it read from the cells.
export function buildSettingsEnvelope(p: Settings): SettingsEnvelope {
  return {
    schemaVersion: SETTINGS_SCHEMA_VERSION,
    settings: { poll: p.poll, host: p.host, theme: p.theme, focusRing: p.focusRing, keymap: p.keymap },
  };
}

// The outcome of an import: the merged next snapshot plus which keys the file actually supplied (for the
// surface's messaging and its reload nudge), or a human error when the envelope is unusable. `unknown`
// and `skipped` let the surface warn about what it silently dropped: `unknown` = keys present in the
// settings object that magus does not know, `skipped` = known keys present but rejected by the type check
// (a wrong-typed value, or a malformed keymap). `newerSchema` carries the file's schemaVersion when it is
// ahead of this console's, so the surface can say the file came from a newer build.
export type ImportResult =
  | { ok: true; next: Settings; applied: (keyof Settings)[]; unknown: string[]; skipped: string[]; newerSchema?: number }
  | { ok: false; error: string };

// The canonical set of keys importSettings understands. Kept in sync with the Settings interface so a
// settings-object key outside this set is reported as unknown rather than silently dropped.
const KNOWN_KEYS: readonly (keyof Settings)[] = ["poll", "host", "theme", "focusRing", "keymap"];

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

// isKeymap accepts a plain object whose every value is a string (a chord, or "" for a disabled binding).
// A malformed keymap is skipped wholesale rather than partially applied, so an import never installs a
// half-parsed binding table.
function isKeymap(v: unknown): v is Keymap {
  return isRecord(v) && Object.values(v).every((x) => typeof x === "string");
}

// importSettings validates raw envelope text and merges its known, well-typed keys onto `current`.
// Unknown keys are ignored; missing or wrong-typed keys keep the current value; only invalid JSON or a
// missing settings object is a hard error. Pure - the surface applies `next` through the real cells.
export function importSettings(raw: string, current: Settings): ImportResult {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return { ok: false, error: "The text is not valid JSON." };
  }
  if (!isRecord(parsed) || !isRecord(parsed.settings)) {
    return { ok: false, error: "Not a magus console settings file (no settings object)." };
  }

  const settings = parsed.settings;
  const next: Settings = { ...current };
  const applied: (keyof Settings)[] = [];
  // A known key is `skipped` only when it is present but fails its type check; a key that is simply
  // absent keeps the current value and is not reported. `skip` records the former.
  const skipped: (keyof Settings)[] = [];
  const skip = (key: keyof Settings): void => {
    if (key in settings) skipped.push(key);
  };

  if (typeof settings.poll === "number" && Number.isFinite(settings.poll)) {
    next.poll = settings.poll;
    applied.push("poll");
  } else skip("poll");
  if (typeof settings.host === "string") {
    next.host = settings.host.trim();
    applied.push("host");
  } else skip("host");
  if (settings.theme === "auto" || settings.theme === "light" || settings.theme === "dark") {
    next.theme = settings.theme;
    applied.push("theme");
  } else skip("theme");
  if (typeof settings.focusRing === "boolean") {
    next.focusRing = settings.focusRing;
    applied.push("focusRing");
  } else skip("focusRing");
  if (isKeymap(settings.keymap)) {
    next.keymap = settings.keymap;
    applied.push("keymap");
  } else skip("keymap");

  if (applied.length === 0) {
    return { ok: false, error: "No recognizable settings to import." };
  }

  const unknown = Object.keys(settings).filter((k) => !(KNOWN_KEYS as readonly string[]).includes(k));
  // Imports stay permissive on version: a newer schemaVersion never hard-fails, it just tells the surface
  // the file came from a newer console so it can explain why some keys may not have applied.
  const version = parsed.schemaVersion;
  const newerSchema = typeof version === "number" && version > SETTINGS_SCHEMA_VERSION ? version : undefined;
  return { ok: true, next, applied, unknown, skipped, newerSchema };
}

// --- Pending diff (the transactional model) --------------------------------------------------------
// The Settings surface stages edits in a DRAFT and shows the diff against the committed baseline before
// it is saved or applied. One human-readable before -> after entry per changed field.
export interface PendingChange {
  key: string; // stable id: "poll" | "host" | "theme" | "focusRing" | "keymap:<commandId>"
  label: string; // "Refresh rate", "Theme", "Daemon host", "Focus ring", "Keybinding Close pane or tab"
  before: string; // display value of the committed side, e.g. "20s"
  after: string; // display value of the draft side, e.g. "10s"
}

// The display formatters computePendingChanges needs, injected so the function stays pure and browser-free
// (the surface wires the real formatters; a test passes stubs). effectiveChord resolves a command's
// display chord from a user-override keymap (merging defaults); commandIds is the editable command set to
// scan for keybinding changes.
export interface DiffContext {
  pollLabel: (ms: number) => string;
  themeLabel: (t: ThemePref) => string;
  hostLabel: (host: string) => string;
  focusRingLabel: (on: boolean) => string;
  commandLabel: (id: string) => string;
  effectiveChord: (keymap: Keymap, id: string) => string;
  commandIds: string[];
}

// computePendingChanges diffs a draft against the committed baseline into readable entries. Pure: no
// storage, no DOM. Keymap changes are compared by EFFECTIVE chord (a dropped override that returns to
// the default reads as a real change), one entry per affected command.
export function computePendingChanges(committed: Settings, draft: Settings, ctx: DiffContext): PendingChange[] {
  const changes: PendingChange[] = [];
  if (committed.poll !== draft.poll) {
    changes.push({ key: "poll", label: "Refresh rate", before: ctx.pollLabel(committed.poll), after: ctx.pollLabel(draft.poll) });
  }
  if (committed.host !== draft.host) {
    changes.push({ key: "host", label: "Daemon host", before: ctx.hostLabel(committed.host), after: ctx.hostLabel(draft.host) });
  }
  if (committed.theme !== draft.theme) {
    changes.push({ key: "theme", label: "Theme", before: ctx.themeLabel(committed.theme), after: ctx.themeLabel(draft.theme) });
  }
  if (committed.focusRing !== draft.focusRing) {
    changes.push({
      key: "focusRing", label: "Focus ring",
      before: ctx.focusRingLabel(committed.focusRing), after: ctx.focusRingLabel(draft.focusRing),
    });
  }
  for (const id of ctx.commandIds) {
    const before = ctx.effectiveChord(committed.keymap, id);
    const after = ctx.effectiveChord(draft.keymap, id);
    if (before !== after) {
      changes.push({ key: "keymap:" + id, label: "Keybinding " + ctx.commandLabel(id), before, after });
    }
  }
  return changes;
}

// --- Raw JSON diff (the "Raw" view of the pending changes) -----------------------------------------
// The pending block can show the raw settings envelope as a git-style line diff (removed red, added
// green) instead of the readable field list. diffLines is the pure LCS line diff behind it.
export type DiffLineKind = "same" | "del" | "add";
export interface DiffLine {
  kind: DiffLineKind;
  text: string;
}

// diffLines computes a minimal line-based diff (longest-common-subsequence) between two texts: shared
// lines are "same", lines only in `before` are "del", lines only in `after` are "add". Pure. The inputs
// are the pretty-printed settings envelope (tens of lines), so the O(n*m) table is inconsequential.
export function diffLines(before: string, after: string): DiffLine[] {
  const a = before.split("\n");
  const b = after.split("\n");
  const n = a.length;
  const m = b.length;
  // lcs[i][j] = length of the LCS of a[i:] and b[j:], filled from the bottom-right corner.
  const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
    }
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) { out.push({ kind: "same", text: a[i] }); i++; j++; }
    else if (lcs[i + 1][j] >= lcs[i][j + 1]) { out.push({ kind: "del", text: a[i] }); i++; }
    else { out.push({ kind: "add", text: b[j] }); j++; }
  }
  while (i < n) { out.push({ kind: "del", text: a[i] }); i++; }
  while (j < m) { out.push({ kind: "add", text: b[j] }); j++; }
  return out;
}

// createDraftCell adapts an in-memory value to the Persisted<T> interface so a component built to drive a
// durable cell (the keybindings editor) can instead stage into the draft: get/set/update mutate the
// in-memory value and notify local subscribers (so the editor re-renders live), and onChange fires so the
// surface recomputes the pending diff. It never touches storage - persistOnly is a no-notify in-memory
// write for interface completeness and is not used by the surface.
export function createDraftCell<T>(initial: T, onChange: () => void): Persisted<T> {
  let value = initial;
  const listeners = new Set<(v: T) => void>();
  const set = (v: T): void => {
    value = v;
    for (const fn of [...listeners]) fn(v);
    onChange();
  };
  return {
    get: () => value,
    set,
    update: (fn) => set(fn(value)),
    persistOnly: (v) => { value = v; },
    subscribe(fn) { listeners.add(fn); return () => listeners.delete(fn); },
  };
}
