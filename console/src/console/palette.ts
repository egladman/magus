// palette.ts - the command palette (mod+k): one searchable overlay listing every registered command
// with its current chord, so an action is discoverable and runnable without memorizing a key. It is a
// pure VIEW over the commands.ts registry (listCommands) + the merged keymap - the palette owns no
// command state of its own. The filter (matchCommands) is pure and unit-tested; the overlay below is a
// thin DOM layer with its own local keyboard handling (arrow to move, Enter to run, Esc to close), so
// it never fights the global keybinding listener.

import { formatChord, type Command, type Keymap } from "./commands";
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
// command (the console dispatches + the palette closes).
export interface PaletteDeps {
  commands(): Command[];
  keymap(): Keymap;
  mac: boolean;
  onRun(id: string): void;
}

export function createPalette(deps: PaletteDeps): Palette {
  // PatternFly (W2): a ModalBox centered in a Backdrop+Bullseye. The search sits in the modal header
  // (a PF form-control), the command list in the body as a PF Menu. The overlay id, role=dialog/
  // aria-modal, the [data-palette-box] hook, and role=listbox/option are preserved; the local
  // keyboard handling below is unchanged, so this stays a thin view over matchCommands (untouched).
  const overlay = h("div", "pf-v6-c-backdrop");
  overlay.id = "command-palette";
  overlay.hidden = true;
  overlay.setAttribute("role", "dialog");
  overlay.setAttribute("aria-modal", "true");
  overlay.setAttribute("aria-label", "Command palette");

  const bullseye = h("div", "pf-v6-l-bullseye");
  const box = h("div", "pf-v6-c-modal-box pf-m-sm");
  box.dataset.paletteBox = "";
  const header = h("div", "pf-v6-c-modal-box__header");
  const field = h("span", "pf-v6-c-form-control");
  const input = h("input");
  input.type = "text";
  input.placeholder = "Run a command";
  input.setAttribute("aria-label", "Search commands");
  field.append(input);
  header.append(field);
  const body = h("div", "pf-v6-c-modal-box__body");
  const menu = h("div", "pf-v6-c-menu");
  const menuContent = h("div", "pf-v6-c-menu__content");
  const list = h("ul", "pf-v6-c-menu__list");
  list.setAttribute("role", "listbox");
  menuContent.append(list);
  menu.append(menuContent);
  body.append(menu);
  box.append(header, body);
  bullseye.append(box);
  overlay.append(bullseye);

  let filtered: Command[] = [];
  let selected = 0;

  // renderList repaints the filtered commands as PF menu items, each showing the label and its chord
  // (or nothing when unbound). The selected row carries pf-m-focus (PF's highlight) + aria-selected.
  function renderList(): void {
    filtered = matchCommands(deps.commands(), input.value);
    if (selected >= filtered.length) selected = Math.max(0, filtered.length - 1);
    const km = deps.keymap();
    list.replaceChildren();
    filtered.forEach((c, i) => {
      const li = h("li", "pf-v6-c-menu__list-item");
      li.setAttribute("role", "presentation");
      if (i === selected) li.classList.add("pf-m-focus");
      const btn = h("button", "pf-v6-c-menu__item");
      btn.type = "button";
      btn.dataset.cmd = c.id;
      btn.setAttribute("role", "option");
      btn.setAttribute("aria-selected", i === selected ? "true" : "false");
      const main = h("span", "pf-v6-c-menu__item-main");
      main.append(h("span", "pf-v6-c-menu__item-text", c.label));
      const chord = formatChord(km[c.id] ?? "", deps.mac);
      const kbd = h("kbd", undefined, chord);
      if (chord === "") kbd.hidden = true;
      main.append(kbd);
      btn.append(main);
      li.append(btn);
      btn.addEventListener("click", () => run(c.id));
      li.addEventListener("pointermove", () => { if (selected !== i) { selected = i; markSelection(); } });
      list.append(li);
    });
  }

  // markSelection moves the highlight without rebuilding the list (cheaper on arrow-key navigation)
  // and keeps the selected row scrolled into view. Toggles pf-m-focus on the list item (PF's
  // highlight) and aria-selected on its option button.
  function markSelection(): void {
    [...list.children].forEach((li, i) => {
      const on = i === selected;
      (li as HTMLElement).classList.toggle("pf-m-focus", on);
      li.querySelector('[role="option"]')?.setAttribute("aria-selected", on ? "true" : "false");
      if (on) (li as HTMLElement).scrollIntoView({ block: "nearest" });
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
    overlay.hidden = false;
    input.value = "";
    selected = 0;
    renderList();
    input.focus();
  }

  function close(): void {
    overlay.hidden = true;
  }

  input.addEventListener("input", () => { selected = 0; renderList(); });
  // Local keyboard handling: arrows move the selection, Enter runs it, Esc closes. Stop propagation so
  // the global keybinding listener does not also act on these while the palette owns focus.
  input.addEventListener("keydown", (ev) => {
    if (ev.key === "ArrowDown") { ev.preventDefault(); move(1); }
    else if (ev.key === "ArrowUp") { ev.preventDefault(); move(-1); }
    else if (ev.key === "Enter") { ev.preventDefault(); if (filtered[selected]) run(filtered[selected].id); }
    else if (ev.key === "Escape") { ev.preventDefault(); close(); }
    ev.stopPropagation();
  });
  // A click on the backdrop (outside the box) dismisses; a click inside stays. The Bullseye layout
  // fills the backdrop, so test containment against the box rather than an exact overlay-target match.
  overlay.addEventListener("pointerdown", (ev) => { if (!box.contains(ev.target as Node)) close(); });

  return { el: overlay, open, close, isOpen: () => !overlay.hidden };
}
