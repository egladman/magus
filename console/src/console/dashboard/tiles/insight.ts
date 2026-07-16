// insight.ts - the Insight section: the five insight lenses as tiles, fed by the
// on-demand /api/v1/insight poll (state.insight). Consolidated in one file because
// they are one feature reading one store slice; each is still an independent Tile
// ({ el, update, destroy }) and each heading deep-links its glossary term.
//
//   - Hotspots:   churn x complexity, the prime refactoring targets (project nodes).
//   - Affinity:   co-change pairs; hidden (undeclared) coupling flagged.
//   - Ownership:  primary-author share, bus-factor-1 and stale flags.
//   - Trend:      per-project rising/cooling delta.
//   - Volatility: the volatile-targets table (replaces the old inert placeholder).
//
// The tables are the shared SortableTable, so wide tables scroll inside their own
// overflow-x container and every numeric column is exact and sortable. Boolean flags
// render as a plain word ("hidden" / "yes" / "volatile") or "-", and sort by the flag.

import type {
  DashboardState, HotspotNodeView, AffinityPairView, OwnershipRowView,
  TrendRowView, VolatilityRowView,
} from "../state";
import { fmtCount } from "../state";
import { SortableTable, type Column } from "./widgets";
import { Card, h, type Tile } from "./card";

const flag = (on: boolean, label: string): string => (on ? label : "-");
// A signed integer for the trend delta so a rising project reads "+N" and a cooling one "-N".
const signed = (n: number): string => (n > 0 ? "+" : "") + fmtCount(n);

// ---- Hotspots --------------------------------------------------------------

const hotspotCols: Column<HotspotNodeView>[] = [
  { key: "name", label: "Project", text: (r) => r.name, sort: (r) => r.name },
  { key: "churn", label: "Churn", numeric: true, text: (r) => fmtCount(r.churn), sort: (r) => r.churn },
  { key: "authors", label: "Authors", numeric: true, text: (r) => fmtCount(r.authors), sort: (r) => r.authors },
  { key: "blast", label: "Blast radius", numeric: true, text: (r) => fmtCount(r.blastRadius), sort: (r) => r.blastRadius },
  { key: "last", label: "Last commit", text: (r) => r.lastCommit, sort: (r) => r.lastCommit },
];

function hotspotsTile(): Tile {
  const card = new Card("insight-hotspots", "Hotspots", { term: "Hotspot", label: "hotspots", note: "churn x complexity" });
  const table = new SortableTable<HotspotNodeView>(hotspotCols, { sortKey: "churn", emptyText: "No hotspots in the window." });
  card.body.append(table.el);
  return {
    el: card.el,
    update(s: DashboardState) {
      if (!s.insight) return;
      card.setNote(`${s.insight.hotspots.length} projects, ${s.insight.commits} commits`);
      table.setRows(s.insight.hotspots);
    },
    destroy() {},
  };
}

// ---- Affinity --------------------------------------------------------------

const affinityCols: Column<AffinityPairView>[] = [
  { key: "a", label: "Project A", text: (r) => r.a, sort: (r) => r.a },
  { key: "b", label: "Project B", text: (r) => r.b, sort: (r) => r.b },
  { key: "count", label: "Co-changes", numeric: true, text: (r) => fmtCount(r.count), sort: (r) => r.count },
  { key: "hidden", label: "Hidden", text: (r) => flag(r.hidden, "hidden"), sort: (r) => (r.hidden ? 1 : 0) },
];

function affinityTile(): Tile {
  const card = new Card("insight-affinity", "Affinity", { term: "Affinity", label: "affinity", note: "co-change coupling" });
  const table = new SortableTable<AffinityPairView>(affinityCols, { sortKey: "count", emptyText: "No co-change pairs in the window." });
  card.body.append(table.el);
  return {
    el: card.el,
    update(s: DashboardState) {
      if (!s.insight) return;
      const hidden = s.insight.affinity.filter((p) => p.hidden).length;
      card.setNote(`${s.insight.affinity.length} pairs, ${hidden} hidden`);
      table.setRows(s.insight.affinity);
    },
    destroy() {},
  };
}

// ---- Ownership -------------------------------------------------------------

const ownershipCols: Column<OwnershipRowView>[] = [
  { key: "path", label: "Project", text: (r) => r.path, sort: (r) => r.path },
  { key: "primary", label: "Primary", text: (r) => r.primary, sort: (r) => r.primary },
  { key: "share", label: "Share", numeric: true, text: (r) => r.primaryShare + "%", sort: (r) => r.primaryShare },
  { key: "authors", label: "Authors", numeric: true, text: (r) => fmtCount(r.authors), sort: (r) => r.authors },
  { key: "bus1", label: "Bus factor 1", text: (r) => flag(r.busFactor1, "yes"), sort: (r) => (r.busFactor1 ? 1 : 0) },
  { key: "stale", label: "Stale", text: (r) => flag(r.stale, "yes"), sort: (r) => (r.stale ? 1 : 0) },
];

function ownershipTile(): Tile {
  const card = new Card("insight-ownership", "Ownership", { term: "Ownership", label: "ownership", note: "author concentration" });
  const table = new SortableTable<OwnershipRowView>(ownershipCols, { sortKey: "share", emptyText: "No ownership data in the window." });
  card.body.append(table.el);
  return {
    el: card.el,
    update(s: DashboardState) {
      if (!s.insight) return;
      const bus1 = s.insight.ownership.filter((o) => o.busFactor1).length;
      card.setNote(`${s.insight.ownership.length} projects, ${bus1} bus-factor-1`);
      table.setRows(s.insight.ownership);
    },
    destroy() {},
  };
}

// ---- Trend -----------------------------------------------------------------

const trendCols: Column<TrendRowView>[] = [
  { key: "path", label: "Project", text: (r) => r.path, sort: (r) => r.path },
  { key: "delta", label: "Delta", numeric: true, text: (r) => signed(r.delta), sort: (r) => r.delta },
  { key: "recent", label: "Recent", numeric: true, text: (r) => fmtCount(r.recent), sort: (r) => r.recent },
  { key: "earlier", label: "Earlier", numeric: true, text: (r) => fmtCount(r.earlier), sort: (r) => r.earlier },
];

function trendTile(): Tile {
  const card = new Card("insight-trend", "Trend", { term: "Trend", label: "trend", note: "rising vs cooling" });
  const table = new SortableTable<TrendRowView>(trendCols, { sortKey: "delta", emptyText: "No trend data in the window." });
  card.body.append(table.el);
  return {
    el: card.el,
    update(s: DashboardState) {
      if (!s.insight) return;
      const rising = s.insight.trend.filter((t) => t.delta > 0).length;
      card.setNote(`${s.insight.trend.length} projects, ${rising} rising`);
      table.setRows(s.insight.trend);
    },
    destroy() {},
  };
}

// ---- Volatility (replaces the inert placeholder) ---------------------------

const volatilityCols: Column<VolatilityRowView>[] = [
  { key: "label", label: "Target", text: (r) => r.label, sort: (r) => r.label },
  { key: "score", label: "Score", numeric: true, text: (r) => r.score.toFixed(3), sort: (r) => r.score },
  { key: "volatile", label: "Volatile", text: (r) => flag(r.volatile, "volatile"), sort: (r) => (r.volatile ? 1 : 0) },
  { key: "pass", label: "Pass", numeric: true, text: (r) => fmtCount(r.pass), sort: (r) => r.pass },
  { key: "fail", label: "Fail", numeric: true, text: (r) => fmtCount(r.fail), sort: (r) => r.fail },
  { key: "vcount", label: "Volatile runs", numeric: true, text: (r) => fmtCount(r.volatileCount), sort: (r) => r.volatileCount },
  { key: "samples", label: "Samples", numeric: true, text: (r) => fmtCount(r.samples), sort: (r) => r.samples },
  { key: "lastpass", label: "Last pass", text: (r) => r.lastPass, sort: (r) => r.lastPass },
];

function volatilityTile(): Tile {
  const card = new Card("insight-volatility", "Volatility", { term: "Volatility", label: "volatility", note: "run-outcome flakiness" });
  const table = new SortableTable<VolatilityRowView>(volatilityCols, { sortKey: "score", emptyText: "No run-outcome history recorded yet." });
  card.body.append(table.el);
  return {
    el: card.el,
    update(s: DashboardState) {
      if (!s.insight) return;
      const v = s.insight.volatility;
      if (!v) { card.setNote("no run history"); table.setRows([]); return; }
      const volatile = v.targets.filter((t) => t.volatile).length;
      card.setNote(`${v.targets.length} targets, ${volatile} volatile (threshold ${v.threshold.toFixed(2)})`);
      table.setRows(v.targets);
    },
    destroy() {},
  };
}

// ---- section ---------------------------------------------------------------

// insightSection builds the labeled "Insight" band (heading + a manual refresh
// button) and the five lens tiles. main.ts mounts the band and forwards store
// updates to each tile. onRefresh forces an out-of-band /api/v1/insight refetch.
export function insightSection(onRefresh: () => void): { el: HTMLElement; tiles: Tile[] } {
  const band = h("div", "console-dashboard-insight");
  const head = h("div", "console-dashboard-insight__head");
  head.append(h("h2", "console-dashboard-insight__title", "Insight"));
  const refresh = h("button", "console-dashboard-insight__refresh", "Refresh");
  refresh.type = "button";
  refresh.title = "Refetch the insight lenses now";
  refresh.addEventListener("click", () => onRefresh());
  head.append(refresh);
  band.append(head);
  band.append(h("p", "console-dashboard-insight__sub",
    "Where a codebase's attention and risk concentrate: four lenses read git history, volatility reads run outcomes."));

  const tiles = [hotspotsTile(), affinityTile(), ownershipTile(), trendTile(), volatilityTile()];
  return { el: band, tiles };
}
