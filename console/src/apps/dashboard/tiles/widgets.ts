// widgets.ts - shared DOM builders used by several tiles: the KPI cell and the groups and strips
// built from it, and the PF description list a tile uses for its facts. Kept dumb: they build/patch
// DOM from plain view-model values, no protobuf and no store awareness.

import { h, helpGlyph } from "./card";

export type Accent = "hit" | "miss" | "rate" | "size" | "err" | "info";

// kpi builds ONE KPI cell (label + big monospace value). Every numeric readout on the board is this
// cell, so a number looks the same whether it sits in a strip of five or in a captioned group.
function kpi(label: string, accent?: Accent): { cell: HTMLElement; value: HTMLElement } {
  const cell = h("div", "console-dashboard-stat");
  if (accent) cell.dataset.accent = accent;
  cell.append(h("span", "console-dashboard-stat__key", label));
  const value = h("span", "console-dashboard-stat__value", "-");
  cell.append(value);
  return { cell, value };
}

// StatStrip is a row of KPI cells, each with a colored left rule. Cells are addressed by key so
// update() patches values in place rather than rebuilding the DOM every tick.
export class StatStrip {
  readonly el: HTMLElement;
  private cells = new Map<string, HTMLElement>();

  constructor(specs: { key: string; label: string; accent: Accent }[]) {
    this.el = h("div", "console-dashboard-statstrip");
    for (const s of specs) {
      const { cell, value } = kpi(s.label, s.accent);
      this.cells.set(s.key, value);
      this.el.append(cell);
    }
  }

  set(key: string, value: string): void {
    const c = this.cells.get(key);
    if (c) c.textContent = value;
  }
}

// MetricGrid is the same cells split into captioned groups, for the Buzz and Sandbox tiles, which
// are many small numbers rather than a series. Values are patched in place by key.
export class MetricGrid {
  readonly el: HTMLElement;
  private cells = new Map<string, HTMLElement>();

  constructor(groups: { caption?: string; items: { key: string; label: string }[] }[]) {
    this.el = h("div", "console-dashboard-stat__groups");
    for (const g of groups) {
      const section = h("div", "console-dashboard-stat__group");
      if (g.caption) section.append(h("h4", "console-dashboard-stat__caption", g.caption));
      const strip = h("div", "console-dashboard-statstrip");
      for (const it of g.items) {
        const { cell, value } = kpi(it.label);
        this.cells.set(it.key, value);
        strip.append(cell);
      }
      section.append(strip);
      this.el.append(section);
    }
  }

  set(key: string, value: string): void {
    const c = this.cells.get(key);
    if (c) c.textContent = value;
  }
}

export interface Fact {
  term: string;
  // The value: text, or nodes for a rich one (a row of charm labels).
  value: string | Node;
  // Why the term matters, on the shared "?" next to it.
  help?: string;
}

// factList builds a PF horizontal description list: field names as terms, never as <code>, and the
// values beside them. It is how a tile states what one thing IS (the broker, the server, the
// configuration), as opposed to a list of many things.
export function factList(facts: readonly Fact[], columns?: 2): HTMLElement {
  const dl = h(
    "dl",
    "pf-v6-c-description-list pf-m-horizontal pf-m-compact pf-m-fluid" +
      (columns === 2 ? " pf-m-2-col" : ""),
  );
  for (const f of facts) {
    const group = h("div", "pf-v6-c-description-list__group");
    const term = h("dt", "pf-v6-c-description-list__term");
    term.append(h("span", "pf-v6-c-description-list__text", f.term));
    if (f.help) term.append(helpGlyph(f.help, f.term));
    const desc = h("dd", "pf-v6-c-description-list__description");
    const text = h("div", "pf-v6-c-description-list__text");
    if (typeof f.value === "string") text.textContent = f.value;
    else text.append(f.value);
    desc.append(text);
    group.append(term, desc);
    dl.append(group);
  }
  return dl;
}
