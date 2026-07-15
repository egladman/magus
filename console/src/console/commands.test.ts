// commands.test.ts - the command registry + keybinding engine. Everything but installKeybindings
// (one DOM listener) is pure, so it runs directly under node. Run: `pnpm run test`.

import { test } from "node:test";
import assert from "node:assert/strict";
import {
  chordFromEvent, conflicts, dispatchCommand, formatChord, listCommands, mergeKeymap, normalizeChord,
  registerCommand, resolveCommand, unregisterCommand, type KeyChord,
} from "./commands";

function evt(partial: Partial<KeyChord>): KeyChord {
  return { metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, key: "", ...partial };
}

test("normalizeChord canonicalizes modifier aliases, order, and key case", () => {
  assert.equal(normalizeChord("Cmd+Shift+K"), "mod+shift+k");
  assert.equal(normalizeChord("ctrl+k"), "mod+k");
  assert.equal(normalizeChord("shift+alt+mod+K"), "mod+alt+shift+k"); // reordered to mod,alt,shift
  assert.equal(normalizeChord("option+\\"), "alt+\\");
  assert.equal(normalizeChord("Escape"), "Escape"); // named key kept verbatim
  assert.equal(normalizeChord("  "), ""); // empty spec is the disabled sentinel
});

test("chordFromEvent folds the accelerator per platform", () => {
  assert.equal(chordFromEvent(evt({ metaKey: true, key: "K" }), true), "mod+k");   // mac: Cmd -> mod
  assert.equal(chordFromEvent(evt({ metaKey: true, key: "K" }), false), "k");       // non-mac: Cmd ignored
  assert.equal(chordFromEvent(evt({ ctrlKey: true, key: "k" }), false), "mod+k");   // non-mac: Ctrl -> mod
  assert.equal(chordFromEvent(evt({ ctrlKey: true, shiftKey: true, key: "\\" }), false), "mod+shift+\\");
  assert.equal(chordFromEvent(evt({ altKey: true, key: "ArrowLeft" }), false), "alt+ArrowLeft");
});

test("chordFromEvent recovers the physical letter for an alt-chord from e.code", () => {
  // macOS Alt+h composes a dead key in e.key; the code fallback still yields "alt+h".
  assert.equal(chordFromEvent(evt({ altKey: true, key: "˙", code: "KeyH" }), true), "alt+h");
  assert.equal(chordFromEvent(evt({ altKey: true, key: "¬", code: "KeyL" }), true), "alt+l");
  // A digit alt-chord recovers too; shift still layers.
  assert.equal(chordFromEvent(evt({ altKey: true, shiftKey: true, code: "Digit2", key: "@" }), false), "alt+shift+2");
  // Without alt, e.code is ignored - the typed key wins (so shifted symbols keep working).
  assert.equal(chordFromEvent(evt({ metaKey: true, key: "K", code: "KeyK" }), true), "mod+k");
});

test("formatChord renders a stored chord for display, per platform", () => {
  assert.equal(formatChord("mod+shift+k", true), "Cmd+Shift+K");
  assert.equal(formatChord("mod+shift+k", false), "Ctrl+Shift+K");
  assert.equal(formatChord("alt+h", true), "Option+H");
  assert.equal(formatChord("mod+alt+ArrowRight", true), "Cmd+Option+Right");
  assert.equal(formatChord("", true), ""); // deliberately unbound
});

test("a bare modifier press yields no chord", () => {
  assert.equal(chordFromEvent(evt({ shiftKey: true, key: "Shift" }), false), "");
  assert.equal(chordFromEvent(evt({ metaKey: true, key: "Meta" }), true), "");
});

test("mergeKeymap: user overrides win, including \"\" to disable; unmentioned keep default", () => {
  const defaults = { "logs.fold": "mod+k", "logs.raw": "mod+r", "logs.filter": "/" };
  const user = { "logs.raw": "Cmd+Shift+R", "logs.fold": "" };
  assert.deepEqual(mergeKeymap(defaults, user), {
    "logs.fold": "",                 // user disabled it
    "logs.raw": "mod+shift+r",        // user rebind, normalized
    "logs.filter": "/",              // untouched default
  });
});

test("resolveCommand reverse-looks-up a chord, skipping disabled entries", () => {
  const keymap = { "logs.raw": "mod+r", "logs.fold": "", "logs.filter": "/" };
  assert.equal(resolveCommand(keymap, "mod+r"), "logs.raw");
  assert.equal(resolveCommand(keymap, "/"), "logs.filter");
  assert.equal(resolveCommand(keymap, "mod+x"), null); // unbound
  assert.equal(resolveCommand(keymap, ""), null);       // the disabled sentinel never resolves
});

test("conflicts finds duplicate bindings, excluding self and disabled", () => {
  const keymap = { a: "mod+k", b: "mod+k", c: "", d: "mod+j" };
  assert.deepEqual(conflicts(keymap, "mod+k", "a").sort(), ["b"]);
  assert.deepEqual(conflicts(keymap, "Cmd+K"), ["a", "b"]); // normalized before comparing
  assert.deepEqual(conflicts(keymap, "mod+z"), []);
  assert.deepEqual(conflicts(keymap, ""), []);
});

test("the registry registers, dispatches, replaces by id, and unregisters", () => {
  const calls: string[] = [];
  registerCommand({ id: "test.a", label: "A", run: () => calls.push("a1") });
  assert.equal(dispatchCommand("test.a"), true);
  registerCommand({ id: "test.a", label: "A", run: () => calls.push("a2") }); // replace by id
  assert.equal(dispatchCommand("test.a"), true);
  assert.deepEqual(calls, ["a1", "a2"]); // no duplicate accumulation
  assert.equal(dispatchCommand("test.missing"), false);
  assert.ok(listCommands().some((c) => c.id === "test.a"));
  unregisterCommand("test.a");
  assert.equal(dispatchCommand("test.a"), false);
});

test("dispatchCommand forwards its argument", () => {
  let seen: unknown = null;
  registerCommand({ id: "test.arg", label: "Arg", run: (arg) => { seen = arg; } });
  dispatchCommand("test.arg", "left");
  assert.equal(seen, "left");
  unregisterCommand("test.arg");
});
