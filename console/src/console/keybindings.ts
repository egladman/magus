// keybindings.ts - the keybinding editor surface: a table of the console's commands, each with its
// current chord and controls to rebind, disable, or reset it. It edits the ONE persisted "keymap"
// cell that installKeybindings and the palette read live, so a rebind takes effect immediately - no
// save step. It is a real PageModule (opens as a tab or a tiling pane, dogfooding the shell), built
// into the console bundle since it pulls no heavy deps. The row model (keybindingRows) is pure and
// unit-tested; the capture/DOM below is a thin layer over the commands.ts chord helpers.
//
// Scope: it edits the CONSOLE's own commands (the ones with a known default in CONSOLE_KEYMAP - tabs,
// panes, the palette). Surface-level bindings (a log viewer's own keys) live in their bundles and are
// out of scope here; this stays a bounded, honest editor rather than a half-built global one.

import {
  chordFromEvent, conflicts, formatChord, isMac, mergeKeymap, normalizeChord,
  type Command, type Keymap,
} from "./commands";
import type { PageController, PageModule, SearchProvider } from "./page";
import type { Persisted } from "../lib/persist";
import { h } from "./view";

// One editor row: the command, its EFFECTIVE chord (a user override wins over the default), and where
// that chord came from - so the UI can badge a custom/disabled binding and enable "reset".
export interface KeybindingRow {
  id: string;
  label: string;
  group: string;
  chord: string;
  source: "default" | "custom" | "disabled";
}

// keybindingRows computes the editor rows for the console's commands: the effective chord is the user
// override when present (including "" = deliberately disabled), else the default. Pure.
export function keybindingRows(commands: Command[], defaults: Keymap, user: Keymap): KeybindingRow[] {
  return commands.map((c) => {
    const overridden = Object.prototype.hasOwnProperty.call(user, c.id);
    const chord = normalizeChord((overridden ? user[c.id] : defaults[c.id]) ?? "");
    const source: KeybindingRow["source"] = !overridden ? "default" : chord === "" ? "disabled" : "custom";
    return { id: c.id, label: c.label, group: c.group ?? "", chord, source };
  });
}

const noSearch: SearchProvider<null> = { placeholder: "", parse: () => null, apply: () => ({ matches: 0 }) };

// What the console injects: the commands to edit, their default chords (CONSOLE_KEYMAP), and the shared
// persisted keymap cell the whole console reads.
export interface KeybindingsDeps {
  commands: Command[];
  defaults: Keymap;
  keymap: Persisted<Keymap>;
}

// keybindingsPage builds the editor as a PageModule. It re-renders from the keymap cell (subscribing so
// a change elsewhere reflects here) and edits it in place; the capture listener is torn down on close.
export function keybindingsPage(deps: KeybindingsDeps): PageModule<null, null> {
  const mac = isMac();
  return {
    id: "keybindings",
    title: "Keybindings",
    async activate(host: HTMLElement): Promise<PageController<null, null>> {
      host.dataset.surface = "keybindings";
      let capturing: string | null = null; // the command id currently being rebound
      let unbind: (() => void) | null = null; // active capture listener teardown

      const title = h("h1", undefined, "Keybindings");
      const sub = h("p", undefined, "Rebind a command: Record, then press the keys. Clear disables it; Reset restores the default. Changes take effect immediately.");
      const table = h("div");
      table.dataset.rows = "";
      host.append(title, sub, table);

      // setChord writes one command's override into the shared keymap cell (immediate effect). A null
      // value RESETS (drops the override, back to the default); "" DISABLES; a chord CUSTOMIZES.
      function setChord(id: string, chord: string | null): void {
        const next = { ...deps.keymap.get() };
        if (chord === null) delete next[id];
        else next[id] = chord;
        deps.keymap.set(next);
      }

      function stopCapture(): void {
        if (unbind) { unbind(); unbind = null; }
        capturing = null;
      }

      // beginCapture listens in the CAPTURE phase so it intercepts the keystroke before the global
      // keybinding listener (which is on the bubble phase) can fire it. Escape cancels; a bare modifier
      // keeps waiting; any real chord is recorded.
      function beginCapture(id: string): void {
        stopCapture();
        capturing = id;
        const onKey = (e: KeyboardEvent): void => {
          e.preventDefault();
          e.stopImmediatePropagation();
          if (e.key === "Escape") { stopCapture(); render(); return; }
          const chord = chordFromEvent({ metaKey: e.metaKey, ctrlKey: e.ctrlKey, altKey: e.altKey, shiftKey: e.shiftKey, key: e.key, code: e.code }, mac);
          if (chord === "") return; // a lone modifier - keep waiting
          // Clear the capturing flag BEFORE writing, so the keymap-change subscription re-renders the
          // row with its new chord rather than the "Press keys..." capturing state.
          stopCapture();
          setChord(id, chord);
        };
        document.addEventListener("keydown", onKey, true);
        unbind = () => document.removeEventListener("keydown", onKey, true);
        render();
      }

      // render repaints the table from the current keymap, grouped by command group. Each row shows the
      // effective chord (or "Press keys..." while capturing, "Disabled" when silenced), a conflict
      // warning when two commands share a chord, and the Record / Clear / Reset controls.
      function render(): void {
        const user = deps.keymap.get();
        const merged = mergeKeymap(deps.defaults, user);
        const rows = keybindingRows(deps.commands, deps.defaults, user);
        table.replaceChildren();
        let lastGroup = "";
        for (const r of rows) {
          if (r.group !== lastGroup) {
            table.append(h("h2", undefined, r.group || "Commands"));
            lastGroup = r.group;
          }
          const row = h("div");
          row.dataset.krow = "";
          row.append(h("span", undefined, r.label));

          const chordCell = h("span");
          chordCell.dataset.chord = "";
          if (capturing === r.id) { chordCell.dataset.capturing = ""; chordCell.textContent = "Press keys..."; }
          else if (r.source === "disabled") { chordCell.dataset.disabled = ""; chordCell.textContent = "Disabled"; }
          else { const kbd = h("kbd", undefined, formatChord(r.chord, mac)); chordCell.append(kbd); }
          row.append(chordCell);

          // Conflict: another command bound to the same chord (never for a disabled/empty chord).
          const clash = r.chord === "" ? [] : conflicts(merged, r.chord, r.id);
          if (clash.length) {
            const names = clash.map((id) => deps.commands.find((c) => c.id === id)?.label ?? id);
            const warn = h("span", undefined, "conflicts with " + names.join(", "));
            warn.dataset.conflict = "";
            row.append(warn);
          } else {
            row.append(h("span")); // keep the grid columns aligned
          }

          const actions = h("div");
          actions.dataset.kactions = "";
          const record = h("button", undefined, capturing === r.id ? "Cancel" : "Record");
          record.type = "button";
          record.addEventListener("click", () => { if (capturing === r.id) { stopCapture(); render(); } else beginCapture(r.id); });
          const clear = h("button", undefined, "Clear");
          clear.type = "button";
          clear.addEventListener("click", () => { setChord(r.id, ""); });
          const reset = h("button", undefined, "Reset");
          reset.type = "button";
          reset.disabled = r.source === "default";
          reset.addEventListener("click", () => { setChord(r.id, null); });
          actions.append(record, clear, reset);
          row.append(actions);
          table.append(row);
        }
      }

      // Re-render on any keymap change (this editor's own writes, or another tab's), so the table always
      // reflects the live bindings.
      const unsub = deps.keymap.subscribe(() => render());
      render();

      return {
        search: noSearch,
        deactivate() { stopCapture(); unsub(); host.replaceChildren(); },
      };
    },
  };
}
