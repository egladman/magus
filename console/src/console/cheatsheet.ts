// cheatsheet.ts - a read-only keyboard cheat sheet: a centered card listing every command and its
// current chord, grouped by area. It reads the live command list + merged keymap, so it always shows
// the effective bindings. Open it by holding "?" (Shift+/) or the footer button; dismiss with the X,
// a click on the backdrop, or Escape. It is read-only - the keybinding editor (keybindings.ts) is the
// surface that rebinds and persists.

import { formatChord, type Command, type Keymap } from "./commands";
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
  return t === "INPUT" || t === "TEXTAREA" || (node !== null && (node as HTMLElement).isContentEditable);
}

// createCheatsheet builds the overlay once (the console appends el) and owns its hold-to-reveal key
// listeners. HOLD_MS distinguishes a deliberate hold from an incidental "?" keystroke.
export function createCheatsheet(deps: CheatsheetDeps): Cheatsheet {
  const HOLD_MS = 250;

  // A dismissible modal: PF backdrop + bullseye + modal-box, matching the keybinding editor.
  const overlay = h("div", "pf-v6-c-backdrop");
  overlay.id = "console-cheatsheet";
  overlay.hidden = true;
  overlay.setAttribute("role", "dialog");
  overlay.setAttribute("aria-modal", "true");
  overlay.setAttribute("aria-label", "Keyboard shortcuts");

  const bullseye = h("div", "pf-v6-l-bullseye");
  const box = h("div", "pf-v6-c-modal-box pf-m-md console-cheatsheet-box");
  const head = h("div", "pf-v6-c-modal-box__header");
  const titleWrap = h("div", "pf-v6-c-modal-box__title");
  titleWrap.append(h("span", "pf-v6-c-modal-box__title-text", "Keyboard shortcuts"));
  head.append(titleWrap);
  const closeBtn = h("button", "pf-v6-c-button pf-m-plain pf-v6-c-modal-box__close");
  closeBtn.type = "button";
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.append(h("span", "pf-v6-c-button__icon", "×")); // multiplication sign - a crisp close glyph
  closeBtn.addEventListener("click", () => hide());
  const body = h("div", "pf-v6-c-modal-box__body console-cheatsheet-box__body");
  const foot = h("p", "console-cheatsheet-box__hint", "Press Esc or click outside to dismiss. Open the action bar to rebind.");
  box.append(head, closeBtn, body, foot);
  bullseye.append(box);
  overlay.append(bullseye);
  // A click on the backdrop (outside the box) dismisses; a click inside the box does not. This must
  // stay "click", not "pointerdown": while open, the backdrop covers the status-bar toggle button
  // that opened the sheet. A "pointerdown" listener hides the overlay before the browser re-hit-tests
  // for the trailing "click" event, so that click lands on the now-exposed toggle button underneath
  // and immediately reopens it (flash-and-stay-open). "click" fires only after the full gesture,
  // while the backdrop is still on top, so the toggle button never sees the event.
  overlay.addEventListener("click", (ev) => { if (!box.contains(ev.target as Node)) hide(); });

  // render paints the grouped rows from the current commands + effective keymap. Commands with no
  // (effective) chord are omitted - this is a keybinding sheet, not a command list. Groups keep
  // first-seen order so the layout is stable across reveals.
  function render(): void {
    body.replaceChildren();
    const keymap = deps.keymap();
    const groups = new Map<string, { label: string; chord: string }[]>();
    for (const cmd of deps.commands()) {
      const chord = formatChord(keymap[cmd.id] ?? "", deps.mac);
      if (chord === "") continue;
      const group = cmd.group || "General";
      if (!groups.has(group)) groups.set(group, []);
      groups.get(group)!.push({ label: cmd.label, chord });
    }
    if (groups.size === 0) {
      body.append(h("p", "console-cheatsheet-box__empty", "No keyboard shortcuts are bound."));
      return;
    }
    for (const [group, rows] of groups) {
      const section = h("section", "console-cheatsheet-group");
      section.append(h("h3", "console-cheatsheet-group__title", group));
      const list = h("dl", "console-cheatsheet-group__list");
      for (const r of rows) {
        list.append(h("dt", "console-cheatsheet-group__label", r.label));
        const dd = h("dd", "console-cheatsheet-group__chord");
        // Each chord token as its own <kbd> reads as physical keys (Cmd + Shift + K).
        r.chord.split("+").forEach((tok, i) => {
          if (i > 0) dd.append(h("span", "console-cheatsheet-group__plus", "+"));
          dd.append(h("kbd", "console-cheatsheet-kbd", tok));
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
    overlay.hidden = false;
    open = true;
  }
  function hide(): void {
    if (!open) return;
    overlay.hidden = true;
    open = false;
  }
  function toggle(): void {
    if (open) hide();
    else show();
  }

  // Hold-to-reveal: a "?" keydown (not while typing) arms a short timer; if the key is still held
  // when it fires, the sheet appears. Any keyup of the chord's keys - "?", "/", or Shift - or a
  // window blur, cancels the timer and hides. Escape hides too.
  let timer: number | null = null;
  const clearTimer = (): void => { if (timer !== null) { clearTimeout(timer); timer = null; } };

  document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key === "Escape" && open) { hide(); return; }
    if (e.key !== "?" || e.repeat) return;
    if (isTyping(e.target)) return;
    if (open || timer !== null) return;
    e.preventDefault();
    timer = window.setTimeout(() => { timer = null; show(); }, HOLD_MS);
  });
  document.addEventListener("keyup", (e: KeyboardEvent) => {
    if (e.key === "?" || e.key === "/" || e.key === "Shift") { clearTimer(); hide(); }
  });
  window.addEventListener("blur", () => { clearTimer(); hide(); });

  return { el: overlay, show, hide, toggle };
}
