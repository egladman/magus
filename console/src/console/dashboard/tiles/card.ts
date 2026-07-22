// card.ts - the Tile contract and a collapsible tile shell.
//
// Every tile owns its own DOM subtree, BUILT here in TS rather than fished out of
// dashboard.html by id. main.ts appends each tile's `el` to the panels container
// and forwards store updates to `update()`. A tile with no live data yet renders
// its empty state; `destroy()` tears down anything with a lifetime (chart
// instances, observers) so the page can be re-composed cleanly.

import type { DashboardState } from "../state";
import { glossaryLink } from "../../../lib/glossary";
import { persisted } from "../../../lib/persist";

export interface Tile {
  readonly el: HTMLElement;
  update(s: DashboardState): void;
  destroy(): void;
}

// Collapsed-card persistence: a set of card ids, stored as a JSON array in a durable
// cell. Super-basic UI state; a storage-disabled browser degrades to no persistence.
const collapsedCell = persisted<string[]>("dashboard-collapsed", []);
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
  // Fold this card on its first ever render (a dense, lower-priority metric family the
  // board keeps out of the way until asked). One-time: a later user expand persists.
  defaultCollapsed?: boolean;
  // Fired when a folded card is revealed (charts/grids need to refit while visible).
  onReveal?: () => void;
}

// Card builds the standard collapsible tile shell as a PatternFly Card and exposes its
// body for the tile to populate. It restores its collapsed state from localStorage and
// persists toggles. The header title is a glossary deep-link when a term is given.
//
// PatternFly (W3): the shell is a pf-v6-c-card with the standard __header / __header-main /
// __title / __title-text / __body parts. The collapse affordance is NOT PatternFly's own
// expandable-card toggle (that renders a pficon glyph via a webfont the console does not
// ship); instead the fold rides a data-collapsed attribute on the card and a bare caret
// button, both styled ID/data-scoped in dashboard.css - the same "component-less bit"
// escape the shell's status bar uses. The note sits in __actions (PF floats it right).
export class Card {
  readonly el: HTMLElement;
  readonly body: HTMLElement;
  private noteEl: HTMLElement;

  constructor(id: string, title: string, opts: CardOptions = {}) {
    const section = document.createElement("section");
    section.className = "pf-v6-c-card";
    section.dataset.card = id;

    const head = document.createElement("div");
    head.className = "pf-v6-c-card__header";

    // Collapse toggle: a bare caret button (data-collapse). Bare so Pico (still loaded until
    // W4) does not skin it as a filled button; dashboard.css resets it and draws the caret.
    const toggle = document.createElement("div");
    toggle.className = "pf-v6-c-card__header-toggle";
    const collapse = document.createElement("button");
    collapse.type = "button";
    collapse.dataset.collapse = "";
    collapse.setAttribute("aria-label", "Collapse card");
    const caret = document.createElement("span");
    caret.className = "pf-v6-c-card__header-toggle-icon";
    caret.dataset.caret = "";
    caret.setAttribute("aria-hidden", "true");
    collapse.append(caret);
    toggle.append(collapse);

    const headerMain = document.createElement("div");
    headerMain.className = "pf-v6-c-card__header-main";
    const titleWrap = document.createElement("div");
    titleWrap.className = "pf-v6-c-card__title";
    const h = document.createElement("h2");
    h.className = "pf-v6-c-card__title-text";
    if (opts.term) {
      // The TITLE itself is the reference link: clicking it opens the term inline in the reference
      // panel (ref-drawer.js intercepts .console-render-glosslink). No separate labeled link beside it - that just
      // repeated the title ("Workspaces Workspace", "Sandbox File system sandbox").
      h.append(glossaryLink(opts.term, { label: title, slug: opts.slug }));
    } else {
      h.textContent = title;
    }
    titleWrap.append(h);
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

    const collapsedNow = loadCollapsed().has(id);
    if (collapsedNow) section.dataset.collapsed = "";
    collapse.setAttribute("aria-expanded", collapsedNow ? "false" : "true");
    collapse.addEventListener("click", () => {
      const collapsed = section.hasAttribute("data-collapsed");
      const set = loadCollapsed();
      if (collapsed) {
        section.removeAttribute("data-collapsed");
        set.delete(id);
      } else {
        section.dataset.collapsed = "";
        set.add(id);
      }
      collapse.setAttribute("aria-expanded", collapsed ? "true" : "false");
      saveCollapsed(set);
      if (collapsed) opts.onReveal?.();
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
}

// A tiny DOM helper: create an element with a class and optional text.
// h now lives in the shared console view layer; re-exported here so the tiles that import it from
// ./card keep working unchanged.
export { h } from "../../view";
