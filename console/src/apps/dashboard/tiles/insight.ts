// insight.ts - the Insight section: the five insight lenses as tiles, fed by the
// on-demand /api/v1/insight poll (state.insight). Consolidated in one file because
// they are one feature reading one store slice; each is still an independent Tile
// ({ el, update, destroy }) and each heading deep-links its glossary term.
//
//   - Hotspots:   churn x complexity, the prime refactoring targets (project nodes).
//   - Hotspot files: the same lens at the granularity you can act on, plus a move count.
//     Churn follows a file's LINEAGE (project.FileHotspots folds every name it went by onto
//     the one it ends under), so a renamed file ranks once with its whole history rather
//     than several times with a slice each, and deleted files are absent - they are history,
//     not something to go fix.
//   - Affinity:   co-change pairs; hidden (undeclared) coupling flagged.
//   - Ownership:  primary-author share, bus-factor-1 and stale flags.
//   - Trend:      per-project rising/cooling delta.
//   - Volatility: the volatile-targets table (replaces the old inert placeholder).
//
// The tables are the shared SortableTable, so wide tables scroll inside their own
// overflow-x container and every numeric column is exact and sortable. Boolean flags
// render as a plain word ("hidden" / "yes" / "volatile") or "-", and sort by the flag.

import type {
  DashboardState,
  FileHotspotView,
  HotspotNodeView,
  AffinityPairView,
  OwnershipRowView,
  TrendRowView,
  VolatilityRowView,
} from "../state";
import { fmtCount } from "../state";
import { SortableTable, type Column } from "./widgets";
import { Card, h, helpGlyph, type Tile } from "./card";
import { REFRESH, svgGlyph } from "../../../ui/glyph";

const flag = (on: boolean, label: string): string => (on ? label : "-");
// A signed integer for the trend delta so a rising project reads "+N" and a cooling one "-N".
const signed = (n: number): string => (n > 0 ? "+" : "") + fmtCount(n);

// ---- Hotspots --------------------------------------------------------------

const hotspotCols: Column<HotspotNodeView>[] = [
  { key: "name", label: "Project", text: (r) => r.name, sort: (r) => r.name },
  {
    key: "churn",
    label: "Churn",
    numeric: true,
    text: (r) => fmtCount(r.churn),
    sort: (r) => r.churn,
  },
  {
    key: "authors",
    label: "Authors",
    numeric: true,
    text: (r) => fmtCount(r.authors),
    sort: (r) => r.authors,
  },
  {
    key: "blast",
    label: "Blast radius",
    numeric: true,
    text: (r) => fmtCount(r.blastRadius),
    sort: (r) => r.blastRadius,
  },
  { key: "last", label: "Last commit", text: (r) => r.lastCommit, sort: (r) => r.lastCommit },
];

// The per-FILE ranking. Score leads because it is the number the CLI ranks by and it is sent
// rather than derived, so this table and `magus insight` agree even if the weighting changes.
// Moves earns a column of its own: a file that keeps changing address is churning
// architecturally, and that is invisible in a commit count.
const hotspotFileCols: Column<FileHotspotView>[] = [
  { key: "path", label: "File", text: (r) => r.path, sort: (r) => r.path },
  {
    key: "score",
    label: "Score",
    numeric: true,
    text: (r) => fmtCount(r.score),
    sort: (r) => r.score,
  },
  {
    key: "commits",
    label: "Edits",
    numeric: true,
    text: (r) => fmtCount(r.commits),
    sort: (r) => r.commits,
  },
  {
    key: "complexity",
    label: "Complexity",
    numeric: true,
    text: (r) => fmtCount(r.complexity),
    sort: (r) => r.complexity,
  },
  {
    key: "moves",
    label: "Moves",
    numeric: true,
    // A dash, not a 0: most files never move, and a column of zeroes would draw the eye to
    // the ordinary case instead of the handful that kept changing address.
    text: (r) => (r.moves > 0 ? fmtCount(r.moves) : "-"),
    sort: (r) => r.moves,
  },
  {
    key: "authors",
    label: "Authors",
    numeric: true,
    text: (r) => fmtCount(r.authors),
    sort: (r) => r.authors,
  },
  { key: "last", label: "Last commit", text: (r) => r.lastCommit, sort: (r) => r.lastCommit },
];

function hotspotFilesTile(): Tile {
  const card = new Card("insight-hotspot-files", "Hotspot files", {
    term: "Hotspot",
    label: "hotspots",
    note: "churn x complexity, per file",
    why:
      "The files that change most and are hardest to change. A file high on both is where the next" +
      " defect is most likely to land, and the best candidate for splitting before it gets worse.",
  });
  const table = new SortableTable<FileHotspotView>(hotspotFileCols, {
    sortKey: "score",
    emptyText: "No file hotspots in the window.",
  });
  card.body.append(table.el);
  return {
    el: card.el,
    update(s: DashboardState) {
      if (!s.insight) {
        table.setUnresolved(s.insightNote);
        card.setNote("");
        return;
      }
      table.setUnresolved(null);
      const moved = s.insight.hotspotFiles.filter((f) => f.moves > 0).length;
      card.setNote(
        `${s.insight.hotspotFiles.length} files, ${s.insight.commits} commits` +
          (moved > 0 ? `, ${moved} moved` : ""),
      );
      table.setRows(s.insight.hotspotFiles);
    },
    destroy() {},
  };
}

function hotspotsTile(): Tile {
  const card = new Card("insight-hotspots", "Hotspots", {
    term: "Hotspot",
    label: "hotspots",
    note: "churn x complexity",
    why:
      "The same reading as Hotspot files, rolled up to whatever the graph knows a node to be." +
      " Use it to find which PROJECT is carrying the risk before drilling into which file.",
  });
  const table = new SortableTable<HotspotNodeView>(hotspotCols, {
    sortKey: "churn",
    emptyText: "No hotspots in the window.",
  });
  card.body.append(table.el);
  return {
    el: card.el,
    update(s: DashboardState) {
      if (!s.insight) {
        table.setUnresolved(s.insightNote);
        card.setNote("");
        return;
      }
      table.setUnresolved(null);
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
  {
    key: "count",
    label: "Co-changes",
    numeric: true,
    text: (r) => fmtCount(r.count),
    sort: (r) => r.count,
  },
  {
    key: "hidden",
    label: "Hidden",
    text: (r) => flag(r.hidden, "hidden"),
    sort: (r) => (r.hidden ? 1 : 0),
  },
];

function affinityTile(): Tile {
  const card = new Card("insight-affinity", "Affinity", {
    term: "Affinity",
    label: "affinity",
    note: "co-change coupling",
    why:
      "Files that keep getting committed together. A high pair with no import between them is" +
      " coupling the code does not declare, so an edit to one silently needs an edit to the other.",
  });
  const table = new SortableTable<AffinityPairView>(affinityCols, {
    sortKey: "count",
    emptyText: "No co-change pairs in the window.",
  });
  card.body.append(table.el);
  return {
    el: card.el,
    update(s: DashboardState) {
      if (!s.insight) {
        table.setUnresolved(s.insightNote);
        card.setNote("");
        return;
      }
      table.setUnresolved(null);
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
  {
    key: "share",
    label: "Share",
    numeric: true,
    text: (r) => r.primaryShare + "%",
    sort: (r) => r.primaryShare,
  },
  {
    key: "authors",
    label: "Authors",
    numeric: true,
    text: (r) => fmtCount(r.authors),
    sort: (r) => r.authors,
  },
  {
    key: "bus1",
    label: "Bus factor 1",
    text: (r) => flag(r.busFactor1, "yes"),
    sort: (r) => (r.busFactor1 ? 1 : 0),
  },
  {
    key: "stale",
    label: "Stale",
    text: (r) => flag(r.stale, "yes"),
    sort: (r) => (r.stale ? 1 : 0),
  },
];

function ownershipTile(): Tile {
  const card = new Card("insight-ownership", "Ownership", {
    term: "Ownership",
    label: "ownership",
    note: "author concentration",
    why:
      "Who has actually been editing each project, and how concentrated that is. Bus factor 1 means" +
      " one person holds it: that is who to ask, and the first place to spread knowledge.",
  });
  const table = new SortableTable<OwnershipRowView>(ownershipCols, {
    sortKey: "share",
    emptyText: "No ownership data in the window.",
  });
  card.body.append(table.el);
  return {
    el: card.el,
    update(s: DashboardState) {
      if (!s.insight) {
        table.setUnresolved(s.insightNote);
        card.setNote("");
        return;
      }
      table.setUnresolved(null);
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
  {
    key: "delta",
    label: "Delta",
    numeric: true,
    text: (r) => signed(r.delta),
    sort: (r) => r.delta,
  },
  {
    key: "recent",
    label: "Recent",
    numeric: true,
    text: (r) => fmtCount(r.recent),
    sort: (r) => r.recent,
  },
  {
    key: "earlier",
    label: "Earlier",
    numeric: true,
    text: (r) => fmtCount(r.earlier),
    sort: (r) => r.earlier,
  },
];

function trendTile(): Tile {
  const card = new Card("insight-trend", "Trend", {
    term: "Trend",
    label: "trend",
    note: "rising vs cooling",
    why:
      "Where the work moved recently, measured as this window's commits against the one before it." +
      " Rising says attention is arriving; cooling says a project is being left alone, not that it is done.",
  });
  const table = new SortableTable<TrendRowView>(trendCols, {
    sortKey: "delta",
    emptyText: "No trend data in the window.",
  });
  card.body.append(table.el);
  return {
    el: card.el,
    update(s: DashboardState) {
      if (!s.insight) {
        table.setUnresolved(s.insightNote);
        card.setNote("");
        return;
      }
      table.setUnresolved(null);
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
  {
    key: "score",
    label: "Score",
    numeric: true,
    text: (r) => r.score.toFixed(3),
    sort: (r) => r.score,
  },
  {
    key: "volatile",
    label: "Volatile",
    text: (r) => flag(r.volatile, "volatile"),
    sort: (r) => (r.volatile ? 1 : 0),
  },
  { key: "pass", label: "Pass", numeric: true, text: (r) => fmtCount(r.pass), sort: (r) => r.pass },
  { key: "fail", label: "Fail", numeric: true, text: (r) => fmtCount(r.fail), sort: (r) => r.fail },
  {
    key: "vcount",
    label: "Volatile runs",
    numeric: true,
    text: (r) => fmtCount(r.volatileCount),
    sort: (r) => r.volatileCount,
  },
  {
    key: "samples",
    label: "Samples",
    numeric: true,
    text: (r) => fmtCount(r.samples),
    sort: (r) => r.samples,
  },
  { key: "lastpass", label: "Last pass", text: (r) => r.lastPass, sort: (r) => r.lastPass },
];

function volatilityTile(): Tile {
  const card = new Card("insight-volatility", "Volatility", {
    term: "Volatility",
    label: "volatility",
    note: "run-outcome flakiness",
    why:
      "Targets whose result changes without their inputs changing. A volatile target is one whose" +
      " green you cannot spend, so it costs more than a target that simply fails.",
  });
  const table = new SortableTable<VolatilityRowView>(volatilityCols, {
    sortKey: "score",
    emptyText: "No run-outcome history recorded yet.",
  });
  card.body.append(table.el);
  return {
    el: card.el,
    update(s: DashboardState) {
      if (!s.insight) {
        table.setUnresolved(s.insightNote);
        card.setNote("");
        return;
      }
      table.setUnresolved(null);
      const v = s.insight.volatility;
      if (!v) {
        card.setNote("no run history");
        table.setRows([]);
        return;
      }
      const volatile = v.targets.filter((t) => t.volatile).length;
      card.setNote(
        `${v.targets.length} targets, ${volatile} volatile (threshold ${v.threshold.toFixed(2)})`,
      );
      table.setRows(v.targets);
    },
    destroy() {},
  };
}

// ---- section ---------------------------------------------------------------

// insightSection builds the labeled "Insight" band (heading + a manual refresh
// button) and the five lens tiles. main.ts mounts the band and forwards store
// updates to each tile, and to the band itself (it is a Tile too, purely so its
// "last ran" text has a hook into the same subscription every other tile uses).
// onRefresh forces an out-of-band /api/v1/insight refetch, returning its promise
// so the button can show it is doing something: the poll it forces can take
// seconds, and nothing else about the tiles visibly changes until it resolves.
export function insightSection(
  onRefresh: () => Promise<void>,
): { el: HTMLElement; tiles: Tile[] } & Tile {
  const band = h("div", "console-dashboard-insight");
  const head = h("div", "console-dashboard-insight__head");
  head.append(h("h2", "console-dashboard-insight__title", "Insight"));
  head.append(
    helpGlyph(
      "Where a codebase's attention and risk concentrate: five lenses read git history, volatility reads run outcomes.",
      "Insight",
    ),
  );
  const lastRan = h("span", "console-dashboard-insight__lastran");
  head.append(lastRan);
  const refresh = h("button", "console-dashboard-insight__refresh");
  refresh.dataset.controlSize = "default";
  refresh.type = "button";
  refresh.title = "Refetch the insight lenses now";
  refresh.append(svgGlyph(REFRESH, 14));
  // The label is its own node because the click handler swaps it: writing textContent on the button
  // would take the mark with it, and it would not come back.
  const refreshLabel = h("span", "", "Refresh");
  refresh.append(refreshLabel);
  refresh.addEventListener("click", () => {
    refresh.disabled = true;
    refreshLabel.textContent = "Refreshing...";
    void onRefresh().finally(() => {
      refresh.disabled = false;
      refreshLabel.textContent = "Refresh";
    });
  });
  head.append(refresh);
  band.append(head);

  const tiles = [
    hotspotsTile(),
    hotspotFilesTile(),
    affinityTile(),
    ownershipTile(),
    trendTile(),
    volatilityTile(),
  ];
  return {
    el: band,
    tiles,
    update(s: DashboardState): void {
      if (!s.insightUpdatedAt) {
        lastRan.textContent = "";
        return;
      }
      const t = new Date(s.insightUpdatedAt).toLocaleTimeString([], {
        hour: "2-digit",
        minute: "2-digit",
      });
      lastRan.textContent = "ran " + t;
      lastRan.title = "The insight lenses last answered at " + t + ".";
    },
    destroy(): void {},
  };
}
