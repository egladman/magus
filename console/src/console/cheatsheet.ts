// cheatsheet.ts - a read-only, hold-to-reveal keyboard cheat sheet. Hold "?" (Shift+/) and a
// centered card lists every command and its current chord, grouped by area; release the key to
// dismiss. It is STRICTLY read-only: a teaching aid, deliberately separate from the keybinding
// editor (keybindings.ts), which is the surface that rebinds and persists. It reads the SAME live
// command list + merged keymap the command bar and the global key listener use, so what it shows is
// always the effective bindings - a rebind in the editor is reflected here on the next reveal.

import { formatChord, type Command, type Keymap } from "./commands";
import { h } from "./view";

// What the console injects: the live command list and the effective (merged default+user) keymap,
// both read fresh on each reveal, plus the platform so chords label correctly (Cmd vs Ctrl).
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

// isTyping mirrors commands.ts's guard: never hijack "?" while the operator is typing it into a
// field (the filter box, the docs search, a rebind capture).
function isTyping(node: EventTarget | null): boolean {
  const t = (node && (node as HTMLElement).tagName) || "";
  return t === "INPUT" || t === "TEXTAREA" || (node !== null && (node as HTMLElement).isContentEditable);
}

// createCheatsheet builds the overlay once, appends nothing (the console appends el), and owns its
// own hold-to-reveal key listeners. HOLD_MS distinguishes a deliberate hold from an incidental "?"
// keystroke, so a quick tap does not flash the sheet.
export function createCheatsheet(deps: CheatsheetDeps): Cheatsheet {
  const HOLD_MS = 250;

  // PF backdrop + bullseye + modal-box, same family as the keybinding editor, so the
  // cheat sheet reads as a member of the console's overlay set. It is read-only, so it neither traps
  // focus nor takes pointer events - releasing the key is the only way it goes away.
  const overlay = h("div", "pf-v6-c-backdrop");
  overlay.id = "console-cheatsheet";
  overlay.hidden = true;
  overlay.setAttribute("role", "dialog");
  overlay.setAttribute("aria-modal", "false");
  overlay.setAttribute("aria-label", "Keyboard shortcuts");

  const bullseye = h("div", "pf-v6-l-bullseye");
  const box = h("div", "pf-v6-c-modal-box pf-m-md console-cheatsheet-box");
  const head = h("div", "pf-v6-c-modal-box__header");
  const titleWrap = h("div", "pf-v6-c-modal-box__title");
  titleWrap.append(h("span", "pf-v6-c-modal-box__title-text", "Keyboard shortcuts"));
  head.append(titleWrap);
  const body = h("div", "pf-v6-c-modal-box__body console-cheatsheet-box__body");
  const foot = h("p", "console-cheatsheet-box__hint", "Press Esc to dismiss. Open the command bar to rebind.");
  box.append(head, body, foot);
  bullseye.append(box);
  overlay.append(bullseye);

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
