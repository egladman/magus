// commands.ts - the console's single command surface and its keybinding engine. Every console
// action (toggle raw view, fold all, focus the filter, split a pane, close a tab) is registered
// here as a named command; menus, the tab strip, a future palette, and keybindings all dispatch
// through this one map, so an action is defined once and bound once. The design is borrowed from
// Tack's registerCommand/dispatchCommand + flat keymap record, re-expressed in the console's
// idioms. Everything except installKeybindings' single listener is a pure function (the registry
// map, chord normalization, keymap merge, and command resolution), unit-tested without a browser.

// A command: a stable id, a human label for menus/palette, an optional group for ordering, and
// the handler. arg carries an optional payload (e.g. a direction for a "focus pane" command).
export interface Command {
  id: string;
  label: string;
  group?: string;
  run: (arg?: unknown) => void;
}

// A chord is a canonical modifier+key string: modifiers in the fixed order mod, alt, shift, then
// one key, "+"-joined, e.g. "mod+shift+k". "mod" is the platform accelerator (Cmd on macOS,
// Ctrl elsewhere), so a binding is written once and works on both. The empty string is a valid
// keymap value meaning "deliberately unbound" (see Keymap).
export type Chord = string;

// A keymap is a flat record of commandId -> chord. An empty-string chord means the command is
// deliberately disabled; a MISSING entry falls back to the default keymap (see mergeKeymap). A
// user override thus layers over defaults field by field, and can silence a default with "".
export type Keymap = Record<string, Chord>;

const MODIFIER_ORDER = ["mod", "alt", "shift"] as const;
type Modifier = (typeof MODIFIER_ORDER)[number];

// --- Registry ---------------------------------------------------------------
// One process-wide command map. registerCommand is idempotent by id (a re-register replaces),
// so a surface re-activating in the shell does not accumulate duplicates.
const registry = new Map<string, Command>();

export function registerCommand(cmd: Command): void {
  registry.set(cmd.id, cmd);
}

export function unregisterCommand(id: string): void {
  registry.delete(id);
}

// dispatchCommand runs a registered command by id, returning whether one was found. Menus, the
// tab strip, the palette, and the keybinding listener all funnel through here.
export function dispatchCommand(id: string, arg?: unknown): boolean {
  const cmd = registry.get(id);
  if (!cmd) return false;
  cmd.run(arg);
  return true;
}

// listCommands returns the registered commands, for a palette or a menu/shortcuts view.
export function listCommands(): Command[] {
  return [...registry.values()];
}

// --- Chords -----------------------------------------------------------------

// normalizeChord canonicalizes a hand-written chord ("Mod+Shift+K", "cmd+\\") to the stored form
// ("mod+shift+k"): modifiers lowercased and reordered to mod, alt, shift; a single-character key
// lowercased; a named key (Escape, ArrowLeft) kept verbatim. "cmd"/"ctrl"/"meta"/"control" all
// fold to "mod". Returns "" for an empty spec (the disabled sentinel).
export function normalizeChord(spec: string): Chord {
  if (spec.trim() === "") return "";
  const parts = spec.split("+").map((p) => p.trim()).filter((p) => p.length > 0);
  const mods = new Set<Modifier>();
  let key = "";
  for (const p of parts) {
    const low = p.toLowerCase();
    if (low === "mod" || low === "cmd" || low === "command" || low === "meta" || low === "ctrl" || low === "control") {
      mods.add("mod");
    } else if (low === "alt" || low === "option" || low === "opt") {
      mods.add("alt");
    } else if (low === "shift") {
      mods.add("shift");
    } else {
      key = p.length === 1 ? p.toLowerCase() : p; // last non-modifier token wins as the key
    }
  }
  return [...MODIFIER_ORDER.filter((m) => mods.has(m)), key].filter((t) => t.length > 0).join("+");
}

// The minimal keyboard-event shape chordFromEvent reads, so it is pure and testable without a
// real DOM KeyboardEvent. mac folds the accelerator: Cmd on macOS, Ctrl elsewhere.
export interface KeyChord {
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  key: string;
}

// chordFromEvent builds the canonical chord for a key event, folding the platform accelerator
// into "mod". Shift is included as a modifier (a letter's case is normalized away, so "shift+k"
// is stable). A bare modifier press yields "" (no key), which never matches a real binding.
export function chordFromEvent(e: KeyChord, mac: boolean): Chord {
  if (e.key === "Shift" || e.key === "Alt" || e.key === "Control" || e.key === "Meta") return "";
  const mods: Modifier[] = [];
  if (mac ? e.metaKey : e.ctrlKey) mods.push("mod");
  if (e.altKey) mods.push("alt");
  if (e.shiftKey) mods.push("shift");
  const key = e.key.length === 1 ? e.key.toLowerCase() : e.key;
  return [...MODIFIER_ORDER.filter((m) => mods.includes(m)), key].join("+");
}

// --- Keymap -----------------------------------------------------------------

// mergeKeymap overlays a user keymap over the defaults field by field. A user entry wins, INCLUDING
// an empty string (which disables that command); a command the user never mentions keeps its
// default. Both sides are normalized so a hand-edited "Cmd+K" matches the stored "mod+k".
export function mergeKeymap(defaults: Keymap, user: Keymap): Keymap {
  const out: Keymap = {};
  for (const [id, chord] of Object.entries(defaults)) out[id] = normalizeChord(chord);
  for (const [id, chord] of Object.entries(user)) out[id] = normalizeChord(chord);
  return out;
}

// resolveCommand reverse-looks-up which command a chord is bound to in a keymap, or null. A
// disabled ("") entry never matches, so silencing a default frees the key rather than firing.
export function resolveCommand(keymap: Keymap, chord: Chord): string | null {
  if (chord === "") return null;
  for (const [id, bound] of Object.entries(keymap)) {
    if (bound !== "" && normalizeChord(bound) === chord) return id;
  }
  return null;
}

// conflicts returns the command ids that share a chord with `chord` in `keymap` (excluding
// `exceptId`), so the settings keybinding editor can warn before saving a duplicate. Disabled
// entries never conflict.
export function conflicts(keymap: Keymap, chord: Chord, exceptId?: string): string[] {
  if (chord === "") return [];
  const target = normalizeChord(chord);
  const out: string[] = [];
  for (const [id, bound] of Object.entries(keymap)) {
    if (id !== exceptId && bound !== "" && normalizeChord(bound) === target) out.push(id);
  }
  return out;
}

// --- Installation (the only DOM-touching part) ------------------------------

// isMac reads the platform once so chordFromEvent folds the right accelerator. Guarded for a
// non-browser context (tests import this module without a navigator).
function isMac(): boolean {
  if (typeof navigator === "undefined") return false;
  return /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent || "");
}

// isTyping reports whether focus is in a text field, so a shortcut never fires while the operator
// types into the filter/search box (mirrors logs/dom.ts's guard).
function isTyping(node: EventTarget | null): boolean {
  const t = (node && (node as HTMLElement).tagName) || "";
  return t === "INPUT" || t === "TEXTAREA" || (node !== null && (node as HTMLElement).isContentEditable);
}

// installKeybindings wires ONE keydown listener that maps each event to a command via the current
// keymap and dispatches it. keymap is read through a getter so a live settings edit takes effect
// without re-installing. Skips while typing in a field. Returns a teardown that removes the
// listener (the shell calls it on unmount; a standalone page can ignore it).
export function installKeybindings(keymap: () => Keymap): () => void {
  const mac = isMac();
  const onKeyDown = (e: KeyboardEvent): void => {
    if (isTyping(e.target)) return;
    const id = resolveCommand(keymap(), chordFromEvent(e, mac));
    if (id === null) return;
    e.preventDefault();
    dispatchCommand(id);
  };
  document.addEventListener("keydown", onKeyDown);
  return () => document.removeEventListener("keydown", onKeyDown);
}
