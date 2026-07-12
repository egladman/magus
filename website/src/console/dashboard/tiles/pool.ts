// pool.ts - the concurrency pool as an occupancy grid: one cube per capacity slot,
// filled when running, dashed cubes for tasks queued on a slot (an
// airplane-seating read). An unlimited pool (capacity 0) shows one cube per running
// slot. The heading deep-links the Pool glossary term.

import type { DashboardState, PoolView } from "../state";
import { Card, h, type Tile } from "./card";

const SLOT_CAP = 256; // soft cap on rendered cubes so a huge pool never bloats the DOM

export function poolTile(): Tile {
  const card = new Card("pool", "Pool", { term: "Pool", note: "0 / 0 slots" });
  const grid = h("div", "slot-grid");
  grid.setAttribute("aria-label", "Concurrency slots");
  const legend = h("div", "pool-legend");
  const queuedLg = h("span", "lg lg-queued");
  queuedLg.hidden = true;
  const queuedCount = h("span", undefined, "0");
  queuedLg.append(document.createTextNode("queued "), queuedCount);
  legend.append(h("span", "lg lg-running", "running"), h("span", "lg lg-free", "free"), queuedLg);
  card.body.append(grid, legend);

  function render(pool: PoolView): void {
    const cap = pool.capacity, used = pool.running, queued = pool.queued;
    card.setNote(cap > 0 ? `${used} / ${cap} slots` : `${used} running, unlimited`);
    const slots = cap > 0 ? cap : used;
    const total = Math.min(slots + queued, SLOT_CAP);
    const frag = document.createDocumentFragment();
    for (let i = 0; i < total; i++) {
      const s = h("div");
      s.className = "slot" + (i < used ? " running" : i >= slots ? " queued" : "");
      frag.append(s);
    }
    grid.replaceChildren(frag);
    queuedLg.hidden = queued === 0;
    queuedCount.textContent = String(queued);
  }

  return {
    el: card.el,
    update(s: DashboardState) { if (s.status) render(s.status.pool); },
    destroy() {},
  };
}
