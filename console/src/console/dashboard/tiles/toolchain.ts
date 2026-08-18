// toolchain.ts - the Toolchain tile: every binary this workspace's spells drive, the
// version each one reported, and the window it is held to.
//
// The three windows are separate columns on purpose. A failing bound's first question is
// who set it - the spell (what its ops need to run at all) or this project (what it has
// qualified) - and the CLI diagnostic cannot answer that, because the intersection
// discards provenance before the message is built. A table has the room the error string
// does not.
//
// "Probed" shows the reading's age rather than implying live, because the daemon caches
// probes behind a TTL and a page has no build to piggyback on.
//
// There is deliberately no "enforced" column. Whether a window can actually fail a build
// depends on whether the project dispatches a spell op at all, which this view cannot see;
// an earlier attempt computed "does an op fork this binary", which is a different question
// and answered it wrongly. See the reserved field in magus/tool/v1alpha1/tool.proto.

import type { DashboardState, ToolRowView } from "../state";
import { SortableTable, type Column } from "./widgets";
import { Card, type Tile } from "./card";

// age renders how old a probe is in the coarsest honest unit. "-" when never probed.
const age = (ms: number, now: number): string => {
  if (!ms) return "-";
  const s = Math.max(0, Math.round((now - ms) / 1000));
  if (s < 60) return s + "s ago";
  const m = Math.round(s / 60);
  if (m < 60) return m + "m ago";
  return Math.round(m / 60) + "h ago";
};

const cols: Column<ToolRowView>[] = [
  { key: "bin", label: "Tool", text: (r) => r.bin, sort: (r) => r.bin },
  { key: "project", label: "Project", text: (r) => r.project, sort: (r) => r.project },
  {
    key: "installed",
    // Not "version": this is what the binary on PATH reported, which is the whole point -
    // a version manager pins what to install, magus checks what actually ran.
    label: "Installed",
    text: (r) => r.installed || "not found",
    sort: (r) => r.installed,
  },
  {
    key: "effective",
    label: "Window",
    text: (r) => r.effectiveWindow || "unconstrained",
    sort: (r) => r.effectiveWindow,
  },
  {
    key: "source",
    label: "Declared by",
    // Which side actually set the window a reader is looking at. Both means the two
    // intersected; neither means nothing constrains this tool.
    text: (r) => {
      const spell = r.spellWindow !== "";
      const ws = r.workspaceWindow !== "";
      if (spell && ws) return "spell + workspace";
      if (spell) return "spell";
      if (ws) return "workspace";
      return "-";
    },
    sort: (r) => (r.spellWindow ? 2 : 0) + (r.workspaceWindow ? 1 : 0),
  },
  {
    key: "verdict",
    label: "Verdict",
    text: (r) => (r.code ? r.verdict + " (" + r.code + ")" : r.verdict),
    sort: (r) => r.verdict,
  },
  {
    key: "probed",
    label: "Probed",
    text: (r) => age(r.probedAtMs, Date.now()),
    sort: (r) => r.probedAtMs,
  },
];

// toolchainTile renders the table, or the empty state when no project declares a probed
// tool. A workspace with no windows is a legitimate resting state, not a failure, so the
// empty copy says what to do rather than reporting an absence.
export function toolchainTile(): Tile {
  const table = new SortableTable<ToolRowView>(cols, {
    sortKey: "bin",
    // A workspace with no windows is a legitimate resting state, so the empty copy says
    // what to declare rather than only reporting an absence.
    emptyText:
      "No project declares a probed tool. A spell declares what its ops need with supported; a project declares its own policy with the tools key.",
  });
  const card = new Card("toolchain", "Toolchain", {
    note: "The binaries this workspace drives, and the version window each is held to.",
  });
  card.body.append(table.el);

  return {
    el: card.el,
    update(state: DashboardState) {
      const view = state.tools;
      const rows = view?.rows ?? [];
      table.setRows(rows);
      // Lead with the count that needs acting on. Zero violations is worth stating
      // explicitly, because silence reads as "not checked".
      card.setNote(
        rows.length === 0
          ? "The binaries this workspace drives, and the version window each is held to."
          : view && view.violations > 0
            ? view.violations + " outside their window"
            : rows.length + " tools, all inside their window",
      );
    },
    destroy() {},
  };
}
