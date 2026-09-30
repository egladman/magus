// commandBar.ts - the command bar (mod+k), styled after dmenu: ONE dense horizontal bar pinned to
// the very top of the viewport - a short prompt, an inline text input, and the matching commands laid
// out side by side to the input's right, extras truncated off the bar's end. Each item is the
// command's canonical TOKEN (its id minus the "console." prefix, e.g. open.logs) in monospace, with
// the query's matched characters highlighted; the selected command's plain-language label previews at
// the bar's right end. It is a pure VIEW over the commands.ts registry (listCommands) - the command
// bar owns no command state of its own. The filter (matchCommands - a fuzzy subsequence rank) is pure
// and unit-tested; the bar below is a thin DOM layer with its own local keyboard handling (Left/Right
// or Up/Down or Tab to move, Enter to run, Esc to close), so it never fights the global key listener.
//
// This is the console's ONE sanctioned fully-custom component (no PatternFly analog renders a dmenu
// bar), so its classes follow the README.md formula: console-shell-commandbar__<element>, transient
// selection state as data-selected. Styled in console.css against PF tokens so both themes work.

import { type Command, type CommandTarget, type Keymap } from "./commands";
import { h } from "./view";

// A command ranked against a query: the command, the character indices HIT in its display token (for
// highlighting), and a score (higher = better). Pure data, no DOM.
export interface CommandMatch {
  command: Command;
  hits: number[];
  score: number;
}

// displayToken is the canonical, whitespace-free token shown for a command: its id with the
// "console." namespace prefix dropped ("console.open.logs" -> "open.logs"). Already-bare ids pass
// through. This is what the operator reads and types against - a command path, not a prose sentence.
export function displayToken(id: string): string {
  return id.replace(/^console\./, "");
}

// fuzzyMatch does a subsequence (dmenu/fzf-style) match of the lowercased query against `hay`,
// returning the matched indices and a score, or null when `query` is not a subsequence. Scoring is the
// fzf/VS-Code-lite heuristic - every hit scores, with bonuses for the first char, a char right after a
// segment boundary (. - _ space), a camelCase hump, and a run consecutive with the previous hit - so
// "pfl" ranks pane.focusLeft over an incidental scatter. Greedy left-to-right (no full DP): O(n) and
// plenty for short command tokens.
function fuzzyMatch(query: string, hay: string): { hits: number[]; score: number } | null {
  const lower = hay.toLowerCase();
  const hits: number[] = [];
  let qi = 0;
  let score = 0;
  let prev = -2;
  for (let i = 0; i < lower.length && qi < query.length; i++) {
    if (lower[i] !== query[qi]) continue;
    let bonus = 1;
    const ch = hay[i];
    const pc = i > 0 ? hay[i - 1] : "";
    if (i === 0) bonus += 8;
    else if (pc === "." || pc === "-" || pc === "_" || pc === " ") bonus += 7;
    else if (ch >= "A" && ch <= "Z" && pc >= "a" && pc <= "z") bonus += 6;
    if (i === prev + 1) bonus += 4;
    score += bonus;
    hits.push(i);
    prev = i;
    qi++;
  }
  return qi === query.length ? { hits, score } : null;
}

// matchCommands ranks the commands against the query. It matches the display TOKEN plus the prose
// label (so a command is findable by either "cheatsheet.toggle" or "keyboard"), but reports only the
// hits that land on the token, since that is what the row renders and highlights. An empty query
// returns every command in registry order, unscored. Pure - the command bar's only logic.
export function matchCommands(commands: Command[], query: string): CommandMatch[] {
  const q = query.trim().toLowerCase();
  if (q === "") return commands.map((command) => ({ command, hits: [], score: 0 }));
  const out: CommandMatch[] = [];
  for (const command of commands) {
    const token = displayToken(command.id);
    const m = fuzzyMatch(q, token + " " + command.label);
    if (!m) continue;
    out.push({ command, hits: m.hits.filter((i) => i < token.length), score: m.score });
  }
  out.sort(
    (a, b) =>
      b.score - a.score || displayToken(a.command.id).length - displayToken(b.command.id).length,
  );
  return out;
}

// highlightToken builds the token text with its matched characters wrapped in a highlight span, so the
// operator sees exactly which chars the query hit - the same affordance as the docs search.
function highlightToken(token: string, hits: number[]): DocumentFragment {
  const frag = document.createDocumentFragment();
  const hit = new Set(hits);
  let i = 0;
  while (i < token.length) {
    const on = hit.has(i);
    let j = i;
    while (j < token.length && hit.has(j) === on) j++;
    const slice = token.slice(i, j);
    frag.append(
      on ? h("span", "console-shell-commandbar__hit", slice) : document.createTextNode(slice),
    );
    i = j;
  }
  return frag;
}

export interface CommandBar {
  readonly el: HTMLElement;
  open(): void;
  close(): void;
}

// What the console injects: the live command list and merged keymap (read through getters so a settings
// edit is reflected on next open), the platform accelerator for chord labels, and how to run a chosen
// command (the console dispatches + the command bar closes). keymap/mac are part of the injection contract
// even though the dmenu row shows no chords (the keybindings editor owns chord display now).
// matchTargets is matchCommands for the second stage: the same fuzzy rank, over a target's prose
// label rather than a command token. Targets have no token - a tab is named, not addressed - so the
// hits ARE the label's, and the row highlights them directly.
export function matchTargets(targets: CommandTarget[], query: string): TargetMatch[] {
  const q = query.trim().toLowerCase();
  if (q === "") return targets.map((target) => ({ target, hits: [], score: 0 }));
  const out: TargetMatch[] = [];
  for (const target of targets) {
    const m = fuzzyMatch(q, target.label);
    if (m) out.push({ target, hits: m.hits, score: m.score });
  }
  out.sort((a, b) => b.score - a.score);
  return out;
}

export interface TargetMatch {
  target: CommandTarget;
  hits: number[];
  score: number;
}

export interface CommandBarDeps {
  commands(): Command[];
  keymap(): Keymap;
  mac: boolean;
  onRun(id: string, arg?: unknown): void;
}

export function createCommandBar(deps: CommandBarDeps): CommandBar {
  // The bar: prompt | input | items. role=combobox semantics are overkill for a dmenu; a labeled
  // dialog holding a labeled input and a listbox of options keeps the ARIA honest and simple.
  const bar = h("div", "console-shell-commandbar");
  bar.id = "command-bar";
  bar.hidden = true;
  bar.setAttribute("role", "dialog");
  bar.setAttribute("aria-label", "Command Palette");

  const prompt = h("span", "console-shell-commandbar__prompt", "run");
  prompt.setAttribute("aria-hidden", "true");

  const input = h("input", "console-shell-commandbar__input");
  input.type = "text";
  input.setAttribute("aria-label", "Search actions");
  input.setAttribute("autocomplete", "off");
  input.setAttribute("spellcheck", "false");
  input.placeholder = "Run a command"; // a terse prompt; the "run" cap already names the verb

  const items = h("div", "console-shell-commandbar__items");
  items.setAttribute("role", "listbox");
  items.setAttribute("aria-label", "Matching actions");

  // A quiet preview of the SELECTED command's prose label, pinned to the bar's right end. The row
  // shows terse command tokens (open.logs); this says what the focused one does in plain words.
  const preview = h("span", "console-shell-commandbar__preview");
  preview.setAttribute("aria-hidden", "true");

  bar.append(prompt, input, items, preview);

  let filtered: CommandMatch[] = [];
  let selected = 0;
  // The second stage, when a command that takes a target has been picked but not yet run. Null is
  // stage one. The bar keeps no other mode flag: everything that differs - what gets filtered, what a
  // row shows, what Enter and Escape do - reads this.
  let stage: { command: Command; matches: TargetMatch[] } | null = null;

  // syncTyped drives the suggestion row's slide-in: while the input is empty (placeholder showing)
  // the row rests shifted to the right (a gap after the prompt); on the first keystroke data-typed
  // flips and CSS slides the tokens left into their tight resting position (a swift carriage motion,
  // instant under prefers-reduced-motion). Cleared back to empty, the row eases back out.
  function syncTyped(): void {
    if (input.value.length > 0) items.dataset.typed = "";
    else delete items.dataset.typed;
  }

  // renderItems repaints the ranked commands as a flat horizontal run of text buttons, each showing
  // the command's canonical token with the query's matched chars highlighted. dmenu shows only what
  // fits: the items container clips overflow, and markSelection scrolls the selected one into view, so
  // arrowing past the edge pages the row without any extra chrome.
  // row builds one option. Both stages render the same shape - a token with its matched characters
  // lit - because to the reader they are the same gesture: narrow a list, pick one.
  function row(
    text: string,
    hits: number[],
    i: number,
    name: string,
    onPick: () => void,
  ): HTMLElement {
    const btn = h("button", "console-shell-commandbar__item");
    btn.type = "button";
    btn.title = name;
    btn.tabIndex = -1; // the input keeps focus; the row is keyboard-driven from there
    btn.setAttribute("role", "option");
    btn.setAttribute("aria-selected", i === selected ? "true" : "false");
    btn.setAttribute("aria-label", name);
    if (i === selected) btn.dataset.selected = "";
    btn.append(highlightToken(text, hits));
    btn.addEventListener("click", onPick);
    return btn;
  }

  function renderItems(): void {
    items.replaceChildren();
    if (stage) {
      stage.matches = matchTargets(stage.command.targets?.() ?? [], input.value);
      if (selected >= stage.matches.length) selected = Math.max(0, stage.matches.length - 1);
      stage.matches.forEach((m, i) => {
        items.append(row(m.target.label, m.hits, i, m.target.label, () => run(m.target.value)));
      });
      updatePreview();
      return;
    }
    filtered = matchCommands(deps.commands(), input.value);
    if (selected >= filtered.length) selected = Math.max(0, filtered.length - 1);
    filtered.forEach((m, i) => {
      const token = displayToken(m.command.id);
      const btn = row(token, m.hits, i, token + " - " + m.command.label, () => pick(m.command));
      btn.dataset.cmd = m.command.id;
      items.append(btn);
    });
    updatePreview();
  }

  // updatePreview shows the selected command's plain-language label at the bar's right end, so the
  // terse token in the row always has a meaning visible for the focused command.
  function updatePreview(): void {
    if (stage) {
      // In stage two the label is already the row text, so repeating it would say the same thing
      // twice across the bar. The preview names the COMMAND instead, which is the thing that has
      // scrolled out of sight now that the row lists targets.
      preview.textContent = stage.command.label;
      return;
    }
    const m = filtered[selected];
    preview.textContent = m ? m.command.label : "";
  }

  // markSelection moves the inverted highlight without rebuilding the row (cheaper on arrow-key
  // navigation), keeps the selected item scrolled into the visible run, and refreshes the preview.
  function markSelection(): void {
    [...items.children].forEach((el, i) => {
      const on = i === selected;
      const b = el as HTMLElement;
      if (on) b.dataset.selected = "";
      else delete b.dataset.selected;
      b.setAttribute("aria-selected", on ? "true" : "false");
      if (on) b.scrollIntoView({ inline: "nearest", block: "nearest" });
    });
    updatePreview();
  }

  function move(delta: number): void {
    const n = stage ? stage.matches.length : filtered.length;
    if (n === 0) return;
    selected = (selected + delta + n) % n;
    markSelection();
  }

  // pick handles Enter on stage one: a command that declares targets does not run, it ASKS. The bar
  // stays open with the query cleared, so the characters that found the command are not then filtering
  // its targets. The prompt changes from the verb to the command, which is what the bar is now asking
  // about - a command with no targets is unaffected and runs as it always did.
  function pick(command: Command): void {
    if (!command.targets) {
      run(command.id);
      return;
    }
    stage = { command, matches: [] };
    prompt.textContent = command.label.toLowerCase();
    input.value = "";
    input.placeholder = "Pick a target";
    selected = 0;
    syncTyped();
    renderItems();
  }

  // run closes and dispatches. In stage two the id is the STAGED command's and the argument is the
  // chosen target's value; in stage one there is no argument and the id is the command's own.
  function run(idOrValue: string): void {
    const staged = stage;
    close();
    if (staged) deps.onRun(staged.command.id, idOrValue);
    else deps.onRun(idOrValue);
  }

  function open(): void {
    bar.hidden = false;
    reset();
    renderItems();
    input.focus();
  }

  // reset returns the bar to stage one. Called on open and when Escape backs out of a target list, so
  // a bar reopened after picking a target never resumes mid-question.
  function reset(): void {
    stage = null;
    prompt.textContent = "run";
    input.value = "";
    input.placeholder = "Run a command";
    selected = 0;
    syncTyped(); // reset to the pre-typing resting position (row shifted right, gap after the prompt)
  }

  function close(): void {
    bar.hidden = true;
    reset();
  }

  input.addEventListener("input", () => {
    selected = 0;
    syncTyped();
    renderItems();
  });
  // Local keyboard handling, dmenu-style: Left/Right walk the horizontal row (Up/Down alias to them,
  // Tab/Shift+Tab too), Enter runs the selection, Esc closes. Tab is captured so focus never leaves
  // the bar while it is open. Stop propagation so the global keybinding listener does not also act on
  // these while the command bar owns focus.
  input.addEventListener("keydown", (ev) => {
    if (ev.key === "ArrowRight" || ev.key === "ArrowDown" || (ev.key === "Tab" && !ev.shiftKey)) {
      ev.preventDefault();
      move(1);
    } else if (
      ev.key === "ArrowLeft" ||
      ev.key === "ArrowUp" ||
      (ev.key === "Tab" && ev.shiftKey)
    ) {
      ev.preventDefault();
      move(-1);
    } else if (ev.key === "Enter") {
      ev.preventDefault();
      if (stage) {
        const m = stage.matches[selected];
        if (m) run(m.target.value);
      } else if (filtered[selected]) pick(filtered[selected].command);
    } else if (ev.key === "Escape") {
      ev.preventDefault();
      // Escape backs OUT of a target list rather than dismissing outright: having answered "which
      // command" you should be able to take that answer back without losing the bar and starting over.
      // A second Escape, now at stage one, closes.
      if (stage) {
        reset();
        renderItems();
      } else close();
    }
    ev.stopPropagation();
  });
  // A click anywhere off the bar dismisses it (dmenu drops on focus loss); a click on the bar stays.
  document.addEventListener("pointerdown", (ev) => {
    if (!bar.hidden && !bar.contains(ev.target as Node)) close();
  });

  return { el: bar, open, close };
}
