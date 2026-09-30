// model.ts - what the Tools table and the Dashboard's Toolchain summary both say about the rows.
// Pure: no DOM, so one definition of "past end of life" serves the filter chip, the column and the
// summary count, and they cannot disagree.
//
// The three windows are separate columns on purpose. A failing bound's first question is who set
// it - the spell (what its ops need to run at all) or this project (what it has qualified) - and
// the CLI diagnostic cannot answer that, because the intersection discards provenance before the
// message is built.
//
// There is deliberately no "enforced" column. Whether a window can fail a build depends on whether
// the project dispatches a spell op at all, which this view cannot see. See the reserved field in
// magus/tool/v1alpha1/tool.proto.

import type { LifecycleView, ToolRowView } from "../dashboard/state";
import type { Column } from "../../ui/table";

export type ToolFilterKey = "eol" | "unannounced" | "unpinned";

export interface ToolFilter {
  key: ToolFilterKey;
  label: string;
  match: (r: ToolRowView) => boolean;
}

// A pin is the window the project itself qualified. A tool with only its spell's window is held to
// what the spell needs to run, which no one here chose.
export const FILTERS: readonly ToolFilter[] = [
  { key: "eol", label: "Past end of life", match: (r) => r.support === "eol" },
  { key: "unannounced", label: "Unannounced", match: (r) => r.support === "unannounced" },
  { key: "unpinned", label: "Unpinned", match: (r) => r.workspaceWindow === "" },
];

export interface ToolCounts {
  total: number;
  pastEol: number;
  outsideWindow: number;
  unannounced: number;
  unpinned: number;
}

export function toolCounts(rows: readonly ToolRowView[]): ToolCounts {
  const n = (key: ToolFilterKey): number => {
    const f = FILTERS.find((x) => x.key === key);
    return f ? rows.filter(f.match).length : 0;
  };
  return {
    total: rows.length,
    pastEol: n("eol"),
    outsideWindow: rows.filter((r) => r.code !== "").length,
    unannounced: n("unannounced"),
    unpinned: n("unpinned"),
  };
}

// applyFilters keeps the rows every active filter matches.
export function applyFilters(
  rows: readonly ToolRowView[],
  active: ReadonlySet<ToolFilterKey>,
): ToolRowView[] {
  const on = FILTERS.filter((f) => active.has(f.key));
  return rows.filter((r) => on.every((f) => f.match(r)));
}

// age renders how old a probe is in the coarsest honest unit, "-" when never probed.
export const age = (ms: number, now: number): string => {
  if (!ms) return "-";
  const s = Math.max(0, Math.round((now - ms) / 1000));
  if (s < 60) return s + "s ago";
  const m = Math.round(s / 60);
  if (m < 60) return m + "m ago";
  return Math.round(m / 60) + "h ago";
};

// declaredBy says which side set the window a reader is looking at. Both means the two
// intersected; neither means nothing constrains this tool.
export const declaredBy = (r: ToolRowView): string => {
  const spell = r.spellWindow !== "";
  const ws = r.workspaceWindow !== "";
  if (spell && ws) return "spell + workspace";
  if (spell) return "spell";
  if (ws) return "workspace";
  return "-";
};

// columns reads the clock through now so a test can pin the probe ages.
export const columns = (now: () => number = Date.now): Column<ToolRowView>[] => [
  { key: "bin", label: "Tool", text: (r) => r.bin, sort: (r) => r.bin },
  { key: "project", label: "Project", text: (r) => r.project, sort: (r) => r.project },
  {
    key: "installed",
    // What the binary on PATH reported, not what a version manager pinned: magus checks what
    // actually ran.
    label: "Version",
    text: (r) => r.installed || "not found",
    sort: (r) => r.installed,
  },
  {
    key: "pin",
    label: "Pin",
    text: (r) => r.workspaceWindow || "unpinned",
    sort: (r) => r.workspaceWindow,
  },
  {
    key: "effective",
    label: "Window",
    text: (r) => r.effectiveWindow || "unconstrained",
    sort: (r) => r.effectiveWindow,
  },
  { key: "source", label: "Declared by", text: declaredBy, sort: declaredBy },
  {
    key: "verdict",
    label: "Verdict",
    text: (r) => (r.code ? r.verdict + " (" + r.code + ")" : r.verdict),
    sort: (r) => r.verdict,
  },
  {
    key: "probed",
    label: "Probed",
    text: (r) => age(r.probedAtMs, now()),
    sort: (r) => r.probedAtMs,
  },
  { key: "cycle", label: "Cycle", text: (r) => r.cycle || "-", sort: (r) => r.cycle },
  { key: "eol", label: "End of life", text: (r) => r.eol || "-", sort: (r) => r.eol },
  // Named after `magus describe tools`' column, and read from the same provider answer.
  { key: "support", label: "Support", text: (r) => r.support || "-", sort: (r) => r.support },
];

// lifecycleNote says where the end-of-life columns came from when that is not a live answer. An
// unreached or offline provider leaves every row unknown, and a note that only counted windows
// would let that pass as "all fine".
export const lifecycleNote = (l: LifecycleView | undefined): string => {
  if (!l) return "";
  switch (l.state) {
    case "offline":
      return "end of life not fetched: MAGUS_OFFLINE is set on the server";
    case "unreached": {
      const why = l.detail ? " (" + l.detail + ")" : "";
      return "end of life unknown: " + l.provider + " did not answer" + why;
    }
    default:
      return "";
  }
};

// lifecycleSource names the provider and how fresh its answer is. "" unless there is an answer to
// credit: the other states are lifecycleNote's to explain.
export const lifecycleSource = (l: LifecycleView | undefined): string =>
  l && (l.state === "live" || l.state === "cached") ? l.provider + " (" + l.state + ")" : "";
