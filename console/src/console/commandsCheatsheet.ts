// commandsCheatsheet.ts - a read-only overlay listing EVERY registered command, grouped by area. It
// is the command companion to the keyboard cheat sheet (cheatsheet.ts): where that one shows only the
// commands that HAVE a chord (a keybinding reference), this one is the full command catalogue - each
// row shows the canonical TOKEN (open.logs) in monospace, the prose label, and the chord when one is
// bound. Like the keyboard sheet it is STRICTLY read-only (a discovery aid, not the command bar), reads
// the SAME live command list + merged keymap every reveal, and shares that sheet's overlay/box chrome.

import { formatChord, type Command, type Keymap } from "./commands";
import { displayToken } from "./commandBar";
import { h } from "./view";

// What the console injects: the live command list and the effective (merged default+user) keymap, both
// read fresh on each reveal, plus the platform so any chord labels correctly (Cmd vs Ctrl).
export interface CommandsCheatsheetDeps {
  commands: () => Command[];
  keymap: () => Keymap;
  mac: boolean;
}

export interface CommandsCheatsheet {
  readonly el: HTMLElement;
  show(): void;
  hide(): void;
  toggle(): void; // the status-bar button flips it open/closed
}

// createCommandsCheatsheet builds the overlay once (the console appends el) and owns its own Escape
// listener. Same PF backdrop + bullseye + modal-box family as the keyboard cheat sheet, so the two
// read as members of one overlay set; read-only, so the backdrop is click-through (pointer-events off
// in CSS) while the box stays interactive - a long catalogue scrolls and the footer toggle stays live.
export function createCommandsCheatsheet(deps: CommandsCheatsheetDeps): CommandsCheatsheet {
  const overlay = h("div", "pf-v6-c-backdrop");
  overlay.id = "console-commands";
  overlay.hidden = true;
  overlay.setAttribute("role", "dialog");
  overlay.setAttribute("aria-modal", "false");
  overlay.setAttribute("aria-label", "All commands");

  const bullseye = h("div", "pf-v6-l-bullseye");
  const box = h("div", "pf-v6-c-modal-box pf-m-md console-cheatsheet-box");
  const head = h("div", "pf-v6-c-modal-box__header");
  const titleWrap = h("div", "pf-v6-c-modal-box__title");
  titleWrap.append(h("span", "pf-v6-c-modal-box__title-text", "All commands"));
  head.append(titleWrap);
  const body = h("div", "pf-v6-c-modal-box__body console-cheatsheet-box__body");
  const foot = h("p", "console-cheatsheet-box__hint", "Press Esc to dismiss. Open the command bar to run one.");
  box.append(head, body, foot);
  bullseye.append(box);
  overlay.append(bullseye);

  // render paints EVERY command grouped by area (first-seen order, so the layout is stable). Each row
  // is a token / label / chord triple; a command with no effective chord simply leaves the chord blank.
  function render(): void {
    body.replaceChildren();
    const keymap = deps.keymap();
    const groups = new Map<string, Command[]>();
    for (const cmd of deps.commands()) {
      const group = cmd.group || "General";
      if (!groups.has(group)) groups.set(group, []);
      groups.get(group)!.push(cmd);
    }
    if (groups.size === 0) {
      body.append(h("p", "console-cheatsheet-box__empty", "No commands are registered."));
      return;
    }
    for (const [group, cmds] of groups) {
      const section = h("section", "console-cheatsheet-group");
      section.append(h("h3", "console-cheatsheet-group__title", group));
      const list = h("div", "console-commands-group__list");
      for (const cmd of cmds) {
        list.append(h("code", "console-commands-token", displayToken(cmd.id)));
        list.append(h("span", "console-commands-label", cmd.label));
        const chordCell = h("span", "console-commands-chord");
        const chord = formatChord(keymap[cmd.id] ?? "", deps.mac);
        if (chord !== "") {
          // Each chord token as its own <kbd> reads as physical keys (Cmd + K), reusing the keyboard
          // sheet's keycap styling.
          chord.split("+").forEach((tok, i) => {
            if (i > 0) chordCell.append(h("span", "console-cheatsheet-group__plus", "+"));
            chordCell.append(h("kbd", "console-cheatsheet-kbd", tok));
          });
        }
        list.append(chordCell);
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

  // Escape closes it (the only key gesture it owns - there is no hold-to-reveal here; the footer
  // button and the command bar are how it opens).
  document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key === "Escape" && open) { hide(); }
  });

  return { el: overlay, show, hide, toggle };
}
