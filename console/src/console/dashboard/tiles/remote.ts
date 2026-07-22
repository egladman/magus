// remote.ts - the remote cache panel: outcome tallies plus transfer latency and
// volume, from the metrics Snapshot's Remote family. Already on the wire; this tile
// renders it. The heading deep-links the Remote cache glossary term.

import type { DashboardState, RemoteView } from "../state";
import { fmtBytes, fmtCount, fmtDur, fmtPct } from "../state";
import { StatStrip } from "./widgets";
import { Card, type Tile } from "./card";

export function remoteTile(): Tile {
  const card = new Card("remote", "Remote cache", {
    term: "Remote cache",
    label: "remote cache",
    note: "get / put over the network",
  });
  const strip = new StatStrip([
    { key: "hits", label: "Hits", accent: "hit" },
    { key: "misses", label: "Misses", accent: "miss" },
    { key: "errors", label: "Errors", accent: "err" },
    { key: "rate", label: "Hit rate", accent: "rate" },
    { key: "p50", label: "Transfer p50", accent: "size" },
    { key: "p95", label: "Transfer p95", accent: "size" },
    { key: "io", label: "IO ops", accent: "info" },
    { key: "bytes", label: "Bytes moved", accent: "info" },
  ]);
  card.body.append(strip.el);

  function render(r: RemoteView): void {
    strip.set("hits", fmtCount(r.hits));
    strip.set("misses", fmtCount(r.misses));
    strip.set("errors", fmtCount(r.errors));
    strip.set("rate", fmtPct(r.hitRate));
    strip.set("p50", fmtDur(r.durationP50));
    strip.set("p95", fmtDur(r.durationP95));
    strip.set("io", fmtCount(r.ioCount));
    strip.set("bytes", fmtBytes(r.bytesTotal));
  }

  return {
    el: card.el,
    update(s: DashboardState) {
      const r = s.metrics?.remote;
      card.el.hidden = !r;
      if (r) render(r);
    },
    destroy() {},
  };
}
