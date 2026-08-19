// targets.ts - the per-target table (NEW). One row per (project, target, spell) from
// the metrics Snapshot's target_stats: run count, latency percentiles, cache hit-rate,
// and the success/error split. Sortable by any column. The heading deep-links Target.

import type { DashboardState, TargetStatView } from "../state";
import { fmtCount, fmtDur, fmtPct } from "../state";
import { SortableTable, type Column } from "./widgets";
import { Card, type Tile } from "./card";

const columns: Column<TargetStatView>[] = [
  {
    key: "target",
    label: "Target",
    text: (r) => (r.project && r.project !== "." ? r.project + ":" : "") + r.target,
    sort: (r) => r.project + ":" + r.target,
  },
  { key: "spell", label: "Spell", text: (r) => r.spell || "-", sort: (r) => r.spell },
  {
    key: "count",
    label: "Runs",
    numeric: true,
    text: (r) => fmtCount(r.count),
    sort: (r) => r.count,
  },
  {
    key: "p50",
    label: "p50",
    numeric: true,
    text: (r) => fmtDur(r.p50Seconds),
    sort: (r) => r.p50Seconds,
  },
  {
    key: "p95",
    label: "p95",
    numeric: true,
    text: (r) => fmtDur(r.p95Seconds),
    sort: (r) => r.p95Seconds,
  },
  {
    key: "p99",
    label: "p99",
    numeric: true,
    text: (r) => fmtDur(r.p99Seconds),
    sort: (r) => r.p99Seconds,
  },
  {
    key: "hit",
    label: "Cache hit",
    numeric: true,
    text: (r) => fmtPct(r.cacheHitRate),
    sort: (r) => r.cacheHitRate,
  },
  {
    key: "success",
    label: "Success",
    numeric: true,
    text: (r) => fmtCount(r.success),
    sort: (r) => r.success,
  },
  {
    key: "errors",
    label: "Errors",
    numeric: true,
    text: (r) => fmtCount(r.errors),
    sort: (r) => r.errors,
  },
];

export function targetsTile(): Tile {
  const card = new Card("targets", "Per-target", {
    term: "Target",
    label: "targets",
    note: "latency and cache by target",
    why:
      "Which targets actually cost you time, ranked. Read the cache column next to the duration: a" +
      " slow target that always hits cache costs nothing, while a fast miss is paid every run.",
  });
  const table = new SortableTable<TargetStatView>(columns, {
    sortKey: "count",
    emptyText: "No target runs recorded yet.",
  });
  card.body.append(table.el);

  return {
    el: card.el,
    update(s: DashboardState) {
      if (!s.metrics) return;
      card.setNote(`${s.metrics.targetStats.length} targets`);
      table.setRows(s.metrics.targetStats);
    },
    destroy() {},
  };
}
