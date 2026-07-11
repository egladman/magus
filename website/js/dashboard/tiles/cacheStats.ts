// cacheStats.ts - the pool-wide cache KPI strip (hits / misses / hit-rate / size /
// errors), read from the live status frame. A bare stat strip, not a bordered tile,
// so it stays a dense full-width readout. The Cache term is deep-linked via a small
// caption row (the strip has no tile heading of its own).

import type { DashboardState, CacheView } from "../state";
import { fmtBytes, fmtCount, fmtPct } from "../state";
import { glossaryLink } from "../../lib/glossary";
import { StatStrip } from "./widgets";
import { h, type Tile } from "./card";

export function cacheStatsTile(): Tile {
  const root = h("section", "stat-section");
  const cap = h("p", "stat-caption");
  cap.append(document.createTextNode("Local "));
  cap.append(glossaryLink("Cache", { label: "cache" }));
  cap.append(document.createTextNode(" activity"));
  const strip = new StatStrip([
    { key: "hits", label: "Cache hits", accent: "hit" },
    { key: "misses", label: "Cache misses", accent: "miss" },
    { key: "rate", label: "Hit rate", accent: "rate" },
    { key: "size", label: "Cache size", accent: "size" },
    { key: "errors", label: "Errors", accent: "err" },
  ]);
  root.append(cap, strip.el);

  function render(c: CacheView): void {
    strip.set("hits", fmtCount(c.hits));
    strip.set("misses", fmtCount(c.misses));
    strip.set("rate", fmtPct(c.hitRate));
    strip.set("size", fmtBytes(c.sizeBytes));
    strip.set("errors", fmtCount(c.errors));
  }

  return {
    el: root,
    update(s: DashboardState) { if (s.status) render(s.status.cache); },
    destroy() {},
  };
}
