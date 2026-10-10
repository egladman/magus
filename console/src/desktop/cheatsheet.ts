// cheatsheet.ts - a read-only keyboard cheat sheet: a centered card listing every command and its
// current chord, grouped by area. It reads the live command list + merged keymap, so it always shows
// the effective bindings. Open it by holding "?" (Shift+/) or the footer button; dismiss with the X,
// a click on the backdrop, or Escape. It is read-only - the keybinding editor (keybindings.ts) is the
// app that rebinds and persists.

import { formatChord, type Command, type Keymap } from "./commands";
import { buildModal, keepFocus } from "./modal";
import { h } from "./view";

// The live command list, the effective (merged) keymap, and the platform (for Cmd vs Ctrl labels),
// all read fresh on each reveal.
export interface CheatsheetDeps {
  commands: () => Command[];
  keymap: () => Keymap;
  mac: boolean;
}

export interface Cheatsheet {
  readonly el: HTMLElement;
  show(): void;
  hide(): void;
  toggle(): void; // the status-bar button flips it open/closed (the hold-"?" gesture only reveals)
}

// isTyping mirrors commands.ts's guard: never hijack "?" while the operator is typing it into a field.
function isTyping(node: EventTarget | null): boolean {
  const t = (node && (node as HTMLElement).tagName) || "";
  return (
    t === "INPUT" || t === "TEXTAREA" || (node !== null && (node as HTMLElement).isContentEditable)
  );
}

// createCheatsheet builds the overlay once (the console appends el) and owns its hold-to-reveal key
// listeners. HOLD_MS distinguishes a deliberate hold from an incidental "?" keystroke.
export function createCheatsheet(deps: CheatsheetDeps): Cheatsheet {
  const HOLD_MS = 250;

  // A dismissible modal: PF backdrop + bullseye + modal-box, matching the keybinding editor.
  const modal = buildModal({
    id: "console-cheatsheet",
    title: "Keyboard shortcuts",
    description: "Every command the console binds to a key, and the key that runs it.",
    boxClass: "console-shell-cheatsheet",
    onClose: () => hide(),
  });
  const { overlay, box, body } = modal;
  body.classList.add("console-shell-cheatsheet__body");
  const focus = keepFocus(box);
  // Two ways in, so two ways out, and the sheet says which applies: the hold is a peek that ends with
  // the key, the button leaves it open. Rebinding is what the Shortcuts app does and this sheet, which
  // only reads, cannot.
  modal.footer.append(
    h(
      "p",
      "console-shell-cheatsheet__hint",
      "Held open with ?, this closes when you let go. Opened from the status bar, it stays until " +
        "Esc or a click outside. To change a shortcut, open the Shortcuts app.",
    ),
  );
  // A click on the backdrop (outside the box) dismisses; a click inside the box does not. This must
  // stay "click", not "pointerdown": while open, the backdrop covers the status-bar toggle button
  // that opened the sheet. A "pointerdown" listener hides the overlay before the browser re-hit-tests
  // for the trailing "click" event, so that click lands on the now-exposed toggle button underneath
  // and immediately reopens it (flash-and-stay-open). "click" fires only after the full gesture,
  // while the backdrop is still on top, so the toggle button never sees the event.
  overlay.addEventListener("click", (ev) => {
    if (!box.contains(ev.target as Node)) hide();
  });

  // render paints the grouped rows from the current commands + effective keymap. Commands with no
  // (effective) chord are omitted - this is a keybinding sheet, not a command list. Groups keep
  // first-seen order so the layout is stable across reveals.
  function render(): void {
    body.replaceChildren();
    const keymap = deps.keymap();
    const groups = new Map<string, { label: string; chord: string }[]>();
    for (const cmd of deps.commands()) {
      // An app-local key WINS over a keymap entry for the same id, which is the opposite
      // of what it looks like it should do.
      //
      // The app dispatches these itself, from its own table, and never consults the
      // keymap - so a keymap entry for one of them changes nothing. Preferring the keymap
      // here would print a chord that does not work, which is worse than printing nothing:
      // this sheet's whole job is to say what will happen when you press something.
      const chord = (cmd.key ?? "") || formatChord(keymap[cmd.id] ?? "", deps.mac);
      if (chord === "") continue;
      const group = cmd.group || "General";
      let list = groups.get(group);
      if (!list) {
        list = [];
        groups.set(group, list);
      }
      list.push({ label: cmd.label, chord });
    }
    if (groups.size === 0) {
      body.append(h("p", "console-shell-cheatsheet__empty", "No keyboard shortcuts are bound."));
      return;
    }
    for (const [group, rows] of groups) {
      const section = h("section", "console-shell-shortcuts__group");
      section.append(h("h2", "console-shell-shortcuts__group-title", group));
      const list = h("dl", "console-shell-cheatsheet__list");
      for (const r of rows) {
        list.append(h("dt", "console-shell-cheatsheet__label", r.label));
        const dd = h("dd", "console-shell-cheatsheet__chord");
        // Each chord token as its own <kbd> reads as physical keys (Cmd + Shift + K).
        r.chord.split("+").forEach((tok, i) => {
          if (i > 0) dd.append(h("span", "console-shell-cheatsheet__plus", "+"));
          dd.append(h("kbd", "console-shell-keycap", tok));
        });
        list.append(dd);
      }
      section.append(list);
      body.append(section);
    }
  }

  let open = false;
  function show(): void {
    if (open) return;
    render();
    focus.opened();
    overlay.hidden = false;
    box.focus();
    open = true;
  }
  function hide(): void {
    if (!open) return;
    overlay.hidden = true;
    open = false;
    focus.closed();
  }
  function toggle(): void {
    if (open) hide();
    else show();
  }

  // Hold-to-reveal: a "?" keydown (not while typing) arms a short timer; if the key is still held
  // when it fires, the sheet appears. Any keyup of the chord's keys - "?", "/", or Shift - or a
  // window blur, cancels the timer and hides. Escape hides too.
  let timer: number | null = null;
  const clearTimer = (): void => {
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
  };

  document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key === "Escape" && open) {
      hide();
      return;
    }
    if (e.key !== "?" || e.repeat) return;
    if (isTyping(e.target)) return;
    if (open || timer !== null) return;
    e.preventDefault();
    timer = window.setTimeout(() => {
      timer = null;
      show();
    }, HOLD_MS);
  });
  document.addEventListener("keyup", (e: KeyboardEvent) => {
    if (e.key === "?" || e.key === "/" || e.key === "Shift") {
      clearTimer();
      hide();
    }
  });
  window.addEventListener("blur", () => {
    clearTimer();
    hide();
  });

  return { el: overlay, show, hide, toggle };
}
