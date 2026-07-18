// commands.ts - the console's single command surface and its keybinding engine. Every console
// action (toggle raw view, fold all, focus the filter, split a pane, close a tab) is registered
// here as a named command; menus, the tab bar, the command bar, and keybindings all dispatch
// through this one map, so an action is defined once and bound once. It is a registerCommand/
// dispatchCommand registry over a flat keymap record. Everything except installKeybindings' single
// listener is a pure function (the registry map, chord normalization, keymap merge, and command
// resolution), unit-tested without a browser.

// A command: a stable id, a human label for menus/command bar, an optional group for ordering, and
// the handler. arg carries an optional payload (e.g. a direction for a "focus pane" command).
export interface Command {
  id: string;
  label: string;
  group?: string;
  run: (arg?: unknown) => void;
}

// A chord is a canonical modifier+key string: modifiers in the fixed order mod, alt, shift, then
// one key, "+"-joined, e.g. "mod+shift+k". "mod" is the platform accelerator (Cmd on macOS,
// Ctrl elsewhere), so a binding is written once and works on both.
//
// A binding VALUE may be a SEQUENCE of chords: two or more chords SPACE-joined, pressed in order
// (Emacs-style prefixes, e.g. "mod+x o" or "mod+x mod+o"). A single chord is the degenerate
// one-chord sequence, so every existing binding is already a valid value and nothing about the
// single-chord path changes. The empty string is a valid keymap value meaning "deliberately unbound"
// (see Keymap). The type name stays `Chord` for continuity; read it as "a chord or chord sequence".
export type Chord = string;

// A keymap is a flat record of commandId -> chord (or chord sequence). An empty-string value means the
// command is deliberately disabled; a MISSING entry falls back to the default keymap (see mergeKeymap).
// A user override thus layers over defaults field by field, and can silence a default with "".
export type Keymap = Record<string, Chord>;

const MODIFIER_ORDER = ["mod", "alt", "shift"] as const;
type Modifier = (typeof MODIFIER_ORDER)[number];

// The separator between chords in a sequence: a single space. normalizeSequence collapses any run of
// whitespace to this, so a hand-written "mod+x   o" stores as "mod+x o".
const SEQ_SEP = " ";

// --- Registry ---------------------------------------------------------------
// One process-wide command map. registerCommand is idempotent by id (a re-register replaces),
// so a surface re-activating in the console does not accumulate duplicates.
const registry = new Map<string, Command>();

export function registerCommand(cmd: Command): void {
  registry.set(cmd.id, cmd);
}

export function unregisterCommand(id: string): void {
  registry.delete(id);
}

// dispatchCommand runs a registered command by id, returning whether one was found. Menus, the
// tab bar, the command bar, and the keybinding listener all funnel through here.
export function dispatchCommand(id: string, arg?: unknown): boolean {
  const cmd = registry.get(id);
  if (!cmd) return false;
  cmd.run(arg);
  return true;
}

// listCommands returns the registered commands, for a command bar or a menu/shortcuts view.
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

// normalizeSequence canonicalizes a whole binding - one chord OR a space-joined sequence - by
// normalizing each chord and rejoining with a single space. "Ctrl+X  o" -> "mod+x o", "Cmd+K" ->
// "mod+k", "  " -> "" (the disabled sentinel). This is the canonicalizer every keymap operation runs,
// so a single chord and a sequence go through the same path; normalizeChord handles one chord.
export function normalizeSequence(spec: string): Chord {
  return spec
    .split(/\s+/)
    .map((c) => normalizeChord(c))
    .filter((c) => c.length > 0)
    .join(SEQ_SEP);
}

// hasSequenceWithPrefix reports whether any binding in `keymap` is a STRICT sequence extension of
// `prefix` (i.e. begins with `prefix` then at least one more chord), e.g. prefix "mod+x" against a
// keymap holding "mod+x o". The sequence matcher uses it to decide whether to WAIT for more keys after
// an incomplete chord run. Both sides are normalized so a raw default and a live candidate compare
// cleanly.
export function hasSequenceWithPrefix(keymap: Keymap, prefix: string): boolean {
  const p = normalizeSequence(prefix);
  if (p === "") return false;
  for (const bound of Object.values(keymap)) {
    const b = normalizeSequence(bound);
    if (b !== "" && b.startsWith(p + SEQ_SEP)) return true;
  }
  return false;
}

// The minimal keyboard-event shape chordFromEvent reads, so it is pure and testable without a
// real DOM KeyboardEvent. mac folds the accelerator: Cmd on macOS, Ctrl elsewhere.
export interface KeyChord {
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  key: string;
  code?: string; // the physical key (KeyH, Digit2), layout-independent; used for alt-chords
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
  let key = e.key.length === 1 ? e.key.toLowerCase() : e.key;
  // Alt+<letter> composes a special glyph in e.key on many layouts (macOS Alt+h yields a dead
  // key, Alt+l a not-sign), so a written "alt+h" would never match the event's key. When Alt is
  // held, recover the physical letter/digit from the layout-independent e.code so alt-chords -
  // the pane-focus bindings (alt+hjkl) - bind reliably across platforms and layouts.
  if (e.altKey && e.code) {
    const m = /^Key([A-Z])$/.exec(e.code) ?? /^Digit([0-9])$/.exec(e.code);
    if (m) key = m[1].toLowerCase();
  }
  return [...MODIFIER_ORDER.filter((m) => mods.includes(m)), key].join("+");
}

// --- Keymap -----------------------------------------------------------------

// mergeKeymap overlays a user keymap over the defaults field by field. A user entry wins, INCLUDING
// an empty string (which disables that command); a command the user never mentions keeps its
// default. Both sides are normalized so a hand-edited "Cmd+K" matches the stored "mod+k".
export function mergeKeymap(defaults: Keymap, user: Keymap): Keymap {
  const out: Keymap = {};
  for (const [id, chord] of Object.entries(defaults)) out[id] = normalizeSequence(chord);
  for (const [id, chord] of Object.entries(user)) out[id] = normalizeSequence(chord);
  return out;
}

// resolveCommand reverse-looks-up which command an EXACT chord sequence is bound to in a keymap, or
// null. A disabled ("") entry never matches, so silencing a default frees the keys rather than firing.
// The query and each binding are normalized, so a single chord and a sequence resolve the same way.
export function resolveCommand(keymap: Keymap, chord: Chord): string | null {
  const target = normalizeSequence(chord);
  if (target === "") return null;
  for (const [id, bound] of Object.entries(keymap)) {
    if (bound !== "" && normalizeSequence(bound) === target) return id;
  }
  return null;
}

// seqConflict reports whether two bindings cannot coexist in a live keymap: they are equal, OR one is a
// strict sequence prefix of the other (the prefix fires - or opens its wait - before the longer can
// ever complete, so the longer is unreachable). Both are assumed already normalized.
function seqConflict(a: Chord, b: Chord): boolean {
  return a === b || a.startsWith(b + SEQ_SEP) || b.startsWith(a + SEQ_SEP);
}

// conflicts returns the command ids whose binding collides with `chord` in `keymap` (excluding
// `exceptId`), so the settings keybinding editor can warn before saving. A collision is an exact
// duplicate OR a prefix shadow (e.g. "mod+x" shadows "mod+x o"). Disabled entries never conflict.
export function conflicts(keymap: Keymap, chord: Chord, exceptId?: string): string[] {
  const target = normalizeSequence(chord);
  if (target === "") return [];
  const out: string[] = [];
  for (const [id, bound] of Object.entries(keymap)) {
    if (id !== exceptId && bound !== "" && seqConflict(normalizeSequence(bound), target)) out.push(id);
  }
  return out;
}

// The human labels for the non-modifier keys formatChord prettifies (an arrow, a space, escape).
// Everything else formats from its stored token (a single letter uppercased, a named key verbatim).
const KEY_LABELS: Record<string, string> = {
  ArrowLeft: "Left", ArrowRight: "Right", ArrowUp: "Up", ArrowDown: "Down", " ": "Space", Escape: "Esc",
};

// formatSingleChord renders ONE stored chord ("mod+shift+k") for display ("Cmd+Shift+K" on macOS,
// "Ctrl+Shift+K" elsewhere).
function formatSingleChord(chord: Chord, mac: boolean): string {
  return chord.split("+").map((t) => {
    if (t === "mod") return mac ? "Cmd" : "Ctrl";
    if (t === "alt") return mac ? "Option" : "Alt";
    if (t === "shift") return "Shift";
    if (KEY_LABELS[t]) return KEY_LABELS[t];
    return t.length === 1 ? t.toUpperCase() : t;
  }).join("+");
}

// formatChord renders a stored binding for display - what the command bar and the keybinding editor
// show beside a command. A single chord renders as before ("Cmd+Shift+K"); a SEQUENCE renders each
// chord space-joined ("mod+x o" -> "Cmd+X O"), so a multi-key prefix reads as the steps you press.
// Pure and platform-parameterized (mac passed in) so it is testable without a navigator. An empty
// binding (deliberately unbound) renders as "".
export function formatChord(chord: Chord, mac: boolean): string {
  if (chord === "") return "";
  return chord.split(SEQ_SEP).filter((c) => c.length > 0).map((c) => formatSingleChord(c, mac)).join(" ");
}

// --- Installation (the only DOM-touching part) ------------------------------

// isMac reads the platform so chordFromEvent folds the right accelerator and formatChord labels it.
// Guarded for a non-browser context (tests import this module without a navigator).
export function isMac(): boolean {
  if (typeof navigator === "undefined") return false;
  return /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent || "");
}

// isTyping reports whether focus is in a text field, so a shortcut never fires while the operator
// types into the filter/search box (mirrors logs/dom.ts's guard).
function isTyping(node: EventTarget | null): boolean {
  const t = (node && (node as HTMLElement).tagName) || "";
  return t === "INPUT" || t === "TEXTAREA" || (node !== null && (node as HTMLElement).isContentEditable);
}

// How long a partial sequence waits for its next chord before it lapses. Emacs-ish: long enough to
// press "mod+x" then "o" unhurried, short enough that an abandoned prefix does not linger.
const SEQUENCE_TIMEOUT_MS = 1200;

// SequenceOutcome is one step of the sequence matcher: what to do with the chord just pressed given the
// chords already pending. `consumed` says whether the key belongs to the binding system (preventDefault
// it); `fire` is a command to dispatch NOW; `pending` is the buffer to carry forward (non-empty means
// "waiting for the next chord"); `waitFor` is the command that should fire if that wait times out (set
// when the pending run is ALSO already a complete binding, Emacs-style).
export interface SequenceOutcome {
  consumed: boolean;
  fire: string | null;
  pending: Chord[];
  waitFor: string | null;
}

// advanceSequence is the pure core of the keybinding matcher: fold one chord into the pending run
// against a keymap. It never touches the DOM or a timer, so it is unit-tested directly; installKeybindings
// wraps it with the actual listener + timeout. Three outcomes: the run is an exact binding with nothing
// longer sharing its prefix -> FIRE; the run is a live prefix of a longer binding -> WAIT (carry it,
// remembering any exact match to fire on timeout); the run matches nothing -> if we were mid-sequence
// the chord broke it, so re-evaluate the chord ALONE as a fresh start; otherwise it is not ours, pass it
// through untouched.
export function advanceSequence(pending: Chord[], chord: Chord, keymap: Keymap): SequenceOutcome {
  const candidate = [...pending, chord].join(SEQ_SEP);
  const exact = resolveCommand(keymap, candidate);
  const longer = hasSequenceWithPrefix(keymap, candidate);
  if (exact !== null && !longer) return { consumed: true, fire: exact, pending: [], waitFor: null };
  if (longer) return { consumed: true, fire: null, pending: [...pending, chord], waitFor: exact };
  if (pending.length > 0) return advanceSequence([], chord, keymap); // broken prefix: re-evaluate fresh
  return { consumed: false, fire: null, pending: [], waitFor: null };
}

// installKeybindings wires ONE keydown listener that maps keystrokes to commands via the current keymap
// and dispatches them. It is a small SEQUENCE matcher: most bindings are one chord and fire on the
// first keydown exactly as before, but a binding can be a multi-chord sequence (Emacs-style "mod+x o"),
// so the listener holds a pending-prefix buffer. On each chord it asks the keymap three things - is the
// run so far an exact binding, and/or a strict prefix of a longer one - and either fires, waits for
// more, or resets. A pending prefix swallows its keys (preventDefault) so "mod+x" does not also trigger
// the browser; a chord that matches nothing while idle passes straight through untouched. keymap is
// read through a getter so a live settings edit takes effect without re-installing; typing in a field
// is skipped. Returns a teardown that removes the listener and clears any pending sequence.
export function installKeybindings(keymap: () => Keymap): () => void {
  const mac = isMac();
  let pending: Chord[] = [];          // chords pressed so far in the in-progress sequence
  let pendingExact: string | null = null; // command the current run already fully binds (fires on timeout)
  let timer: number | null = null;

  const reset = (): void => {
    pending = [];
    pendingExact = null;
    if (timer !== null) { clearTimeout(timer); timer = null; }
  };
  const armTimeout = (): void => {
    if (timer !== null) clearTimeout(timer);
    timer = window.setTimeout(() => {
      const fire = pendingExact;
      reset();
      if (fire) dispatchCommand(fire); // the pending run was itself a complete binding: honor it
    }, SEQUENCE_TIMEOUT_MS);
  };

  const onKeyDown = (e: KeyboardEvent): void => {
    if (isTyping(e.target)) return;
    const chord = chordFromEvent(e, mac);
    if (chord === "") return; // a bare modifier: keep any pending sequence alive, consume nothing
    const out = advanceSequence(pending, chord, keymap());
    if (out.consumed) e.preventDefault();
    if (out.fire) { reset(); dispatchCommand(out.fire); return; }
    if (out.pending.length > 0) { pending = out.pending; pendingExact = out.waitFor; armTimeout(); return; }
    reset();
  };
  document.addEventListener("keydown", onKeyDown);
  return () => { reset(); document.removeEventListener("keydown", onKeyDown); };
}
