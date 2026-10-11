// The Shortcuts app: a first-class tab listing EVERY registered shortcut, grouped by area. It is
// the companion to the keyboard cheat sheet (desktop/cheatsheet.ts): where that one shows only the
// shortcuts that HAVE keys (a reference, opened by holding "?"), this one is the full catalogue - each
// row shows the shortcut's name, its canonical token (open.logs) in monospace, and its keys when some
// are bound. Unlike the cheat sheet it is a real app (page.ts), not a modal overlay: it mounts straight
// into the pane host like any other tab, so it can be opened, tiled, and moved to its own window the
// same way. Each row has a real Run button, so the page is a discovery aid AND a runner, the tab
// companion to the Command Palette; a shortcut with rebindable keys also gets an edit button that jumps
// to the keybinding editor, so this app both explains and edits what the Palette only runs.
//
// One word for the thing: a SHORTCUT is a named command the console can run, with keys when it has
// them. "Command" belongs to the Palette, where the reader types a command path.

import { formatChord, type Command, type Keymap } from "../../desktop/commands";
import { displayToken } from "../../desktop/commandBar";
import { h } from "../../desktop/view";
import type { PageController, PageModule, SearchProvider } from "../../desktop/page";

// What the console injects: the live command list and the effective (merged default+user) keymap, both
// read fresh on each activation, plus the platform so keys label correctly (Cmd vs Ctrl); run
// dispatches a row's command, editableIds gates which rows get a per-row edit button (only commands
// with a CONSOLE_KEYMAP default are rebindable), and onEditKeybindings opens Settings' keybindings
// editor - with an id, deep-linked and focused on that command's row.
export interface ShortcutsAppDeps {
  commands: () => Command[];
  keymap: () => Keymap;
  mac: boolean;
  run: (id: string) => void;
  editableIds: Set<string>;
  onEditKeybindings: (id?: string) => void;
}

// The app has no search grammar of its own (the list is short and grouped, not filtered) - the
// same no-op provider standalone.ts's wrapped apps opt into.
const noSearch: SearchProvider<null> = {
  placeholder: "",
  parse: () => null,
  apply: () => ({ matches: 0 }),
};

const SVG_NS = "http://www.w3.org/2000/svg";

// editIcon is the per-row edit glyph: a small pencil, matching the console's inline-SVG icon
// convention (createElementNS, stroke on currentColor so it themes for free, aria-hidden since the
// button it sits in already carries the accessible name).
function editIcon(): SVGElement {
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("width", "14");
  svg.setAttribute("height", "14");
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

let seq = 0;

// createShortcutsApp builds the PageModule. activate() paints the full catalogue into the pane host,
// grouped by area (first-seen order, so the layout is stable). Each row is a name / keys / actions
// triple: a shortcut with no effective keys leaves the keys blank, and only a rebindable one grows the
// edit button. Run and edit are siblings, never one control inside another.
export function createShortcutsApp(deps: ShortcutsAppDeps): PageModule<null, null> {
  return {
    id: "shortcuts",
    title: "Shortcuts",
    async activate(host: HTMLElement): Promise<PageController<null, null>> {
      const root = h("div");
      root.dataset.app = "shortcuts";

      // The banner says what the page does and names the one related control. It shares the list's
      // width: both are capped by the same rule in console.css.
      const banner = h("div", "console-shell-shortcuts__banner");
      banner.append(
        h(
          "p",
          undefined,
          "Run a shortcut from its row. To change its keys, use the edit button on the row, or edit them all at once.",
        ),
      );
      const editAllBtn = h("button", "pf-v6-c-button pf-m-secondary pf-m-small", "Edit shortcuts");
      editAllBtn.type = "button";
      editAllBtn.addEventListener("click", () => deps.onEditKeybindings());
      banner.append(editAllBtn);
      root.append(banner);

      const keymap = deps.keymap();
      const groups = new Map<string, Command[]>();
      for (const cmd of deps.commands()) {
        const group = cmd.group || "General";
        let list = groups.get(group);
        if (!list) {
          list = [];
          groups.set(group, list);
        }
        list.push(cmd);
      }
      if (groups.size === 0) {
        root.append(h("p", "console-shell-shortcuts__empty", "No shortcuts are registered."));
      }
      for (const [group, cmds] of groups) {
        const section = h("section", "console-shell-shortcuts__group");
        const heading = h("h2", "console-shell-shortcuts__group-title", group);
        section.append(heading);
        const list = h("ul", "console-shell-shortcuts__list");
        // A list with its markers removed loses its role in Safari; say it.
        list.setAttribute("role", "list");
        list.setAttribute("aria-labelledby", (heading.id = "console-shortcuts-group-" + ++seq));
        for (const cmd of cmds) {
          const row = h("li", "console-shell-shortcuts__row");

          const text = h("div", "console-shell-shortcuts__text");
          text.append(h("span", "console-shell-shortcuts__label", cmd.label));
          text.append(h("code", "console-shell-shortcuts__token", displayToken(cmd.id)));
          row.append(text);

          // App-local key first, for the reason cheatsheet.ts gives at its own call: the app
          // dispatches from its own table, so a keymap entry for one of these would display keys
          // that do nothing.
          const keys = h("span", "console-shell-shortcuts__keys");
          keys.id = "console-shortcut-keys-" + ++seq;
          const chord = (cmd.key ?? "") || formatChord(keymap[cmd.id] ?? "", deps.mac);
          if (chord !== "") {
            // Each key as its own <kbd> reads as physical keys (Cmd + K).
            chord.split("+").forEach((tok, i) => {
              if (i > 0) keys.append(h("span", "console-shell-shortcuts__plus", "+"));
              keys.append(h("kbd", "console-shell-keycap", tok));
            });
          }
          row.append(keys);

          const actions = h("div", "console-shell-shortcuts__actions");
          const runBtn = h("button", "pf-v6-c-button pf-m-secondary pf-m-small", "Run");
          runBtn.type = "button";
          runBtn.setAttribute("aria-label", "Run " + cmd.label);
          // The keys are the button's description: a reader who lands on Run hears which keys do this.
          if (chord !== "") runBtn.setAttribute("aria-describedby", keys.id);
          runBtn.addEventListener("click", () => deps.run(cmd.id));
          actions.append(runBtn);

          // Only commands with a CONSOLE_KEYMAP default are rebindable (the keybinding editor's
          // scope); the open.* rows and other keyless one-offs get no edit button.
          if (deps.editableIds.has(cmd.id)) {
            const editBtn = h("button", "pf-v6-c-button pf-m-plain pf-m-small");
            editBtn.type = "button";
            editBtn.dataset.role = "edit";
            editBtn.setAttribute("aria-label", "Edit shortcut for " + cmd.label);
            editBtn.title = "Edit shortcut";
            const icon = h("span", "pf-v6-c-button__icon");
            icon.append(editIcon());
            editBtn.append(icon);
            editBtn.addEventListener("click", () => deps.onEditKeybindings(cmd.id));
            actions.append(editBtn);
          }
          row.append(actions);
          list.append(row);
        }
        section.append(list);
        root.append(section);
      }

      host.append(root);
      return {
        search: noSearch,
        // Static, but still part of the shell lifecycle: this keeps the app contract
        // exhaustive when panes are focused, tiled, or backgrounded.
        setVisible() {},
        deactivate() {
          host.replaceChildren();
        },
      };
    },
  };
}
