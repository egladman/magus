// pool.ts - the concurrency pool as an occupancy grid: one cube per capacity slot,
// filled when in use, dashed cubes for tasks queued waiting on a slot (an
// airplane-seating read). An unlimited pool (capacity 0) shows one cube per in-use
// slot. The heading deep-links the Pool glossary term.

import type { DashboardState, PoolView } from "../state";
import { Card, h, type Tile } from "./card";

const SLOT_CAP = 256; // soft cap on rendered cubes so a huge pool never bloats the DOM

export function poolTile(): Tile {
  const card = new Card("pool", "Pool", { term: "Pool", note: "0 / 0 slots" });
  const grid = h("div", "slot-grid");
  grid.setAttribute("aria-label", "Concurrency slots");
  const legend = h("div", "pool-legend");
  const waitLg = h("span", "lg lg-wait");
  waitLg.hidden = true;
  const waitCount = h("span", undefined, "0");
  waitLg.append(document.createTextNode("waiting "), waitCount);
  legend.append(h("span", "lg lg-busy", "in use"), h("span", "lg lg-free", "free"), waitLg);
  card.body.append(grid, legend);

  function render(pool: PoolView): void {
    const cap = pool.capacity, used = pool.inUse, waiting = pool.waiting;
    card.setNote(cap > 0 ? `${used} / ${cap} slots` : `${used} in use, unlimited`);
    const slots = cap > 0 ? cap : used;
    const total = Math.min(slots + waiting, SLOT_CAP);
    const frag = document.createDocumentFragment();
    for (let i = 0; i < total; i++) {
      const s = h("div");
      s.className = "slot" + (i < used ? " busy" : i >= slots ? " waiting" : "");
      frag.append(s);
    }
    grid.replaceChildren(frag);
    waitLg.hidden = waiting === 0;
    waitCount.textContent = String(waiting);
  }

  return {
    el: card.el,
    update(s: DashboardState) { if (s.status) render(s.status.pool); },
    destroy() {},
  };
}
