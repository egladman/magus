// card.ts - the Tile contract and a collapsible tile shell.
//
// Every tile owns its own DOM subtree, BUILT here in TS rather than fished out of
// dashboard.html by id. main.ts appends each tile's `el` to the panels container
// and forwards store updates to `update()`. A tile with no live data yet renders
// its empty state; `destroy()` tears down anything with a lifetime (chart
// instances, observers) so the page can be re-composed cleanly.

import type { DashboardState } from "../state";
import { glossaryLink } from "../../../lib/glossary";
import { attachHelpPopover, createHelpButton } from "../../../ui/help-popover";
import { persisted } from "../../../lib/persist";
import { collapsedCardsCell } from "../../../desktop/layoutPrefs";
import { scrollRegion } from "../scroll";

export interface Tile {
  readonly el: HTMLElement;
  update(s: DashboardState): void;
  setVisible?(visible: boolean): void;
  destroy(): void;
}

// Collapsed-card persistence: a set of card ids, stored as a JSON array in a durable
// cell. Super-basic UI state; a storage-disabled browser degrades to no persistence.
const collapsedCell = collapsedCardsCell;
function loadCollapsed(): Set<string> {
  return new Set(collapsedCell.get());
}
function saveCollapsed(set: Set<string>): void {
  collapsedCell.set([...set]);
}

// A default-collapsed card (a heavy metric family) folds itself on FIRST sight only, so
// the user's later expand sticks. `seeded` records which ids have had their default
// applied; once seeded, the collapsed set alone (which the toggle edits) is authoritative.
const seededCell = persisted<string[]>("dashboard-collapse-seeded", []);
function loadSeeded(): Set<string> {
  return new Set(seededCell.get());
}
function saveSeeded(set: Set<string>): void {
  seededCell.set([...set]);
}

export interface CardOptions {
  // A magus glossary term to deep-link from the heading (linked ONCE per tile).
  term?: string;
  slug?: string;
  // Visible heading label if it should differ from the term.
  label?: string;
  note?: string;
  // WHY this panel is worth reading - what decision it informs, and what a bad number here would
  // mean. Rendered as the shared "?" affordance beside the heading (ui/help-popover.ts).
  //
  // Deliberately distinct from `term`, which links the glossary's definition of WHAT something is.
  // Write it as a consequence, not a restatement: "a low rate here means most work is rebuilding
  // from scratch" tells the reader something; "the ratio of hits to total lookups" does not.
  why?: string;
  // Fold this card on its first ever render (a dense, lower-priority metric family the
  // board keeps out of the way until asked). One-time: a later user expand persists.
  defaultCollapsed?: boolean;
  // Fired when a folded card is revealed (charts/grids need to refit while visible).
  onReveal?: () => void;
}

// One disposer per live help popover, found again by the trigger. The boards rebuild their cards on
// every reopen, so a popover that nobody disposes keeps its open state, and its shared document
// listeners, alive after its trigger has left the page.
const helpDisposers = new WeakMap<HTMLElement, () => void>();

// helpGlyph builds the shared "?" affordance and wires its popover.
//
// Exported because not every panel is a Card: the utilization tile hand-builds its own PatternFly
// shell, and the attention hero is deliberately not a card at all. The disposer is kept, so
// disposeHelpGlyphs can close and unwire every one under a container before it is discarded.
export function helpGlyph(why: string, label: string): HTMLElement {
  const help = createHelpButton("Why " + label + " matters");
  helpDisposers.set(
    help,
    attachHelpPopover(help, { text: why, label: "Why " + label + " matters" }),
  );
  return help;
}

// disposeHelpGlyphs closes and unwires every help popover under root. Call it before the container
// is emptied or removed.
export function disposeHelpGlyphs(root: ParentNode): void {
  for (const trigger of root.querySelectorAll<HTMLElement>("[data-help-trigger]")) {
    const dispose = helpDisposers.get(trigger);
    if (!dispose) continue;
    helpDisposers.delete(trigger);
    dispose();
  }
}

// prose fills el with text in which a command in `backticks` becomes <code>: the spans between the
// ticks are text, the spans inside them are code. textContent printed the ticks themselves, so a
// mark meant to say "this is something you type" read as stray punctuation. One inline construct
// only; a markdown parser here would be a second one beside notes/markdown.ts.
export function prose(el: HTMLElement, text: string): void {
  el.replaceChildren();
  // Odd indices are the spans that sat between a pair of ticks.
  text.split("`").forEach((part, i) => {
    if (part === "") return;
    if (i % 2 === 0) {
      el.append(document.createTextNode(part));
      return;
    }
    const code = document.createElement("code");
    code.className = "console-dashboard-code";
    code.textContent = part;
    el.append(code);
  });
}

// emptyState is the one "nothing to show here" the board uses: a PF extra-small empty state with a
// single sentence. A tile that has nothing yet keeps its card and says so, instead of vanishing and
// leaving the grid a different shape each poll. Identifiers go in `backticks`.
export function emptyState(text: string): HTMLElement {
  const root = document.createElement("div");
  root.className = "pf-v6-c-empty-state pf-m-xs";
  root.dataset.emptyState = "";
  const content = document.createElement("div");
  content.className = "pf-v6-c-empty-state__content";
  const body = document.createElement("div");
  body.className = "pf-v6-c-empty-state__body";
  prose(body, text);
  content.append(body);
  root.append(content);
  return root;
}

export { scrollRegion };

// tableScroller names the scroll container SortableTable wraps its table in.
export function tableScroller(table: HTMLElement, label: string): void {
  const wrap = table.querySelector<HTMLElement>(".console-table__wrap");
  if (wrap) scrollRegion(wrap, label);
}

// countBadge is the header's count: a PF read Badge carrying the number, with the unit it counts
// as hidden text so "3" is not announced alone. Replace the card's note slot with it.
export function countBadge(unit: string): { el: HTMLElement; set(n: number): void } {
  const el = document.createElement("span");
  el.className = "pf-v6-c-badge pf-m-read";
  const value = document.createTextNode("0");
  const hidden = document.createElement("span");
  hidden.className = "pf-v6-screen-reader";
  hidden.textContent = " " + unit;
  el.append(value, hidden);
  return {
    el,
    set(n) {
      value.data = String(n);
    },
  };
}

// SVG_NS and the toggle's chevron. PF's own toggle icon is a webfont glyph the console does not
// ship, so the same angle-right is drawn inline; PF's pf-m-expanded rule turns it.
const SVG_NS = "http://www.w3.org/2000/svg";
function chevron(): SVGElement {
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("class", "pf-v6-svg");
  svg.setAttribute("viewBox", "0 0 256 512");
  svg.setAttribute("fill", "currentColor");
  svg.setAttribute("width", "1em");
  svg.setAttribute("height", "1em");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS(SVG_NS, "path");
  path.setAttribute(
    "d",
    "M224.3 273l-136 136c-9.4 9.4-24.6 9.4-33.9 0l-22.6-22.6c-9.4-9.4-9.4-24.6 0-33.9l96.4-96.4-96.4-96.4c-9.4-9.4-9.4-24.6 0-33.9L54.3 103c9.4-9.4 24.6-9.4 33.9 0l136 136c9.5 9.4 9.5 24.6.1 34z",
  );
  svg.append(path);
  return svg;
}

let titleSeq = 0;

// Card builds the standard collapsible tile shell as a PatternFly compact Card and exposes its
// body for the tile to populate. It restores its collapsed state from storage and persists toggles.
// The header title is a glossary deep-link when a term is given, and the card's accessible name.
//
// The fold rides a data-collapsed attribute on the card (dashboard.css hides the body); the toggle is
// PF's own plain button with the Card's toggle icon, named by the title it folds. The note sits in
// __actions (PF floats it right).
export class Card {
  readonly el: HTMLElement;
  readonly body: HTMLElement;
  private noteEl: HTMLElement;
  private empty: HTMLElement | null = null;

  constructor(id: string, title: string, opts: CardOptions = {}) {
    const section = document.createElement("section");
    section.className = "pf-v6-c-card pf-m-compact pf-m-expanded";
    section.dataset.card = id;
    const titleId = "console-dashboard-card-title-" + ++titleSeq;
    section.setAttribute("aria-labelledby", titleId);

    const head = document.createElement("div");
    head.className = "pf-v6-c-card__header";

    const toggle = document.createElement("div");
    toggle.className = "pf-v6-c-card__header-toggle";
    const collapse = document.createElement("button");
    collapse.type = "button";
    collapse.className = "pf-v6-c-button pf-m-plain";
    collapse.dataset.collapse = "";
    collapse.setAttribute("aria-labelledby", titleId);
    const caret = document.createElement("span");
    caret.className = "pf-v6-c-card__header-toggle-icon";
    caret.append(chevron());
    collapse.append(caret);
    toggle.append(collapse);

    const headerMain = document.createElement("div");
    headerMain.className = "pf-v6-c-card__header-main";
    const titleWrap = document.createElement("div");
    titleWrap.className = "pf-v6-c-card__title";
    const h = document.createElement("h3");
    h.className = "pf-v6-c-card__title-text";
    h.id = titleId;
    if (opts.term) {
      // The TITLE itself is the reference link: clicking it opens the term's glossary entry.
      // No separate labeled link beside it - that just repeated the title ("Workspaces Workspace").
      h.append(glossaryLink(opts.term, { label: title, slug: opts.slug }));
    } else {
      h.textContent = title;
    }
    titleWrap.append(h);
    // The "?" affordance, when the tile says why it matters: a popover rather than a title=
    // tooltip, which is invisible on touch and unreachable on a shared screen.
    if (opts.why) titleWrap.append(helpGlyph(opts.why, opts.label || title));
    headerMain.append(titleWrap);

    const actions = document.createElement("div");
    actions.className = "pf-v6-c-card__actions";
    this.noteEl = document.createElement("span");
    this.noteEl.className = "console-dashboard-tile__note";
    if (opts.note) this.noteEl.textContent = opts.note;
    actions.append(this.noteEl);

    head.append(toggle, headerMain, actions);

    this.body = document.createElement("div");
    this.body.className = "pf-v6-c-card__body";

    section.append(head, this.body);
    this.el = section;

    // Seed the default-collapsed state exactly once per id, so the fold is only imposed
    // the first time the user meets this card; after that their own toggle wins.
    if (opts.defaultCollapsed) {
      const seeded = loadSeeded();
      if (!seeded.has(id)) {
        const set = loadCollapsed();
        set.add(id);
        saveCollapsed(set);
        seeded.add(id);
        saveSeeded(seeded);
      }
    }

    const fold = (folded: boolean): void => {
      section.toggleAttribute("data-collapsed", folded);
      section.classList.toggle("pf-m-expanded", !folded);
      collapse.setAttribute("aria-expanded", folded ? "false" : "true");
    };
    fold(loadCollapsed().has(id));
    collapse.addEventListener("click", () => {
      const folded = !section.hasAttribute("data-collapsed");
      const set = loadCollapsed();
      if (folded) set.add(id);
      else set.delete(id);
      fold(folded);
      saveCollapsed(set);
      if (!folded) opts.onReveal?.();
    });
  }

  setNote(text: string): void {
    this.noteEl.textContent = text;
  }

  // noteNode exposes the header note element for a tile that needs a RICH note (child
  // nodes, a swatch legend, a glossary link) or wants to swap the slot for a differently
  // styled chip. Prefer setNote for a plain string; reach for this only when the note is
  // more than text. Returned so callers do not have to querySelector past the card shell.
  noteNode(): HTMLElement {
    return this.noteEl;
  }

  // setEmpty swaps the body for the shared empty state while the tile has nothing to show, and
  // marks the card [data-empty] so the Big Picture rotator skips it. null puts the content back.
  // The text is only rebuilt when it changes, so a status frame per second leaves it alone.
  setEmpty(text: string | null): void {
    if (text === null) {
      this.el.removeAttribute("data-empty");
      this.empty?.remove();
      this.empty = null;
      return;
    }
    this.el.dataset.empty = "";
    if (this.empty && this.empty.textContent === text.replaceAll("`", "")) return;
    const next = emptyState(text);
    if (this.empty) this.empty.replaceWith(next);
    else this.body.prepend(next);
    this.empty = next;
  }
}

// A tiny DOM helper: create an element with a class and optional text.
// h now lives in the shared console view layer; re-exported here so the tiles that import it from
// ./card keep working unchanged.
export { h } from "../../../desktop/view";
