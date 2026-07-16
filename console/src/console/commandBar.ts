// palette.ts - the command palette (mod+k), styled after dmenu: ONE dense horizontal bar pinned to
// the very top of the viewport - a short prompt, an inline text input, and the matching commands laid
// out side by side to the input's right, extras truncated off the bar's end. Flat text only: no cards,
// no icons, no chords in the row - the selected item is simply inverted with the accent. It is a pure
// VIEW over the commands.ts registry (listCommands) - the palette owns no command state of its own.
// The filter (matchCommands) is pure and unit-tested; the bar below is a thin DOM layer with its own
// local keyboard handling (Left/Right or Up/Down or Tab to move, Enter to run, Esc to close), so it
// never fights the global keybinding listener.
//
// This is the console's ONE sanctioned fully-custom component (no PatternFly analog renders a dmenu
// bar), so its classes follow the PATTERNFLY.md formula: console-shell-palette__<element>, transient
// selection state as data-selected. Styled in console.css against PF tokens so both themes work.

import { type Command, type Keymap } from "./commands";
import { h } from "./view";

// matchCommands filters the command list by a case-insensitive substring over "<group> <label>",
// preserving registry order; an empty query returns everything. Pure - the palette's only logic.
export function matchCommands(commands: Command[], query: string): Command[] {
  const q = query.trim().toLowerCase();
  if (q === "") return commands;
  return commands.filter((c) => `${c.group ?? ""} ${c.label}`.toLowerCase().includes(q));
}

export interface Palette {
  readonly el: HTMLElement;
  open(): void;
  close(): void;
  isOpen(): boolean;
}

// What the console injects: the live command list and merged keymap (read through getters so a settings
// edit is reflected on next open), the platform accelerator for chord labels, and how to run a chosen
// command (the console dispatches + the palette closes). keymap/mac are part of the injection contract
// even though the dmenu row shows no chords (the keybindings editor owns chord display now).
export interface PaletteDeps {
  commands(): Command[];
  keymap(): Keymap;
  mac: boolean;
  onRun(id: string): void;
}

export function createPalette(deps: PaletteDeps): Palette {
  // The bar: prompt | input | items. role=combobox semantics are overkill for a dmenu; a labelled
  // dialog holding a labelled input and a listbox of options keeps the ARIA honest and simple.
  const bar = h("div", "console-shell-palette");
  bar.id = "command-palette";
  bar.hidden = true;
  bar.setAttribute("role", "dialog");
  bar.setAttribute("aria-label", "Command palette");

  const prompt = h("span", "console-shell-palette__prompt", "run");
  prompt.setAttribute("aria-hidden", "true");

  const input = h("input", "console-shell-palette__input");
  input.type = "text";
  input.setAttribute("aria-label", "Search commands");
  input.setAttribute("autocomplete", "off");
  input.setAttribute("spellcheck", "false");

  const items = h("div", "console-shell-palette__items");
  items.setAttribute("role", "listbox");
  items.setAttribute("aria-label", "Matching commands");

  bar.append(prompt, input, items);

  let filtered: Command[] = [];
  let selected = 0;

  // renderItems repaints the filtered commands as a flat horizontal run of text buttons. dmenu shows
  // only what fits: the items container clips overflow, and markSelection scrolls the selected one
  // into view, so arrowing past the edge pages the row without any extra chrome.
  function renderItems(): void {
    filtered = matchCommands(deps.commands(), input.value);
    if (selected >= filtered.length) selected = Math.max(0, filtered.length - 1);
    items.replaceChildren();
    filtered.forEach((c, i) => {
      const btn = h("button", "console-shell-palette__item", c.label);
      btn.type = "button";
      btn.dataset.cmd = c.id;
      btn.tabIndex = -1; // the input keeps focus; the row is keyboard-driven from there
      btn.setAttribute("role", "option");
      btn.setAttribute("aria-selected", i === selected ? "true" : "false");
      if (i === selected) btn.dataset.selected = "";
      btn.addEventListener("click", () => run(c.id));
      items.append(btn);
    });
  }

  // markSelection moves the inverted highlight without rebuilding the row (cheaper on arrow-key
  // navigation) and keeps the selected item scrolled into the visible run.
  function markSelection(): void {
    [...items.children].forEach((el, i) => {
      const on = i === selected;
      const b = el as HTMLElement;
      if (on) b.dataset.selected = "";
      else delete b.dataset.selected;
      b.setAttribute("aria-selected", on ? "true" : "false");
      if (on) b.scrollIntoView({ inline: "nearest", block: "nearest" });
    });
  }

  function move(delta: number): void {
    if (filtered.length === 0) return;
    selected = (selected + delta + filtered.length) % filtered.length;
    markSelection();
  }

  function run(id: string): void {
    close();
    deps.onRun(id);
  }

  function open(): void {
    bar.hidden = false;
    input.value = "";
    selected = 0;
    renderItems();
    input.focus();
  }

  function close(): void {
    bar.hidden = true;
  }

  input.addEventListener("input", () => { selected = 0; renderItems(); });
  // Local keyboard handling, dmenu-style: Left/Right walk the horizontal row (Up/Down alias to them,
  // Tab/Shift+Tab too), Enter runs the selection, Esc closes. Tab is captured so focus never leaves
  // the bar while it is open. Stop propagation so the global keybinding listener does not also act on
  // these while the palette owns focus.
  input.addEventListener("keydown", (ev) => {
    if (ev.key === "ArrowRight" || ev.key === "ArrowDown" || (ev.key === "Tab" && !ev.shiftKey)) { ev.preventDefault(); move(1); }
    else if (ev.key === "ArrowLeft" || ev.key === "ArrowUp" || (ev.key === "Tab" && ev.shiftKey)) { ev.preventDefault(); move(-1); }
    else if (ev.key === "Enter") { ev.preventDefault(); if (filtered[selected]) run(filtered[selected].id); }
    else if (ev.key === "Escape") { ev.preventDefault(); close(); }
    ev.stopPropagation();
  });
  // A click anywhere off the bar dismisses it (dmenu drops on focus loss); a click on the bar stays.
  document.addEventListener("pointerdown", (ev) => {
    if (!bar.hidden && !bar.contains(ev.target as Node)) close();
  });

  return { el: bar, open, close, isOpen: () => !bar.hidden };
}
