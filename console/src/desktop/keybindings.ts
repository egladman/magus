// keybindings.ts - the keybinding editor over the console's commands: a per-row table against a
// Persisted<Keymap> cell. keybindingRows is the pure row model; createKeybindingsEditor is the reusable
// table + capture core (the live shared cell in the modal, a draft cell in the Settings app);
// createKeybindingsOverlay wraps it in a modal. Scope: only commands with a CONSOLE_KEYMAP default.

import {
  chordFromEvent,
  conflicts,
  formatChord,
  isMac,
  mergeKeymap,
  normalizeSequence,
  type Command,
  type Keymap,
} from "./commands";
import type { Persisted } from "../lib/persist";
import { buildModal, keepFocus } from "./modal";
import { h } from "./view";

const SVG_NS = "http://www.w3.org/2000/svg";

// svgEl creates an SVG child element (createElementNS, per the console's no-innerHTML icon convention).
function svgEl(tag: string, attrs: Record<string, string>): SVGElement {
  const el = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  return el;
}

// rowIcon builds a 14px control glyph: a filled dot for Record, an x for Clear, a revert arrow for reset.
function rowIcon(kind: "record" | "clear" | "reset"): SVGElement {
  const filled = kind === "record";
  const svg = svgEl("svg", {
    viewBox: "0 0 24 24",
    width: "14",
    height: "14",
    fill: filled ? "currentColor" : "none",
    stroke: filled ? "none" : "currentColor",
    "stroke-width": "1.8",
    "stroke-linecap": "round",
    "stroke-linejoin": "round",
    "aria-hidden": "true",
  });
  if (kind === "record") svg.append(svgEl("circle", { cx: "12", cy: "12", r: "5" }));
  else if (kind === "clear") svg.append(svgEl("path", { d: "M6 6l12 12M18 6L6 18" }));
  else
    svg.append(
      svgEl("polyline", { points: "1 4 1 10 7 10" }),
      svgEl("path", { d: "M3.51 15a9 9 0 1 0 2.13-9.36L1 10" }),
    );
  return svg;
}

// actionButton builds one row control: a small PF button with a leading glyph and a text label.
function actionButton(variant: string, label: string, glyph: SVGElement): HTMLButtonElement {
  const btn = h("button", "pf-v6-c-button pf-m-small " + variant) as HTMLButtonElement;
  btn.type = "button";
  const icon = h("span", "pf-v6-c-button__icon pf-m-start");
  icon.append(glyph);
  btn.append(icon, h("span", "pf-v6-c-button__text", label));
  return btn;
}

// iconButton builds a glyph-only row control (no text). aria-label carries the name so it reads to
// assistive tech; used for the reset-to-default control so it does not compete with the action bar's Reset.
function iconButton(variant: string, ariaLabel: string, glyph: SVGElement): HTMLButtonElement {
  const btn = h(
    "button",
    ("pf-v6-c-button pf-m-small pf-m-plain " + variant).trim(),
  ) as HTMLButtonElement;
  btn.type = "button";
  btn.setAttribute("aria-label", ariaLabel);
  btn.title = ariaLabel;
  const icon = h("span", "pf-v6-c-button__icon");
  icon.append(glyph);
  btn.append(icon);
  return btn;
}

// One editor row: the command, its effective chord (a user override wins over the default), and where
// that chord came from - so the UI can badge a custom/disabled binding and enable Reset.
export interface KeybindingRow {
  id: string;
  label: string;
  group: string;
  chord: string;
  source: "default" | "custom" | "disabled";
}

// keybindingRows computes the editor rows: the effective chord is the user override when present
// (including "" = deliberately disabled), else the default. Pure.
export function keybindingRows(
  commands: Command[],
  defaults: Keymap,
  user: Keymap,
): KeybindingRow[] {
  return commands.map((c) => {
    const overridden = Object.prototype.hasOwnProperty.call(user, c.id);
    const chord = normalizeSequence((overridden ? user[c.id] : defaults[c.id]) ?? "");
    const source: KeybindingRow["source"] = !overridden
      ? "default"
      : chord === ""
        ? "disabled"
        : "custom";
    return { id: c.id, label: c.label, group: c.group ?? "", chord, source };
  });
}

// What the console injects: the commands to edit, their default chords (CONSOLE_KEYMAP), and the shared
// persisted keymap cell the whole console reads.
export interface KeybindingsDeps {
  commands: Command[];
  defaults: Keymap;
  keymap: Persisted<Keymap>;
}

// The reusable editor core: the [data-kbeditor] table and its capture machinery, no modal chrome. It
// subscribes to the shared keymap and re-renders live; destroy() drops the subscription. Embedded both
// in the modal overlay and in the Settings app's Keybindings section.
export interface KeybindingsEditor {
  readonly el: HTMLElement;
  // Abandons any in-progress recording and repaints. Distinct from destroy(): the editor
  // survives, so a host that hides and re-shows it (the overlay) can drop the capture
  // without losing the keymap subscription that keeps the table live.
  cancelCapture(): void;
  destroy(): void;
}

export interface KeybindingsOverlay {
  readonly el: HTMLElement;
  open(): void;
  close(): void;
}

// createKeybindingsEditor builds the table + capture core into a [data-kbeditor] container, re-rendering
// on any keymap change so both embeddings stay in lockstep. The row grid is data-scoped in overrides.css.
// How long the recorder waits after the last chord before it commits the captured sequence. A single
// chord thus saves after a brief pause; a multi-chord sequence (mod+x o) is typed in order and saved
// when you stop. Roughly matches the matcher's own sequence timeout so recording feels like using it.
const CAPTURE_COMMIT_MS = 900;

// KEYBINDINGS_HELP is what the editor says about itself, once.
export const KEYBINDINGS_HELP =
  "Rebind a command: Record, then press the keys. It can be a single shortcut or a sequence like Ctrl+X then O. Pause to save, or press Esc to cancel. Clear disables a shortcut; the revert icon restores the default.";

export interface KeybindingsEditorOptions {
  // False leaves out the editor's own help paragraph, for a host that shows KEYBINDINGS_HELP itself.
  help?: boolean;
}

export function createKeybindingsEditor(
  deps: KeybindingsDeps,
  opts: KeybindingsEditorOptions = {},
): KeybindingsEditor {
  const mac = isMac();
  let capturing: string | null = null; // the command id currently being rebound
  let captureSeq: string[] = []; // chords collected so far in the in-progress recording
  let unbind: (() => void) | null = null; // active capture listener teardown
  let commitTimer: number | null = null; // fires the pending recording after a pause
  let unsub: (() => void) | null = null; // keymap subscription, live for the editor's lifetime

  const root = h("div");
  root.dataset.kbeditor = "";
  const table = h("div");
  table.dataset.rows = "";
  // The help is one paragraph. A host that already shows it elsewhere (the modal's description) turns
  // this one off rather than stacking a second copy under it.
  if (opts.help !== false) {
    const desc = h("p", undefined, KEYBINDINGS_HELP);
    desc.dataset.kbdesc = "";
    root.append(desc);
  }
  root.append(table);

  // setChord writes one command's override into the shared keymap cell: null RESETS (drop the override),
  // "" DISABLES, a chord CUSTOMIZES.
  function setChord(id: string, chord: string | null): void {
    deps.keymap.update((prev) => {
      const next = { ...prev };
      if (chord === null) delete next[id];
      else next[id] = chord;
      return next;
    });
  }

  function stopCapture(): void {
    if (unbind) {
      unbind();
      unbind = null;
    }
    if (commitTimer !== null) {
      clearTimeout(commitTimer);
      commitTimer = null;
    }
    capturing = null;
    captureSeq = [];
  }

  // paintCapture repaints the CAPTURING row's chord cell in place (not a full re-render, which would run
  // only on a keymap change) so the sequence fills in live as chords are pressed - "Press keys..." until
  // the first, then the run so far with a trailing ellipsis ("keep going, or pause to save").
  function paintCapture(): void {
    if (capturing === null) return;
    const cell = table.querySelector<HTMLElement>(
      '[data-command="' + capturing + '"] [data-chord]',
    );
    if (!cell) return;
    cell.replaceChildren();
    cell.dataset.capturing = "";
    if (captureSeq.length === 0) {
      cell.textContent = "Press keys...";
      return;
    }
    const more = h("span", undefined, " ...");
    more.dataset.kbcapMore = "";
    cell.append(h("kbd", undefined, formatChord(captureSeq.join(" "), mac)), more);
  }

  // beginCapture listens in the CAPTURE phase so it intercepts each keystroke before the global
  // keybinding listener sees it. It ACCUMULATES chords into a sequence: Escape cancels; a bare modifier
  // keeps waiting; each real chord is appended and the commit timer restarted, so the recording saves
  // once you pause. A single-chord rebind is just a one-chord sequence.
  function beginCapture(id: string): void {
    stopCapture();
    capturing = id;
    captureSeq = [];
    const commit = (): void => {
      const seq = captureSeq.join(" ");
      stopCapture();
      if (seq !== "")
        setChord(id, seq); // writing re-renders via the subscription; empty just cancels
      else render();
    };
    const onKey = (e: KeyboardEvent): void => {
      e.preventDefault();
      e.stopImmediatePropagation();
      if (e.key === "Escape") {
        stopCapture();
        render();
        return;
      }
      const chord = chordFromEvent(
        {
          metaKey: e.metaKey,
          ctrlKey: e.ctrlKey,
          altKey: e.altKey,
          shiftKey: e.shiftKey,
          key: e.key,
          code: e.code,
        },
        mac,
      );
      if (chord === "") return; // a lone modifier - keep waiting
      captureSeq.push(chord);
      paintCapture();
      if (commitTimer !== null) clearTimeout(commitTimer);
      commitTimer = window.setTimeout(commit, CAPTURE_COMMIT_MS);
    };
    document.addEventListener("keydown", onKey, true);
    unbind = () => document.removeEventListener("keydown", onKey, true);
    render();
  }

  // render repaints the table from the current keymap, grouped by command group. Each row shows the
  // effective chord (or a capture/disabled state), any conflict warning, and its controls.
  function render(): void {
    const user = deps.keymap.get();
    const merged = mergeKeymap(deps.defaults, user);
    const rows = keybindingRows(deps.commands, deps.defaults, user);
    table.replaceChildren();
    let lastGroup = "";
    for (const r of rows) {
      if (r.group !== lastGroup) {
        table.append(h("h3", undefined, r.group || "Commands"));
        lastGroup = r.group;
      }
      const row = h("div");
      row.dataset.krow = "";
      row.dataset.command = r.id; // deep-link target for the Actions app's per-row "edit shortcut"
      row.append(h("span", undefined, r.label));

      const chordCell = h("span");
      chordCell.dataset.chord = "";
      if (capturing === r.id) {
        chordCell.dataset.capturing = "";
        chordCell.textContent = "Press keys...";
      } else if (r.source === "disabled") {
        chordCell.dataset.disabled = "";
        chordCell.textContent = "Disabled";
      } else {
        const kbd = h("kbd", undefined, formatChord(r.chord, mac));
        chordCell.append(kbd);
      }
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
      // Record starts/cancels capture; Clear disables the binding; reset (a glyph-only danger-tinted
      // control) drops the custom binding back to the default. Every row carries the same three, so each
      // is named for the command it acts on: a reader that lists the controls would otherwise hear the
      // same three names for every row.
      const record = actionButton(
        "pf-m-secondary",
        capturing === r.id ? "Cancel" : "Record",
        rowIcon("record"),
      );
      record.setAttribute(
        "aria-label",
        (capturing === r.id ? "Cancel recording the shortcut for " : "Record a shortcut for ") +
          r.label,
      );
      record.addEventListener("click", () => {
        if (capturing === r.id) {
          stopCapture();
          render();
        } else beginCapture(r.id);
      });
      const clear = actionButton("pf-m-secondary", "Clear", rowIcon("clear"));
      clear.setAttribute("aria-label", "Clear the shortcut for " + r.label);
      clear.addEventListener("click", () => {
        setChord(r.id, "");
      });
      const reset = iconButton("", "Reset to default: " + r.label, rowIcon("reset"));
      reset.dataset.role = "reset";
      reset.disabled = r.source === "default";
      reset.addEventListener("click", () => {
        setChord(r.id, null);
      });
      actions.append(record, clear, reset);
      row.append(actions);
      table.append(row);
    }
  }

  // Re-render on any keymap change (this editor's writes, or another embedding's) so the table always
  // reflects the live bindings.
  unsub = deps.keymap.subscribe(() => render());
  render();

  return {
    el: root,
    cancelCapture(): void {
      if (capturing === null) return;
      stopCapture();
      render();
    },
    destroy(): void {
      stopCapture();
      if (unsub) {
        unsub();
        unsub = null;
      }
    },
  };
}

// createKeybindingsOverlay wraps the editor core in a modal overlay matching the cheat sheet. Its editor
// drives the live shared cell, so rebinds here take effect immediately (unlike the staged Settings app).
export function createKeybindingsOverlay(deps: KeybindingsDeps): KeybindingsOverlay {
  const modal = buildModal({
    id: "keybindings-overlay",
    title: "Shortcuts",
    description: KEYBINDINGS_HELP,
    onClose: () => close(),
  });
  const { overlay, box, body: bodyWrap } = modal;
  modal.footer.remove(); // this dialog has no footer
  box.dataset.kbBox = "";
  modal.closeBtn.dataset.kbClose = "";
  const focus = keepFocus(box);
  const editor = createKeybindingsEditor(deps, { help: false });
  bodyWrap.append(editor.el);

  function open(): void {
    if (!overlay.hidden) return;
    focus.opened();
    overlay.hidden = false;
    box.focus();
  }

  function close(): void {
    if (overlay.hidden) return;
    overlay.hidden = true;
    focus.closed();
    // Hiding the overlay does NOT stop a recording: beginCapture puts a capture-phase
    // keydown listener on `document` that preventDefault()s every key, and it outlives
    // the modal it was started from. Dismissed mid-Record without this, the console eats
    // every keystroke app-wide - and then silently rebinds the command 900ms later, since
    // the commit timer is still pending. Reload was the only way out.
    editor.cancelCapture();
  }

  // Escape closes; stopPropagation keeps keys typed while the editor owns the screen from reaching the
  // global keybinding listener (a capturing row already swallowed its own keydown upstream).
  box.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") {
      ev.preventDefault();
      close();
    }
    ev.stopPropagation();
  });
  // A click on the backdrop (outside the box) dismisses; a click inside stays.
  overlay.addEventListener("pointerdown", (ev) => {
    if (!box.contains(ev.target as Node)) close();
  });

  return { el: overlay, open, close };
}
