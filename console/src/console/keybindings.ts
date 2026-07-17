// keybindings.ts - the keybinding editor over the console's commands: a per-row table against a
// Persisted<Keymap> cell. keybindingRows is the pure row model; createKeybindingsEditor is the reusable
// table + capture core (the live shared cell in the modal, a draft cell in the Settings surface);
// createKeybindingsOverlay wraps it in a modal. Scope: only commands with a CONSOLE_KEYMAP default.

import {
  chordFromEvent, conflicts, formatChord, isMac, mergeKeymap, normalizeChord,
  type Command, type Keymap,
} from "./commands";
import type { Persisted } from "../lib/persist";
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
    viewBox: "0 0 24 24", width: "14", height: "14",
    fill: filled ? "currentColor" : "none", stroke: filled ? "none" : "currentColor",
    "stroke-width": "1.8", "stroke-linecap": "round", "stroke-linejoin": "round", "aria-hidden": "true",
  });
  if (kind === "record") svg.append(svgEl("circle", { cx: "12", cy: "12", r: "5" }));
  else if (kind === "clear") svg.append(svgEl("path", { d: "M6 6l12 12M18 6L6 18" }));
  else svg.append(
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
  const btn = h("button", ("pf-v6-c-button pf-m-small pf-m-plain " + variant).trim()) as HTMLButtonElement;
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
export function keybindingRows(commands: Command[], defaults: Keymap, user: Keymap): KeybindingRow[] {
  return commands.map((c) => {
    const overridden = Object.prototype.hasOwnProperty.call(user, c.id);
    const chord = normalizeChord((overridden ? user[c.id] : defaults[c.id]) ?? "");
    const source: KeybindingRow["source"] = !overridden ? "default" : chord === "" ? "disabled" : "custom";
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
// in the modal overlay and in the Settings surface's Keybindings section.
export interface KeybindingsEditor {
  readonly el: HTMLElement;
  destroy(): void;
}

export interface KeybindingsOverlay {
  readonly el: HTMLElement;
  open(): void;
  close(): void;
}

// createKeybindingsEditor builds the table + capture core into a [data-kbeditor] container, re-rendering
// on any keymap change so both embeddings stay in lockstep. The row grid is data-scoped in overrides.css.
export function createKeybindingsEditor(deps: KeybindingsDeps): KeybindingsEditor {
  const mac = isMac();
  let capturing: string | null = null; // the command id currently being rebound
  let unbind: (() => void) | null = null; // active capture listener teardown
  let unsub: (() => void) | null = null; // keymap subscription, live for the editor's lifetime

  const root = h("div");
  root.dataset.kbeditor = "";
  const desc = h("p");
  desc.dataset.kbdesc = "";
  desc.textContent = "Rebind a command: Record, then press the keys. Clear disables a binding; the revert icon restores the default.";
  const table = h("div");
  table.dataset.rows = "";
  root.append(desc, table);

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
    if (unbind) { unbind(); unbind = null; }
    capturing = null;
  }

  // beginCapture listens in the CAPTURE phase so it intercepts the keystroke before the global
  // keybinding listener fires it. Escape cancels; a bare modifier keeps waiting; any real chord records.
  function beginCapture(id: string): void {
    stopCapture();
    capturing = id;
    const onKey = (e: KeyboardEvent): void => {
      e.preventDefault();
      e.stopImmediatePropagation();
      if (e.key === "Escape") { stopCapture(); render(); return; }
      const chord = chordFromEvent({ metaKey: e.metaKey, ctrlKey: e.ctrlKey, altKey: e.altKey, shiftKey: e.shiftKey, key: e.key, code: e.code }, mac);
      if (chord === "") return; // a lone modifier - keep waiting
      // Clear the capturing flag before writing, so the subscription re-renders the new chord, not the
      // "Press keys..." state.
      stopCapture();
      setChord(id, chord);
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
      // Record starts/cancels capture; Clear disables the binding; reset (a glyph-only danger-tinted
      // control, aria-label "Reset to default") drops the custom binding back to the default.
      const record = actionButton("pf-m-secondary", capturing === r.id ? "Cancel" : "Record", rowIcon("record"));
      record.addEventListener("click", () => { if (capturing === r.id) { stopCapture(); render(); } else beginCapture(r.id); });
      const clear = actionButton("pf-m-secondary", "Clear", rowIcon("clear"));
      clear.addEventListener("click", () => { setChord(r.id, ""); });
      const reset = iconButton("", "Reset to default", rowIcon("reset"));
      reset.dataset.role = "reset";
      reset.disabled = r.source === "default";
      reset.addEventListener("click", () => { setChord(r.id, null); });
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
    destroy(): void {
      stopCapture();
      if (unsub) { unsub(); unsub = null; }
    },
  };
}

// createKeybindingsOverlay wraps the editor core in a modal overlay matching the cheat sheet. Its editor
// drives the live shared cell, so rebinds here take effect immediately (unlike the staged Settings surface).
export function createKeybindingsOverlay(deps: KeybindingsDeps): KeybindingsOverlay {
  const overlay = h("div", "pf-v6-c-backdrop");
  overlay.id = "keybindings-overlay";
  overlay.hidden = true;
  overlay.setAttribute("role", "dialog");
  overlay.setAttribute("aria-modal", "true");
  overlay.setAttribute("aria-label", "Keybindings");

  const bullseye = h("div", "pf-v6-l-bullseye");
  const box = h("div", "pf-v6-c-modal-box pf-m-md");
  box.dataset.kbBox = "";
  box.tabIndex = -1; // focusable so the open editor owns keydowns (Esc closes, chords do not leak out)
  const head = h("div", "pf-v6-c-modal-box__header");
  head.dataset.kbHead = "";
  const titleWrap = h("div", "pf-v6-c-modal-box__title");
  titleWrap.append(h("span", "pf-v6-c-modal-box__title-text", "Keybindings"));
  head.append(titleWrap);
  const closeBtn = h("button", "pf-v6-c-button pf-m-plain pf-v6-c-modal-box__close");
  closeBtn.type = "button";
  closeBtn.dataset.kbClose = "";
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.append(h("span", "pf-v6-c-button__icon", "×")); // multiplication sign - a crisp close glyph
  closeBtn.addEventListener("click", () => close());
  const bodyWrap = h("div", "pf-v6-c-modal-box__body");
  const editor = createKeybindingsEditor(deps);
  bodyWrap.append(editor.el);
  box.append(head, closeBtn, bodyWrap);
  bullseye.append(box);
  overlay.append(bullseye);

  function open(): void {
    if (!overlay.hidden) return;
    overlay.hidden = false;
    box.focus();
  }

  function close(): void {
    if (overlay.hidden) return;
    overlay.hidden = true;
  }

  // Escape closes; stopPropagation keeps keys typed while the editor owns the screen from reaching the
  // global keybinding listener (a capturing row already swallowed its own keydown upstream).
  box.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") { ev.preventDefault(); close(); }
    ev.stopPropagation();
  });
  // A click on the backdrop (outside the box) dismisses; a click inside stays.
  overlay.addEventListener("pointerdown", (ev) => { if (!box.contains(ev.target as Node)) close(); });

  return { el: overlay, open, close };
}
