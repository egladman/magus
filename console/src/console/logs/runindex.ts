// runindex.ts - the pure half of the run browser: the row types the daemon's two feeds decode
// into, the filter grammar the panel's box parses, and the tree spec the DOM builder paints.
// It carries NO DOM dependency (`import type` is erased at build), so the grouping and the
// filter are unit-tested in node - the same split query.ts made out of filter.ts, and for the
// same reason: the interesting logic here is which rows survive a query and how they nest,
// neither of which needs an element to be true.

// RunSummary is one row of the daemon's /api/v1/outputs feed (a cache.OutputDescriptor projected
// to the wire): one TARGET's stored output. Times are unix milliseconds. target is the REPRO
// target (a charm suffix like "build:rw" is preserved); the tree groups by the bare name.
export interface RunSummary {
  ref: string;
  project: string;
  target: string;
  inv?: string;
  failed: boolean;
  error?: string;
  timestamp_ms: number;
  duration_ms: number;
}

// RunLog is one row of the daemon's /api/v1/runs feed: one INVOCATION - a whole magus command,
// launch to exit. It is the unit a person remembers a run by ("the affected ci I ran before
// lunch"), which is why it, not the output ref, is the browser's default top level. status is
// empty on a run that was interrupted before it wrote a finished event.
export interface RunLog {
  inv: string;
  arguments?: string[];
  trigger?: string;
  started_ms: number;
  finished_ms?: number;
  status?: string;
  magus_version?: string;
}

// BrowseMode picks the tree's top level: "runs" nests invocation -> target, "projects" nests
// project -> target -> run. They are two orderings of the SAME rows, not two datasets.
export type BrowseMode = "runs" | "projects";

// Selection is what clicking a node opens. An invocation opens its whole journal; a target opens
// that same journal narrowed to the target (see focus), because the events of one step are only
// meaningful beside the run that scheduled them. A run with no retained journal still opens - the
// viewer falls back to the verbatim blob - which is why the ref rides along either way.
export type Selection =
  | { kind: "invocation"; inv: string; label: string }
  | { kind: "output"; run: RunSummary; focus: string };

// NodeSpec is one row of the tree, ready to paint: PF chrome and nesting, no daemon vocabulary.
export interface NodeSpec {
  label: string;
  // When label is a RELATIVE time, the instant it was computed from. It rides along so a slow ticker
  // can rewrite the label in place as time passes, without re-rendering the tree - a re-render every
  // tick would take focus off whatever node a keyboard reader was on.
  timeMs?: number;
  // A second, dimmer line under the label (PF's node description). Carries what a compact label
  // cannot: an invocation's argv under its relative time, a target's outcome under its name.
  description?: string;
  count?: number;
  countUnit?: string;
  status?: "pass" | "fail" | "mixed";
  title?: string;
  children?: NodeSpec[];
  select?: Selection;
  // The id a caller keys expansion and current-node state off. Stable across refreshes so a
  // reload does not collapse the branch the reader was reading.
  id: string;
}

// A parsed filter query: `key:value` terms with a known key are field filters, everything else is
// free text. Terms combine with AND, case-insensitively. Deliberately the same pragmatic shape as
// the log filter (query.ts) rather than a second grammar to learn.
export interface RunFilterTerm {
  key: string;
  value: string;
}
export interface RunFilter {
  keyed: RunFilterTerm[];
  texts: string[];
  empty: boolean;
}

const FILTER_KEYS = ["project", "target", "status", "trigger", "ref", "cmd"];

export function parseRunFilter(q: string): RunFilter {
  const keyed: RunFilterTerm[] = [];
  const texts: string[] = [];
  for (const tok of (q || "").trim().split(/\s+/)) {
    if (!tok) continue;
    const ci = tok.indexOf(":");
    if (ci > 0) {
      const key = tok.slice(0, ci).toLowerCase();
      const val = tok.slice(ci + 1).toLowerCase();
      if (val && FILTER_KEYS.includes(key)) {
        keyed.push({ key, value: val });
        continue;
      }
      // An unknown key, or a bare "target:", falls through as free text rather than matching
      // nothing: a reader typing a path with a colon in it should get a search, not silence.
    }
    texts.push(tok.toLowerCase());
  }
  return { keyed, texts, empty: keyed.length === 0 && texts.length === 0 };
}

// bareTarget strips the charm suffix ("build:rw" -> "build"), mirroring the Go cache.bareTarget, so
// a target's runs group under its declared name regardless of which charm variant produced each.
export function bareTarget(t: string): string {
  const i = t.indexOf(":");
  return i < 0 ? t : t.slice(0, i);
}

// runStatus reads a stored output's outcome in the vocabulary the dot uses.
function runStatus(r: RunSummary): "pass" | "fail" {
  return r.failed ? "fail" : "pass";
}

// commandText renders an invocation's argv the way the CLI was typed, for the filter to match and
// the row to show. A run whose journal aged out has no argv, so it reads as its bare id.
export function commandText(log: RunLog | undefined): string {
  const args = log && log.arguments ? log.arguments : [];
  return args.length ? "magus " + args.join(" ") : "";
}

// matchesFilter reports whether one stored output survives the query, judged with the invocation
// it belongs to in hand: `cmd:` and a free-text term both reach the command line, so searching for
// "affected" finds every target that ran under `magus affected ci` even though no target is named
// that. A run whose journal has aged out simply has less to match against, never less to show.
export function matchesFilter(r: RunSummary, log: RunLog | undefined, f: RunFilter): boolean {
  if (f.empty) return true;
  const cmd = commandText(log).toLowerCase();
  for (const t of f.keyed) {
    switch (t.key) {
      case "project":
        if (!r.project.toLowerCase().includes(t.value)) return false;
        break;
      case "target":
        if (!r.target.toLowerCase().includes(t.value)) return false;
        break;
      case "status":
        if (runStatus(r) !== t.value) return false;
        break;
      case "trigger":
        if ((log?.trigger || "").toLowerCase() !== t.value) return false;
        break;
      case "ref":
        if (!r.ref.toLowerCase().startsWith(t.value)) return false;
        break;
      case "cmd":
        if (!cmd.includes(t.value)) return false;
        break;
    }
  }
  const hay = (
    r.project +
    " " +
    r.target +
    " " +
    r.ref +
    " " +
    (r.error || "") +
    " " +
    cmd
  ).toLowerCase();
  return f.texts.every((t) => hay.includes(t));
}

// groupStatus folds a branch's leaves into one dot: any failure shows as failed, because the whole
// point of a collapsed branch is to say whether it is worth opening.
function groupStatus(runs: RunSummary[]): "pass" | "fail" | "mixed" | undefined {
  if (!runs.length) return undefined;
  const failed = runs.filter((r) => r.failed).length;
  if (failed === 0) return "pass";
  return failed === runs.length ? "fail" : "mixed";
}

// durText renders a millisecond duration compactly ("812ms", "1.2s", "3m04s").
export function durText(ms: number): string {
  if (ms < 1000) return Math.round(ms) + "ms";
  if (ms < 60000) return (ms / 1000).toFixed(1) + "s";
  const m = Math.floor(ms / 60000);
  const s = Math.round((ms % 60000) / 1000);
  return m + "m" + (s < 10 ? "0" : "") + s + "s";
}

// relTime renders a unix-ms timestamp as a compact "how long ago", falling back to a clock time for
// anything older than a day so distant runs stay distinguishable. now is injected so the function
// is pure and testable.
export function relTime(ms: number, now: number): string {
  const sec = Math.max(0, Math.round((now - ms) / 1000));
  if (sec < 60) return sec + "s ago";
  const min = Math.round(sec / 60);
  if (min < 60) return min + "m ago";
  const hr = Math.round(min / 60);
  if (hr < 24) return hr + "h ago";
  const d = new Date(ms);
  const pad = (n: number): string => (n < 10 ? "0" + n : String(n));
  return (
    pad(d.getMonth() + 1) +
    "/" +
    pad(d.getDate()) +
    " " +
    pad(d.getHours()) +
    ":" +
    pad(d.getMinutes())
  );
}

// UNGROUPED is the bucket for outputs whose invocation is not in the runs feed - a run predating
// the feed, or one whose journal rotated out from under its still-retained outputs. They are real
// runs and stay browsable; the label says why they have no command line rather than implying the
// rows are junk.
const UNGROUPED = "(no run record)";

// RunRow is ONE invocation with the stored outputs belonging to it - the model both browsers read.
// The side panel paints it as a tree node; the Runs page paints it as a list row with a detail pane.
// Neither carries presentation, so the two cannot drift on what a run IS.
export interface RunRow {
  inv: string;
  log: RunLog | undefined; // absent when the journal rotated out from under its outputs
  outputs: RunSummary[];
  command: string; // "magus affected ci", or the bare id when the journal is gone
  startMs: number;
  endMs: number;
  durationMs: number;
  status: "pass" | "fail" | "mixed" | undefined;
  trigger: string;
}

// buildRunRows joins the two feeds on each output's invocation id, newest first. `unfiltered` keeps
// a run that produced no retained output ("I ran that and everything cached" is an answer); a search
// drops those, because a search that still listed every run would not be one.
export function buildRunRows(runs: RunSummary[], logs: RunLog[], unfiltered: boolean): RunRow[] {
  const byInv = new Map<string, RunLog>();
  for (const l of logs) byInv.set(l.inv, l);
  const grouped = new Map<string, RunSummary[]>();
  for (const r of runs) {
    const key = r.inv || UNGROUPED;
    const list = grouped.get(key) ?? [];
    if (!list.length) grouped.set(key, list);
    list.push(r);
  }
  const rows: RunRow[] = [];
  const emit = (inv: string, log: RunLog | undefined, outputs: RunSummary[]): void => {
    const stamps = outputs.map((o) => o.timestamp_ms);
    const startMs = log?.started_ms || (stamps.length ? Math.min(...stamps) : 0);
    const endMs = log?.finished_ms || (stamps.length ? Math.max(...stamps) : startMs);
    // The invocation's OWN outcome wins when its journal recorded one: a run can fail after every
    // target passed (a gate outside the graph), and folding its targets would then report a pass.
    const status =
      log?.status === "fail" || log?.status === "pass" ? log.status : groupStatus(outputs);
    rows.push({
      inv,
      log,
      outputs,
      command: commandText(log) || inv,
      startMs,
      endMs,
      durationMs: Math.max(0, endMs - startMs),
      status,
      trigger: log?.trigger ?? "",
    });
  };
  for (const log of logs) {
    const outputs = grouped.get(log.inv);
    if (!outputs && !unfiltered) continue;
    emit(log.inv, log, outputs ?? []);
    grouped.delete(log.inv);
  }
  for (const [inv, outputs] of grouped) emit(inv, byInv.get(inv), outputs);
  return rows;
}

export interface BuildTreeOpts {
  runs: RunSummary[];
  logs: RunLog[];
  mode: BrowseMode;
  filter: RunFilter;
  now: number;
}

// buildRunTree groups the two feeds into the tree the panel paints. Both feeds arrive newest-first
// and every grouping below preserves insertion order, so newest-first survives to the leaves.
//
// It returns [] for "nothing matched" exactly as it does for "nothing stored" - the caller
// distinguishes them from the unfiltered counts it already has, because only it knows whether to
// say "no stored runs" or "no runs match".
export function buildRunTree(opts: BuildTreeOpts): NodeSpec[] {
  const byInv = new Map<string, RunLog>();
  for (const l of opts.logs) byInv.set(l.inv, l);
  const kept = opts.runs.filter((r) => matchesFilter(r, byInv.get(r.inv || ""), opts.filter));
  return opts.mode === "runs"
    ? buildRunRows(kept, opts.logs, opts.filter.empty).map((row) => runNode(row, opts.now))
    : projectsTree(kept, opts.now);
}

// runNode paints one RunRow as a tree branch. The DATA is buildRunRows' - shared with the Runs page,
// so the two browsers cannot disagree about what a run is or how it turned out.
function runNode(row: RunRow, now: number): NodeSpec {
  return {
    id: "inv:" + row.inv,
    label: row.startMs ? relTime(row.startMs, now) : row.inv,
    timeMs: row.startMs || undefined,
    description: row.command,
    count: row.outputs.length || undefined,
    countUnit: "target",
    status: row.status === "mixed" ? "fail" : row.status,
    title:
      row.command +
      (row.durationMs ? " - " + durText(row.durationMs) : "") +
      (row.trigger ? " - " + row.trigger : ""),
    select: row.log ? { kind: "invocation", inv: row.inv, label: row.command } : undefined,
    children: row.outputs.map((r) => targetLeaf(r, now)),
  };
}

// targetLeaf is one stored output under its invocation: the target that ran, its outcome and how
// long it took.
function targetLeaf(r: RunSummary, now: number): NodeSpec {
  const tgt = bareTarget(r.target) || "(run)";
  const name = (r.project && r.project !== "." ? r.project + ":" : "") + tgt;
  return {
    id: "ref:" + r.ref,
    label: name,
    description: durText(r.duration_ms) + (r.error ? " - " + r.error : ""),
    status: runStatus(r),
    title:
      (r.failed ? "failed" : "passed") +
      " - " +
      name +
      " - " +
      durText(r.duration_ms) +
      " - " +
      relTime(r.timestamp_ms, now) +
      " - " +
      r.ref +
      (r.error ? " - " + r.error : ""),
    select: { kind: "output", run: r, focus: tgt },
  };
}

// projectsTree nests project -> target -> run: the history lens, for "show me every build of this
// one project" rather than "what did that command do".
function projectsTree(kept: RunSummary[], now: number): NodeSpec[] {
  const byProject = new Map<string, Map<string, RunSummary[]>>();
  for (const r of kept) {
    const proj = r.project || "(workspace)";
    const tgt = bareTarget(r.target) || "(run)";
    let targets = byProject.get(proj);
    if (!targets) {
      targets = new Map();
      byProject.set(proj, targets);
    }
    const list = targets.get(tgt) ?? [];
    if (!list.length) targets.set(tgt, list);
    list.push(r);
  }
  const out: NodeSpec[] = [];
  for (const [proj, targets] of byProject) {
    const targetSpecs: NodeSpec[] = [];
    const projRuns: RunSummary[] = [];
    for (const [tgt, list] of targets) {
      projRuns.push(...list);
      targetSpecs.push({
        id: "proj:" + proj + "/" + tgt,
        label: tgt,
        count: list.length,
        countUnit: "run",
        status: groupStatus(list),
        children: list.map((r) => ({
          id: "ref:" + r.ref,
          label: relTime(r.timestamp_ms, now),
          timeMs: r.timestamp_ms,
          description: durText(r.duration_ms) + (r.error ? " - " + r.error : ""),
          status: runStatus(r),
          title:
            (r.failed ? "failed" : "passed") +
            " - " +
            tgt +
            " - " +
            durText(r.duration_ms) +
            " - " +
            r.ref +
            (r.error ? " - " + r.error : ""),
          select: { kind: "output", run: r, focus: tgt } as Selection,
        })),
      });
    }
    out.push({
      id: "proj:" + proj,
      label: proj,
      count: targetSpecs.length,
      countUnit: "target",
      status: groupStatus(projRuns),
      children: targetSpecs,
    });
  }
  return out;
}

// --- Facets -------------------------------------------------------------------
// The Runs page's answer to "what do I even type here". A bare filter box asks the reader to know a
// grammar before they can use it; facets invert that - every value that EXISTS is listed with its
// count, one click applies it, and the query it wrote is then visible in the box. The box and the
// facets are the same filter, so clicking teaches the syntax for the times you do want to type.

// FacetValue is one clickable value: what it filters on, how many runs carry it, and whether the
// current query already has it.
export interface FacetValue {
  value: string;
  label: string;
  count: number;
  active: boolean;
}
export interface Facet {
  key: string; // the filter key it writes: status/trigger/project/target
  label: string; // the heading a reader sees
  values: FacetValue[];
}

// facetKeys: which of the filter keys get a facet, in the order the rail lists them. Ordered by how
// often the question gets asked, not alphabetically - "did anything fail" is the reason most people
// open this page at all.
const FACET_ORDER: { key: string; label: string }[] = [
  { key: "status", label: "Status" },
  { key: "project", label: "Project" },
  { key: "target", label: "Target" },
  { key: "trigger", label: "Trigger" },
];

// withoutKey drops one key's terms from a query, which is what makes a facet's counts predictive:
// each facet counts against everything EXCEPT its own selection, so its other values still show what
// picking them would give rather than the 0 they would read as once a sibling value is active.
function withoutKey(filter: RunFilter, key: string): RunFilter {
  const keyed = filter.keyed.filter((t) => t.key !== key);
  return { keyed, texts: filter.texts, empty: keyed.length === 0 && filter.texts.length === 0 };
}

// buildFacets derives the rail from the rows themselves: only values that actually occur are listed,
// so the rail never offers a filter that leads nowhere, and it needs no configuration as the
// vocabulary grows. Values sort by count (commonest first) then name, and each facet is capped -
// a workspace with 200 targets would otherwise bury the list it sits beside.
export function buildFacets(
  runs: RunSummary[],
  logs: RunLog[],
  filter: RunFilter,
  limit = 8,
): Facet[] {
  const byInv = new Map<string, RunLog>();
  for (const l of logs) byInv.set(l.inv, l);
  const out: Facet[] = [];
  for (const { key, label } of FACET_ORDER) {
    // Counts are over the set filtered by every OTHER key, so they answer "how many if I click this".
    const others = withoutKey(filter, key);
    const kept = runs.filter((r) => matchesFilter(r, byInv.get(r.inv || ""), others));
    // Faceted over RUNS, not over outputs, because a run is what the list beside the strip shows and
    // what a click leaves. Over outputs the two disagreed twice over: a header reading "28 runs"
    // beside a strip totalling 25, and a run that kept no output carrying no status at all even
    // though its journal recorded one.
    const counts = new Map<string, number>();
    for (const row of buildRunRows(kept, logs, others.empty)) {
      for (const v of facetValues(key, row)) counts.set(v, (counts.get(v) ?? 0) + 1);
    }
    const active = new Set(filter.keyed.filter((t) => t.key === key).map((t) => t.value));
    // An active value stays listed even at zero, so a filter that matched nothing is still visibly
    // ON and one click from being turned off. Dropping it would strand the reader in an empty list
    // with no way back that does not involve editing the query by hand.
    for (const a of active) if (!counts.has(a)) counts.set(a, 0);
    const values = [...counts.entries()]
      .map(([value, count]) => ({ value, label: value, count, active: active.has(value) }))
      .sort(
        (a, b) =>
          (b.active ? 1 : 0) - (a.active ? 1 : 0) ||
          b.count - a.count ||
          (a.value < b.value ? -1 : 1),
      )
      .slice(0, limit);
    if (values.length) out.push({ key, label, values });
  }
  return out;
}

// facetValues is the DISTINCT values one run contributes to a facet - distinct, so a run that built
// four targets in one project counts once under that project, which is what makes the number
// predict how many rows clicking it leaves.
function facetValues(key: string, row: RunRow): string[] {
  switch (key) {
    // A partly-failed run reads as "fail" here because that is what clicking gives: `status:fail`
    // keeps any run holding a failed target, so labelling it anything else would promise a set the
    // filter does not produce.
    case "status":
      return row.status ? [row.status === "mixed" ? "fail" : row.status] : [];
    case "project":
      return distinct(row.outputs.map((o) => o.project).filter(Boolean));
    case "target":
      return distinct(row.outputs.map((o) => bareTarget(o.target)).filter(Boolean));
    case "trigger":
      return row.trigger ? [row.trigger] : [];
    default:
      return [];
  }
}

function distinct(values: string[]): string[] {
  return [...new Set(values)];
}

// toggleFilterTerm adds or removes one `key:value` term in a query STRING, preserving everything
// else the reader typed. It writes back into the same box they could have typed into, which is the
// whole point: the click is a demonstration of the syntax, not a hidden state change beside it.
export function toggleFilterTerm(query: string, key: string, value: string): string {
  const term = key + ":" + value;
  const toks = (query || "").trim().split(/\s+/).filter(Boolean);
  const i = toks.findIndex((t) => t.toLowerCase() === term.toLowerCase());
  if (i >= 0) {
    toks.splice(i, 1);
    return toks.join(" ");
  }
  // One value per key for the single-valued facets: picking a second status would AND them and
  // always match nothing, which reads as the page being broken rather than as the query being
  // impossible. Multi-valued keys (project, target) accumulate.
  const single = key === "status" || key === "trigger";
  const kept = single ? toks.filter((t) => !t.toLowerCase().startsWith(key + ":")) : toks;
  kept.push(term);
  return kept.join(" ");
}
