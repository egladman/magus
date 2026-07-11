// card.ts - the Tile contract and a collapsible tile shell.
//
// Every tile owns its own DOM subtree, BUILT here in TS rather than fished out of
// dashboard.html by id. main.ts appends each tile's `el` to the panels container
// and forwards store updates to `update()`. A tile with no live data yet renders
// its empty state; `destroy()` tears down anything with a lifetime (chart
// instances, observers) so the page can be re-composed cleanly.

import type { DashboardState } from "../state";
import { glossaryLink } from "../../lib/glossary";

export interface Tile {
  readonly el: HTMLElement;
  update(s: DashboardState): void;
  destroy(): void;
}

// Collapsed-card persistence: a set of card ids in localStorage. Super-basic UI
// state, wrapped so a storage-disabled browser degrades to no persistence.
const LS_COLLAPSED = "magus-dashboard-collapsed";

function loadCollapsed(): Set<string> {
  try { return new Set(JSON.parse(localStorage.getItem(LS_COLLAPSED) || "[]") as string[]); } catch { return new Set(); }
}
function saveCollapsed(set: Set<string>): void {
  try { localStorage.setItem(LS_COLLAPSED, JSON.stringify([...set])); } catch { /* ignore */ }
}

export interface CardOptions {
  // A magus glossary term to deep-link from the heading (linked ONCE per tile).
  term?: string;
  slug?: string;
  // Visible heading label if it should differ from the term.
  label?: string;
  note?: string;
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
      h.append(document.createTextNode(title + " "));
      h.append(glossaryLink(opts.term, { label: opts.label ?? opts.term, slug: opts.slug }));
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
export function h<K extends keyof HTMLElementTagNameMap>(
  tag: K, className?: string, text?: string,
): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (className) e.className = className;
  if (text != null) e.textContent = text;
  return e;
}
