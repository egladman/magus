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
function loadCollapsed(): Set<string> { return new Set(collapsedCell.get()); }
function saveCollapsed(set: Set<string>): void { collapsedCell.set([...set]); }

// A default-collapsed card (a heavy metric family) folds itself on FIRST sight only, so
// the user's later expand sticks. `seeded` records which ids have had their default
// applied; once seeded, the collapsed set alone (which the toggle edits) is authoritative.
const seededCell = persisted<string[]>("dashboard-collapse-seeded", []);
function loadSeeded(): Set<string> { return new Set(seededCell.get()); }
function saveSeeded(set: Set<string>): void { seededCell.set([...set]); }

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

// Card builds the standard collapsible tile shell and exposes its body for the
// tile to populate. It restores its collapsed state from localStorage and persists
// toggles. The header title is a glossary deep-link when a term is given.
export class Card {
  readonly el: HTMLElement;
  readonly body: HTMLElement;
  private noteEl: HTMLElement;

  constructor(id: string, title: string, opts: CardOptions = {}) {
    const section = document.createElement("section");
    section.className = "tile";
    section.dataset.card = id;

    const head = document.createElement("div");
    head.className = "tile-head";

    const collapse = document.createElement("button");
    collapse.type = "button";
    collapse.className = "tile-collapse";
    collapse.dataset.card = id;
    collapse.setAttribute("aria-label", "Collapse card");

    const h = document.createElement("h2");
    h.className = "tile-h";
    if (opts.term) {
      // The TITLE itself is the reference link: clicking it opens the term inline in the reference
      // panel (ref-drawer.js intercepts .gloss-link). No separate labeled link beside it - that just
      // repeated the title ("Workspaces Workspace", "Sandbox File system sandbox").
      h.append(glossaryLink(opts.term, { label: title, slug: opts.slug }));
    } else {
      h.textContent = title;
    }

    this.noteEl = document.createElement("span");
    this.noteEl.className = "tile-note";
    if (opts.note) this.noteEl.textContent = opts.note;

    head.append(collapse, h, this.noteEl);

    this.body = document.createElement("div");
    this.body.className = "tile-body";

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

    if (loadCollapsed().has(id)) section.classList.add("collapsed");
    collapse.addEventListener("click", () => {
      section.classList.toggle("collapsed");
      const set = loadCollapsed();
      const collapsed = section.classList.contains("collapsed");
      if (collapsed) set.add(id); else set.delete(id);
      saveCollapsed(set);
      if (!collapsed) opts.onReveal?.();
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
