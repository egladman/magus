// actions.ts - the Actions surface: a first-class tab listing EVERY registered command, grouped by
// area. It is the command companion to the keyboard cheat sheet (cheatsheet.ts): where that one shows
// only the commands that HAVE a chord (a keybinding reference, opened by holding "?"), this one is the
// full command catalogue - each row shows the canonical TOKEN (open.logs) in monospace, the prose
// label, and the chord when one is bound. Unlike the cheat sheet it is a real surface (page.ts), not a
// modal overlay: it mounts straight into the pane host like any other tab, so it can be opened, tiled,
// and moved to its own window the same way. Each row is CLICK-TO-RUN (a discovery aid AND a runner,
// the tab companion to the action bar); a command with a rebindable chord also gets a per-row jump to
// the keybindings editor, so this surface both explains and edits what the action bar only runs.

import { formatChord, type Command, type Keymap } from "./commands";
import { displayToken } from "./commandBar";
import { h } from "./view";
import type { PageController, PageModule, SearchProvider } from "./page";

// What the console injects: the live command list and the effective (merged default+user) keymap, both
// read fresh on each activation, plus the platform so any chord labels correctly (Cmd vs Ctrl); run
// dispatches a row's command, editableIds gates which rows get a per-row edit-shortcut button (only
// commands with a CONSOLE_KEYMAP default are rebindable), and onEditKeybindings opens Settings'
// keybindings editor - with an id, deep-linked and focused on that command's row.
export interface ActionsSurfaceDeps {
  commands: () => Command[];
  keymap: () => Keymap;
  mac: boolean;
  run: (id: string) => void;
  editableIds: Set<string>;
  onEditKeybindings: (id?: string) => void;
}

// The surface has no search grammar of its own (the list is short and grouped, not filtered) - the
// same no-op provider standalone.ts's wrapped apps opt into.
const noSearch: SearchProvider<null> = { placeholder: "", parse: () => null, apply: () => ({ matches: 0 }) };

const SVG_NS = "http://www.w3.org/2000/svg";

// editIcon is the per-row "edit shortcut" glyph: a small pencil, matching the console's inline-SVG icon
// convention (createElementNS, stroke on currentColor so it themes for free, aria-hidden since the
// button it sits in already carries the accessible name).
function editIcon(): SVGElement {
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("width", "13");
  svg.setAttribute("height", "13");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.7");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS(SVG_NS, "path");
  path.setAttribute("d", "M12 20h9M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z");
  svg.append(path);
  return svg;
}

// createActionsSurface builds the PageModule. activate() paints the full command catalogue into the
// pane host, grouped by area (first-seen order, so the layout is stable). Each row is a token / label
// / chord triple, clickable to run the command; a command with no effective chord simply leaves the
// chord blank, and only a rebindable command grows the trailing edit-shortcut button.
export function createActionsSurface(deps: ActionsSurfaceDeps): PageModule<null, null> {
  return {
    id: "actions",
    title: "Actions",
    async activate(host: HTMLElement): Promise<PageController<null, null>> {
      const root = h("div");
      root.dataset.surface = "actions";

      // The banner replaces the old read-only lede: this surface now runs actions, not just lists
      // them, so it leads with that plus the one place shortcuts are changed.
      const banner = h("div", "console-actions__banner");
      banner.append(h("p", undefined, "Click an action to run it. To change a shortcut, edit your keybindings."));
      const editKeybindingsBtn = h("button", "pf-v6-c-button pf-m-secondary pf-m-small", "Edit keybindings");
      editKeybindingsBtn.type = "button";
      editKeybindingsBtn.addEventListener("click", () => deps.onEditKeybindings());
      banner.append(editKeybindingsBtn);
      root.append(banner);

      const keymap = deps.keymap();
      const groups = new Map<string, Command[]>();
      for (const cmd of deps.commands()) {
        const group = cmd.group || "General";
        if (!groups.has(group)) groups.set(group, []);
        groups.get(group)!.push(cmd);
      }
      if (groups.size === 0) {
        root.append(h("p", undefined, "No commands are registered."));
      }
      for (const [group, cmds] of groups) {
        const section = h("section", "console-cheatsheet-group");
        section.append(h("h3", "console-cheatsheet-group__title", group));
        const list = h("div", "console-commands-group__list");
        for (const cmd of cmds) {
          // Each command is its own row element (a subgrid spanning the list's 3 tracks) rather than three
          // loose grid cells, so alternate rows can carry a zebra band - the eye tracks a token across to
          // its chord without drifting onto the neighbour on these wide, space-maximized lines. The row
          // is also a click-to-run control (role=button; a real <button> can't wrap a subgrid row plus a
          // nested real <button> for the edit action below).
          const row = h("div", "console-commands-row");
          row.setAttribute("role", "button");
          row.tabIndex = 0;
          row.setAttribute("aria-label", "Run " + cmd.label);
          const runRow = (): void => deps.run(cmd.id);
          row.addEventListener("click", runRow);
          row.addEventListener("keydown", (ev) => {
            if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); runRow(); }
          });
          row.append(h("code", "console-commands-token", displayToken(cmd.id)));
          row.append(h("span", "console-commands-label", cmd.label));
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
          // Only commands with a CONSOLE_KEYMAP default are rebindable (the keybindings editor's scope);
          // the open.* rows and other chordless one-offs get no edit control. stopPropagation keeps a
          // click on this nested real <button> from also firing the row's own run handler.
          if (deps.editableIds.has(cmd.id)) {
            const editBtn = h("button", "pf-v6-c-button pf-m-plain pf-m-small console-commands-editbtn");
            editBtn.type = "button";
            editBtn.setAttribute("aria-label", "Edit shortcut for " + cmd.label);
            editBtn.title = "Edit shortcut";
            const icon = h("span", "pf-v6-c-button__icon");
            icon.append(editIcon());
            editBtn.append(icon);
            editBtn.addEventListener("click", (ev) => {
              ev.stopPropagation();
              deps.onEditKeybindings(cmd.id);
            });
            chordCell.append(editBtn);
          }
          row.append(chordCell);
          list.append(row);
        }
        section.append(list);
        root.append(section);
      }

      host.append(root);
      return {
        search: noSearch,
        deactivate() { host.replaceChildren(); },
      };
    },
  };
}
