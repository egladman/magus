// cacheStats.ts - the pool-wide cache KPI strip (hits / misses / hit-rate / size /
// errors), read from the live status frame. A standard bordered, collapsible tile so it
// matches its half-width partner (the pool tile) and the sibling cache readouts, instead
// of the bare borderless strip it used to be. The heading term link is intentionally
// omitted (title alone) to avoid a "Local cache cache" stutter; the glossary hover-def
// planned for every card head will reattach the definition uniformly later.

import type { DashboardState, CacheView } from "../state";
import { fmtBytes, fmtCount, fmtPct } from "../state";
import { StatStrip } from "./widgets";
import { Card, type Tile } from "./card";

export function cacheStatsTile(): Tile {
  const card = new Card("cache-local", "Local cache", { note: "hits / misses this session" });
  const strip = new StatStrip([
    { key: "hits", label: "Cache hits", accent: "hit" },
    { key: "misses", label: "Cache misses", accent: "miss" },
    { key: "rate", label: "Hit rate", accent: "rate" },
    { key: "size", label: "Cache size", accent: "size" },
    { key: "errors", label: "Errors", accent: "err" },
  ]);
  card.body.append(strip.el);

  function render(c: CacheView): void {
    strip.set("hits", fmtCount(c.hits));
    strip.set("misses", fmtCount(c.misses));
    strip.set("rate", fmtPct(c.hitRate));
    strip.set("size", fmtBytes(c.sizeBytes));
    strip.set("errors", fmtCount(c.errors));
  }

  return {
    el: card.el,
    update(s: DashboardState) {
      if (s.status) render(s.status.cache);
    },
    destroy() {},
  };
}
