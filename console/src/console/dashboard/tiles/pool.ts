// pool.ts - the concurrency pool as an occupancy grid: one cube per capacity slot,
// filled when running, dashed cubes for tasks queued on a slot (an
// airplane-seating read). An unlimited pool (capacity 0) shows one cube per running
// slot. The heading deep-links the Pool glossary term.

import type { DashboardState, PoolView } from "../state";
import { Card, h, type Tile } from "./card";

const SLOT_CAP = 256; // soft cap on rendered cubes so a huge pool never bloats the DOM

export function poolTile(): Tile {
  const card = new Card("pool", "Pool", {
    term: "Pool",
    note: "0 / 0 slots",
    why:
      "How much concurrency is actually in use. Full with work queued means raising it will help." +
      " Mostly empty while runs drag means the graph is serialised and more slots change nothing.",
  });
  const grid = h("div", "console-dashboard-pool__grid");
  grid.setAttribute("aria-label", "Concurrency slots");
  const legend = h("div", "console-dashboard-pool__legend");
  const queuedLg = h("span", "console-dashboard-legend console-dashboard-legend--queued");
  queuedLg.hidden = true;
  const queuedCount = h("span", undefined, "0");
  queuedLg.append(document.createTextNode("queued "), queuedCount);
  legend.append(
    h("span", "console-dashboard-legend console-dashboard-legend--running", "running"),
    h("span", "console-dashboard-legend console-dashboard-legend--free", "free"),
    queuedLg,
  );
  card.body.append(grid, legend);

  function render(pool: PoolView): void {
    const cap = pool.capacity,
      used = pool.running,
      queued = pool.queued;
    card.setNote(cap > 0 ? `${used} / ${cap} slots` : `${used} running, unlimited`);
    const slots = cap > 0 ? cap : used;
    const total = Math.min(slots + queued, SLOT_CAP);

    // Lay the cubes out as a BLOCK rather than a single wrapping strip. A flex row that wraps only
    // when it runs out of width gives a different silhouette at every pool size and, at the common
    // capacities, just a thin line of cubes across the top of the panel - which reads as a progress
    // bar, not as occupancy, and wastes the panel's whole height.
    //
    // Columns are ceil(sqrt(total)), so the block stays roughly square as the pool grows: 8 slots
    // is 3x3, 16 is 4x4, 64 is 8x8. Square is what makes the airplane-seating read work - the eye
    // takes in a filled FRACTION of an area at a glance, which a line of cubes cannot convey.
    //
    // Floored at 4 columns so a tiny pool does not turn into two enormous squares, and the cubes
    // size themselves from the column count (dashboard.css), which is what makes them chunky at
    // small capacities and still fit at large ones.
    const cols = Math.max(4, Math.ceil(Math.sqrt(total)));
    grid.style.setProperty("--pool-cols", String(cols));
    // The row count is published alongside the columns so CSS can give the grid an aspect-ratio and
    // let it FILL the panel's spare height instead of sitting as a small block with dead space under
    // it. Only the stylesheet needs it; the layout here is unchanged.
    grid.style.setProperty("--pool-rows", String(Math.max(1, Math.ceil(total / cols))));

    const frag = document.createDocumentFragment();
    for (let i = 0; i < total; i++) {
      const s = h("div", "console-dashboard-pool__slot");
      if (i < used) s.dataset.state = "running";
      else if (i >= slots) s.dataset.state = "queued";
      frag.append(s);
    }
    grid.replaceChildren(frag);
    queuedLg.hidden = queued === 0;
    queuedCount.textContent = String(queued);
  }

  return {
    el: card.el,
    update(s: DashboardState) {
      if (s.status) render(s.status.pool);
    },
    destroy() {},
  };
}
