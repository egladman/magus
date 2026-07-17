// model.test.ts - the export/import core is the only logic on the Settings surface, so it is
// tested here without a DOM: the envelope round-trips, unknown keys are ignored, missing/wrong-typed
// keys keep the current value, and a broken envelope is a hard error. The surface's cell wiring lives in
// the browser.

import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildSettingsEnvelope, computePendingChanges, diffLines, importSettings, SETTINGS_SCHEMA_VERSION,
  type DiffContext, type Settings,
} from "./model";

const base: Settings = {
  poll: 20000,
  host: "127.0.0.1:7391",
  theme: "dark",
  focusRing: false,
  keymap: { "console.tab.close": "mod+w" },
};

test("buildSettingsEnvelope: wraps the snapshot in the versioned envelope", () => {
  assert.deepEqual(buildSettingsEnvelope(base), {
    schemaVersion: SETTINGS_SCHEMA_VERSION,
    settings: {
      poll: 20000,
      host: "127.0.0.1:7391",
      theme: "dark",
      focusRing: false,
      keymap: { "console.tab.close": "mod+w" },
    },
  });
});

test("round-trip: import(build(p)) restores every value onto a different current", () => {
  const raw = JSON.stringify(buildSettingsEnvelope(base));
  const current: Settings = { poll: 5000, host: "", theme: "auto", focusRing: true, keymap: {} };
  const res = importSettings(raw, current);
  assert.ok(res.ok);
  assert.deepEqual(res.next, base);
  assert.deepEqual(res.applied.sort(), ["focusRing", "host", "keymap", "poll", "theme"]);
});

test("import: unknown keys are ignored, known keys still apply", () => {
  const raw = JSON.stringify({
    schemaVersion: 1,
    settings: { poll: 60000, somethingNew: { nested: true }, fontSize: 14 },
  });
  const res = importSettings(raw, base);
  assert.ok(res.ok);
  assert.equal(res.next.poll, 60000);
  assert.equal(res.next.host, base.host); // untouched
  assert.deepEqual(res.applied, ["poll"]);
});

test("import: missing keys keep the current value", () => {
  const raw = JSON.stringify({ schemaVersion: 1, settings: { theme: "light" } });
  const res = importSettings(raw, base);
  assert.ok(res.ok);
  assert.equal(res.next.theme, "light");
  assert.equal(res.next.poll, base.poll);
  assert.equal(res.next.host, base.host);
  assert.deepEqual(res.next.keymap, base.keymap);
  assert.deepEqual(res.applied, ["theme"]);
});

test("import: a wrong-typed key is skipped, not applied (keeps current)", () => {
  const raw = JSON.stringify({
    schemaVersion: 1,
    settings: { poll: "fast", theme: "chartreuse", host: 12, keymap: { a: 3 } },
  });
  const res = importSettings(raw, base);
  // Every supplied key is malformed, so nothing is recognizable -> hard error rather than a silent no-op.
  assert.equal(res.ok, false);
  if (!res.ok) assert.match(res.error, /No recognizable settings/);
});

test("import: a valid host trims surrounding whitespace", () => {
  const raw = JSON.stringify({ schemaVersion: 1, settings: { host: "  10.0.0.2:9000  " } });
  const res = importSettings(raw, base);
  assert.ok(res.ok);
  assert.equal(res.next.host, "10.0.0.2:9000");
});

test("import: an empty host string is valid (clears the override)", () => {
  const raw = JSON.stringify({ schemaVersion: 1, settings: { host: "" } });
  const res = importSettings(raw, base);
  assert.ok(res.ok);
  assert.equal(res.next.host, "");
  assert.deepEqual(res.applied, ["host"]);
});

test("import: forward-compat - a newer schemaVersion still applies its known keys", () => {
  const raw = JSON.stringify({
    schemaVersion: SETTINGS_SCHEMA_VERSION + 1,
    settings: { host: "localhost:1", futureThing: true },
  });
  const res = importSettings(raw, base);
  assert.ok(res.ok);
  assert.equal(res.next.host, "localhost:1");
  assert.deepEqual(res.applied, ["host"]);
});

test("import: invalid JSON is a hard error", () => {
  const res = importSettings("{not json", base);
  assert.equal(res.ok, false);
  if (!res.ok) assert.match(res.error, /not valid JSON/);
});

test("import: a non-object / missing settings object is a hard error", () => {
  for (const raw of ["42", "\"str\"", "null", "[]", "{}", JSON.stringify({ schemaVersion: 1 })]) {
    const res = importSettings(raw, base);
    assert.equal(res.ok, false, "expected error for " + raw);
  }
});

test("import: a disabled binding (empty-string chord) survives the keymap type check", () => {
  const raw = JSON.stringify({ schemaVersion: 1, settings: { keymap: { "console.pane.split": "" } } });
  const res = importSettings(raw, base);
  assert.ok(res.ok);
  assert.deepEqual(res.next.keymap, { "console.pane.split": "" });
});

// --- computePendingChanges (the transactional diff) ---
// A diff context with human formatters and one editable command, so the pure function's readable output
// is asserted without a browser. effectiveChord resolves a command's chord from a user-override keymap
// (falling back to a default), mirroring what the surface injects.
const diffCtx: DiffContext = {
  pollLabel: (ms) => ms / 1000 + "s",
  themeLabel: (t) => (t === "auto" ? "System" : t === "light" ? "Light" : "Dark"),
  hostLabel: (h) => (h === "" ? "loopback" : h),
  focusRingLabel: (on) => (on ? "On" : "Off"),
  commandLabel: (id) => (id === "console.tab.close" ? "Close pane or tab" : id),
  effectiveChord: (keymap, id) => {
    const chord = Object.prototype.hasOwnProperty.call(keymap, id) ? keymap[id] : "mod+w"; // "mod+w" = the default
    return chord === "" ? "None" : chord;
  },
  commandIds: ["console.tab.close"],
};

test("computePendingChanges: identical draft yields no pending changes", () => {
  assert.deepEqual(computePendingChanges(base, { ...base }, diffCtx), []);
});

test("computePendingChanges: scalar edits become readable before -> after entries", () => {
  const draft: Settings = { ...base, poll: 10000, theme: "auto" };
  const changes = computePendingChanges(base, draft, diffCtx);
  assert.deepEqual(changes, [
    { key: "poll", label: "Refresh rate", before: "20s", after: "10s" },
    { key: "theme", label: "Theme", before: "Dark", after: "System" },
  ]);
});

test("computePendingChanges: host uses the display label (empty renders as loopback)", () => {
  const draft: Settings = { ...base, host: "" };
  const changes = computePendingChanges(base, draft, diffCtx);
  assert.deepEqual(changes, [{ key: "host", label: "Daemon host", before: "127.0.0.1:7391", after: "loopback" }]);
});

test("computePendingChanges: a keybinding rebind is one entry per command, by effective chord", () => {
  const draft: Settings = { ...base, keymap: { "console.tab.close": "mod+shift+w" } };
  const changes = computePendingChanges(base, draft, diffCtx);
  assert.deepEqual(changes, [
    { key: "keymap:console.tab.close", label: "Keybinding Close pane or tab", before: "mod+w", after: "mod+shift+w" },
  ]);
});

test("computePendingChanges: dropping an override back to the default is a real change", () => {
  const committed: Settings = { ...base, keymap: { "console.tab.close": "mod+shift+w" } };
  const draft: Settings = { ...base, keymap: {} }; // no override -> effective falls to the default "mod+w"
  const changes = computePendingChanges(committed, draft, diffCtx);
  assert.deepEqual(changes, [
    { key: "keymap:console.tab.close", label: "Keybinding Close pane or tab", before: "mod+shift+w", after: "mod+w" },
  ]);
});

test("computePendingChanges: disabling a binding reads as None", () => {
  const draft: Settings = { ...base, keymap: { "console.tab.close": "" } };
  const changes = computePendingChanges(base, draft, diffCtx);
  assert.deepEqual(changes, [
    { key: "keymap:console.tab.close", label: "Keybinding Close pane or tab", before: "mod+w", after: "None" },
  ]);
});

// --- diffLines (the Raw view's line diff) ---
test("diffLines: identical text is all context (same)", () => {
  const t = "a\nb\nc";
  assert.deepEqual(diffLines(t, t), [
    { kind: "same", text: "a" },
    { kind: "same", text: "b" },
    { kind: "same", text: "c" },
  ]);
});

test("diffLines: a changed line becomes a del then an add, context preserved", () => {
  const before = "a\nb\nc";
  const after = "a\nB\nc";
  assert.deepEqual(diffLines(before, after), [
    { kind: "same", text: "a" },
    { kind: "del", text: "b" },
    { kind: "add", text: "B" },
    { kind: "same", text: "c" },
  ]);
});

test("diffLines: a pure insertion is add-only, a pure deletion is del-only", () => {
  assert.deepEqual(diffLines("a\nc", "a\nb\nc"), [
    { kind: "same", text: "a" },
    { kind: "add", text: "b" },
    { kind: "same", text: "c" },
  ]);
  assert.deepEqual(diffLines("a\nb\nc", "a\nc"), [
    { kind: "same", text: "a" },
    { kind: "del", text: "b" },
    { kind: "same", text: "c" },
  ]);
});

test("diffLines: reflects a real settings envelope value change", () => {
  const before = JSON.stringify(buildSettingsEnvelope(base), null, 2);
  const after = JSON.stringify(buildSettingsEnvelope({ ...base, theme: "light" }), null, 2);
  const diff = diffLines(before, after);
  // exactly the theme line flips: one del carrying "dark", one add carrying "light", nothing else changes.
  assert.deepEqual(diff.filter((l) => l.kind === "del").map((l) => l.text.trim()), ['"theme": "dark",']);
  assert.deepEqual(diff.filter((l) => l.kind === "add").map((l) => l.text.trim()), ['"theme": "light",']);
});
