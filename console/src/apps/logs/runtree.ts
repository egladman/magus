import { createClient } from "@connectrpc/connect";
import { toBinary } from "@bufbuild/protobuf";
import type { Duration, Timestamp } from "@bufbuild/protobuf/wkt";
import {
  JournalSchema,
  Status,
  Trigger,
  ViewerService,
  type Output,
} from "@wire/viewer/v1alpha1/viewer_pb";
import { createServerTransport } from "../../lib/server";
import { must } from "../../lib/guards";
// runtree.ts - the Log Viewer's run browser: a PatternFly TreeView down the left of the viewer
// that lists prior runs so a reader can find one WITHOUT a ref somebody printed on a terminal.
// Two orderings of the same rows, switched in the panel header: "Runs" nests invocation ->
// target (the command you actually typed, newest first) and "Projects" nests project -> target ->
// run. A filter box narrows both.
//
// It reads two read-only server feeds - /api/v1/outputs (the retained per-target outputs) and
// /api/v1/runs (the retained invocation journals) - and joins them on each output's invocation id.
// On selection it hands the choice to the viewer, which loads that run's journal from
// /api/v1/run and renders it structurally, falling back to the verbatim blob at /api/v1/output
// when the journal has rotated away. The whole browser is a no-op with no reachable server (the
// tree stays empty and says so), so the offline #data/#src paths are unaffected. PF owns the tree
// chrome (pf-v6-c-tree-view); only the panel frame, the filter row, and the status dot are ours.
//
// This file is the DOM half. The grouping, the filter grammar, and the row shapes are in
// runindex.ts, which has no DOM dependency and carries the unit tests.

import { authHeaders, fetchSSE, probeServer } from "../../lib/server";
import { REFRESH } from "../../ui/glyph";
import { statusMark, type Status as MarkStatus } from "../../ui/status";
import { timeEl } from "../../render/time";
import { createFilterField } from "../../render/filterField";
import { attachHelpPopover, createHelpButton } from "../../ui/help-popover";
import { persisted } from "../../lib/persist";
import { scenarioInvocations, scenarioRuns } from "../../desktop/demo-scenario";
import {
  buildRunRows,
  buildRunTree,
  parseRunFilter,
  relTime,
  type BrowseMode,
  type NodeSpec,
  type RunLog,
  type RunSummary,
  type Selection,
} from "./runindex";
// Re-exported because the activity index and the activity drawer already import relTime and the
// run row from here; the move to runindex.ts is a split, not a relocation of the entry point.
export { relTime };
export type { RunLog, RunSummary, Selection };

// RunBrowserDeps: what initRunBrowser needs from the log viewer. scroll is the viewer's scroll box
// (the tree docks to its left, sharing the panel below the toolbar). host/token address the server
// (empty host => demo/offline: the demo runs render, selection loads a synthetic sample). onSelect
// hands the chosen row to the viewer to load.
//
// nowMs is a FUNCTION, not a stamp: the tree repaints on its own now (watchRuns), and a value read
// once at mount would leave every "3m ago" frozen at what it said when the viewer opened. Injected
// rather than calling Date.now() here so the labels stay testable.
export interface RunBrowserDeps {
  scroll: HTMLElement;
  host: string;
  token: string | null;
  demo: boolean;
  nowMs: () => number;
  onSelect: (sel: Selection) => void;
  // Called after each load with what the panel now knows, so the viewer's own empty state can say
  // the same thing about the server and the stored runs that the panel does.
  onState?: (state: RunBrowserState) => void;
}

export interface RunBrowserState {
  loaded: boolean;
  // Invocations the store holds, ignoring any filter.
  runs: number;
  // Both feeds came back empty and nothing answered at the address.
  unreachable: boolean;
}

// fetchRuns reads the server's run list. Resolves to [] on any failure (no server, auth, an old
// server without the route) so the browser degrades to empty rather than throwing - the viewer's
// other load paths never depend on it.
// The wire carries a Timestamp and a Duration; this viewer works in unix millis, which is the unit
// the store records. Absent reads as 0, never NaN - a NaN reaching a comparator leaves the whole
// list in an order nobody promised.
function tsMillis(ts: Timestamp | undefined): number {
  return ts ? Number(ts.seconds) * 1000 + ts.nanos / 1e6 : 0;
}

function durMillis(d: Duration | undefined): number {
  return d ? Number(d.seconds) * 1000 + d.nanos / 1e6 : 0;
}

export async function fetchRuns(host: string, token: string | null): Promise<RunSummary[]> {
  try {
    const client = createClient(ViewerService, createServerTransport(host, token));
    const resp = await client.listOutputs({}, { signal: AbortSignal.timeout(4000) });
    return resp.outputs.map((o) => ({
      ref: o.ref,
      project: o.project,
      target: o.target,
      inv: o.invocation,
      failed: o.failed,
      error: o.error,
      timestamp_ms: tsMillis(o.createTime),
      duration_ms: durMillis(o.duration),
    }));
  } catch {
    // reported: by the server transport
    return [];
  }
}

// fetchRunOutput reads one run's verbatim captured output text (GET /api/v1/output?ref=). Returns null
// on any failure, so a stale tree selection surfaces an honest "could not load" rather than a hang.
export async function fetchRunOutput(
  host: string,
  token: string | null,
  ref: string,
): Promise<string | null> {
  try {
    const client = createClient(ViewerService, createServerTransport(host, token));
    const resp = await client.getOutput({ name: ref }, { signal: AbortSignal.timeout(8000) });
    // bytes, not string: a captured log is whatever the tool wrote, which is not guaranteed to be
    // valid UTF-8. Decoded here because this viewer renders text.
    return new TextDecoder().decode(resp.body);
  } catch {
    // reported: by the server transport
    return null;
  }
}

// fetchRunLogs reads the server's INVOCATION feed - the retained run journals, newest first. Same
// degradation as fetchRuns: [] on any failure, including a server too old to serve the route, so a
// mixed-version pair falls back to the project ordering rather than showing an error.
export async function fetchRunLogs(host: string, token: string | null): Promise<RunLog[]> {
  try {
    const client = createClient(ViewerService, createServerTransport(host, token));
    const resp = await client.listInvocations({}, { signal: AbortSignal.timeout(4000) });
    return resp.invocations.map((i) => ({
      inv: i.id,
      arguments: i.command?.arguments,
      trigger: Trigger[i.command?.trigger ?? Trigger.UNSPECIFIED].toLowerCase(),
      started_ms: tsMillis(i.startTime),
      finished_ms: tsMillis(i.endTime),
      status: Status[i.status].toLowerCase(),
      magus_version: i.magusVersion,
      size_bytes: Number(i.sizeBytes),
    }));
  } catch {
    // reported: by the server transport
    return [];
  }
}

// fetchRunJournal reads one past run back as a magus.viewer.v1alpha1 Journal (binary protobuf) -
// the SAME message a `#data=` link carries, which is what lets a browsed run render structurally
// instead of through the text heuristic. Addressed by invocation id, or by an output ref (the
// server resolves it to the run that produced it). null on any failure, including the 404 a run
// whose journal has rotated away returns - the caller then falls back to the verbatim blob.
export async function fetchRunJournal(
  host: string,
  token: string | null,
  q: { inv?: string; ref?: string },
): Promise<Uint8Array | null> {
  try {
    const client = createClient(ViewerService, createServerTransport(host, token));
    const journal = await client.getJournal(
      { name: q.inv ?? q.ref ?? "" },
      { signal: AbortSignal.timeout(8000) },
    );
    return toBinary(JournalSchema, journal);
  } catch {
    // reported: by the server transport
    return null;
  }
}

// watchRuns keeps a run browser current without anyone pressing Refresh: it subscribes to the
// server's SSE stream and calls onChange whenever the store may have moved.
//
// It listens for `event: status` - the POOL's state, pushed on connect and on every change. That is
// the closest thing the server has to "a run finished": a target starting or ending moves the pool,
// and by the time the count drops its output has been persisted. There is no run-completed event to
// subscribe to instead, and inventing one to serve a sidebar would be a wire change for a want a
// change-notification already covers. The payload is ignored entirely - only the FACT that something
// moved matters here, which is also why this needs no protobuf decode.
//
// Both browsers call it, so "when does the list update" has one answer and one implementation.
// Returns a disposer. A caller with no server (the demo, an offline page) gets a no-op.
export function watchRuns(host: string, token: string | null, onChange: () => void): () => void {
  if (!host) return () => {};
  const abort = new AbortController();
  let settle: ReturnType<typeof setTimeout> | undefined;
  let retry: ReturnType<typeof setTimeout> | undefined;
  let backoffMs = 1000;
  // The first connect's status frame is the server saying hello, not news - the caller has just
  // loaded. Every LATER connect is a reconnect, where a refresh is exactly right: the stream was
  // down and whatever happened while it was is what this catches up on.
  let greeted = false;

  // One refresh per burst. A single `magus run` moves the pool several times in a second, and each
  // move would otherwise be its own pair of fetches and its own repaint under the reader's cursor.
  const schedule = (): void => {
    clearTimeout(settle);
    settle = setTimeout(onChange, 400);
  };

  const connect = (): void => {
    void fetchSSE(
      "http://" + host + "/api/v1/events",
      authHeaders(token),
      (type) => {
        if (type !== "status") return;
        if (!greeted) {
          greeted = true;
          return;
        }
        schedule();
      },
      () => {
        // Reconnect with backoff. A server that went away is the common case (a restart between
        // gates), and a browser tab that retried it in a tight loop would be the thing everyone
        // remembers about this page.
        if (abort.signal.aborted) return;
        retry = setTimeout(connect, backoffMs);
        backoffMs = Math.min(backoffMs * 2, 30_000);
      },
      abort.signal,
      () => {
        backoffMs = 1000;
        if (greeted) schedule(); // a reconnect: catch up on whatever the gap held
      },
    );
  };
  connect();
  return () => {
    abort.abort();
    clearTimeout(settle);
    clearTimeout(retry);
  };
}

// tickRelativeTimes keeps every "3m ago" on an app honest while nobody touches it.
//
// The labels are computed at paint time, and paint only happens when something changes - so a page
// left open showed the time it was opened at, indefinitely. Measured: a run stayed at "16s ago"
// minutes after the fact. Auto-refresh hid it further, by making the labels look live whenever a run
// happened to be in flight.
//
// It rewrites the TEXT of anything carrying data-time rather than re-rendering: a re-render every
// tick would drop focus off whatever node a keyboard reader was on, and reset a scroll position
// mid-read. 15s because relTime's finest step is seconds, and a minute-granular tick would leave
// "5s ago" standing for most of a minute.
export function tickRelativeTimes(root: HTMLElement, everyMs = 15_000): () => void {
  const id = setInterval(() => {
    const now = Date.now();
    for (const el of root.querySelectorAll<HTMLElement>("[data-time]")) {
      const ms = Number(el.dataset.time);
      if (ms) el.textContent = relTime(ms, now);
    }
  }, everyMs);
  return () => clearInterval(id);
}

// demoRuns projects the shared scenario's run history (demo-scenario.ts) into the tree's row shape
// for the server-free showcase (the shared #demo path), so the browser reads as populated without a
// server AND shows the SAME runs as the activity trail, the waterfall, and the dashboard - the refs
// here are the ones a reader meets on those apps. Newest first; timestamps relative to `now`.
export function demoRuns(now: number): RunSummary[] {
  return scenarioRuns(now).map((r) => ({
    ref: r.ref,
    project: r.project,
    target: r.target,
    inv: r.inv,
    failed: r.state === "failed",
    error: r.error,
    timestamp_ms: r.endMs,
    duration_ms: r.durationMs,
  }));
}

// demoRunLogs is the invocation half of the same showcase, projected from the shared scenario so the
// demo tree reads the way a real one does - a `magus affected ci` sweep with its targets under it,
// and the MCP-triggered runs each on their own.
export function demoRunLogs(now: number): RunLog[] {
  return scenarioInvocations(now).map((i) => ({
    inv: i.inv,
    arguments: i.arguments,
    trigger: i.trigger,
    started_ms: i.startMs,
    finished_ms: i.endMs,
    status: i.failed ? "fail" : "pass",
  }));
}

const svgNS = "http://www.w3.org/2000/svg";
// chevron is the PF tree-view node-toggle glyph. Exported so the activity index tree (which builds
// its own PF tree in activity/main.ts) reuses the SAME caret rather than duplicating the SVG.
export function chevron(): SVGElement {
  const s = document.createElementNS(svgNS, "svg");
  s.setAttribute("viewBox", "0 0 24 24");
  s.setAttribute("fill", "none");
  s.setAttribute("stroke", "currentColor");
  s.setAttribute("stroke-width", "2");
  s.setAttribute("stroke-linecap", "round");
  s.setAttribute("stroke-linejoin", "round");
  s.setAttribute("aria-hidden", "true");
  const p = document.createElementNS(svgNS, "polyline");
  p.setAttribute("points", "9 6 15 12 9 18");
  s.append(p);
  return s;
}

// TreeState survives a refresh: which branches the reader opened and which node is current. Without
// it a poll or a manual reload collapses the tree back to its defaults mid-read, which is the single
// most annoying thing a live-updating tree can do. Keyed by NodeSpec.id, which is derived from the
// ref/inv/path rather than from position, so it survives new rows arriving at the top.
interface TreeState {
  expanded: Set<string>;
  current: string | null;
}

// STATUS_OF maps a tree node's outcome onto the shared status mark and the one word for it. A branch
// where only some runs failed reads as a warning, not as either verdict.
const STATUS_OF: Record<NonNullable<NodeSpec["status"]>, { status: MarkStatus; word: string }> = {
  pass: { status: "success", word: "Passed" },
  fail: { status: "danger", word: "Failed" },
  mixed: { status: "warning", word: "Partly failed" },
};

function makeNode(spec: NodeSpec, ctx: TreeCtx): HTMLLIElement {
  const li = document.createElement("li");
  li.className = "pf-v6-c-tree-view__list-item";
  li.setAttribute("role", "treeitem");
  // Roving tabindex, on the item as PF's own markup has it: exactly one item in the tree is
  // tabbable and the arrow keys move which. A tree of 500 rows that each take a Tab stop is unusable
  // with a keyboard. The row button stays out of the tab order; the item is what takes focus.
  li.tabIndex = -1;
  const hasKids = !!spec.children && spec.children.length > 0;

  if (!hasKids) li.dataset.leaf = "";

  const content = document.createElement("div");
  content.className = "pf-v6-c-tree-view__content";
  const node = document.createElement("button");
  node.type = "button";
  node.className = "pf-v6-c-tree-view__node";
  node.dataset.nodeId = spec.id;
  node.tabIndex = -1;
  if (spec.title) node.title = spec.title;

  // PF's compact node: a container holding the toggle, a content column (a title over a text line)
  // and the count. The toggle sits INSIDE the container so the compact background wraps all of it.
  const container = document.createElement("span");
  container.className = "pf-v6-c-tree-view__node-container";
  if (hasKids) {
    const toggle = document.createElement("span");
    toggle.className = "pf-v6-c-tree-view__node-toggle";
    const ticon = document.createElement("span");
    ticon.className = "pf-v6-c-tree-view__node-toggle-icon";
    ticon.append(chevron());
    toggle.append(ticon);
    container.append(toggle);
  }
  const nodeContent = document.createElement("span");
  nodeContent.className = "pf-v6-c-tree-view__node-content";
  const title = document.createElement("span");
  title.className = "pf-v6-c-tree-view__node-title";
  // The outcome leads the title as a mark with a shape and a word. It rides INSIDE the title for
  // the same reason a dot used to: node-content is a column, so a sibling would take a row of its
  // own above the name it marks.
  if (spec.status) {
    const outcome = STATUS_OF[spec.status];
    title.append(statusMark(outcome.status, outcome.word));
  }
  // The label is its own element so the ticker can rewrite just it (see tickRelativeTimes)
  // without touching the mark or rebuilding the row. A relative label is a <time>.
  const labelEl = spec.timeMs ? timeEl(spec.timeMs, Date.now()) : document.createElement("span");
  labelEl.textContent = spec.label;
  title.append(labelEl);
  nodeContent.append(title);
  if (spec.description) {
    const desc = document.createElement("span");
    desc.className = "pf-v6-c-tree-view__node-text";
    desc.textContent = spec.description;
    nodeContent.append(desc);
  }
  container.append(nodeContent);
  if (spec.count != null) {
    const badge = document.createElement("span");
    badge.className = "pf-v6-c-tree-view__node-count";
    const b = document.createElement("span");
    b.className = "pf-v6-c-badge pf-m-read";
    b.textContent = String(spec.count);
    if (spec.countUnit) {
      const label = spec.count + " " + spec.countUnit + (spec.count === 1 ? "" : "s");
      b.title = label;
      b.setAttribute("aria-label", label);
    }
    badge.append(b);
    container.append(badge);
  }
  node.append(container);
  content.append(node);
  li.append(content);

  // aria-selected is what a screen reader hears; pf-m-current is the paint. Every item carries it,
  // as PF's single-select tree does, so a branch with nothing to open reads as unselected rather
  // than as outside the selection model.
  li.setAttribute("aria-selected", String(spec.id === ctx.state.current));
  if (spec.id === ctx.state.current) node.classList.add("pf-m-current");
  // Registered BEFORE the children below, so ctx.order comes out in PAINT order (an item, then its
  // subtree). The arrow keys walk that array as "the next visible item", which a post-order list -
  // every child ahead of its own parent - gets exactly backwards.
  ctx.order.push(li);

  const select = spec.select;
  const markCurrent = (): void => {
    ctx.state.current = spec.id;
    ctx.root.querySelectorAll(".pf-v6-c-tree-view__node.pf-m-current").forEach((n) => {
      n.classList.remove("pf-m-current");
      n.closest("li")?.setAttribute("aria-selected", "false");
    });
    node.classList.add("pf-m-current");
    li.setAttribute("aria-selected", "true");
  };

  if (hasKids) {
    const open = ctx.state.expanded.has(spec.id);
    li.setAttribute("aria-expanded", String(open));
    if (open) li.classList.add("pf-m-expanded");
    const group = document.createElement("ul");
    group.className = "pf-v6-c-tree-view__list";
    group.setAttribute("role", "group");
    for (const child of must(spec.children)) group.append(makeNode(child, ctx));
    li.append(group);
  }

  const activate = (): void => {
    if (!select) return;
    markCurrent();
    ctx.onSelect(select);
  };
  // An invocation is a branch AND a destination: one click both opens the run in the viewer and
  // reveals the targets it ran, because "what did that command do" and "which steps did it run"
  // are the same question asked at two zooms. A branch with nothing to open only toggles.
  node.addEventListener("click", () => {
    if (hasKids) setExpanded(li, !li.classList.contains("pf-m-expanded"), ctx, spec.id);
    activate();
  });
  li.addEventListener("keydown", (ev) => {
    // A nested item's keys bubble through its ancestors; only the item the key landed on answers.
    if ((ev.target as Element).closest("li") !== li) return;
    onTreeKey(ev, li, ctx, spec, hasKids, activate);
  });
  // A click or programmatic focus makes this item the tab stop, so Shift+Tab from the next control
  // returns to where the reader was.
  li.addEventListener("focusin", (ev) => {
    if ((ev.target as Element).closest("li") === li) seatTabStop(ctx, li);
  });
  return li;
}

function setExpanded(li: HTMLLIElement, open: boolean, ctx: TreeCtx, id: string): void {
  li.classList.toggle("pf-m-expanded", open);
  li.setAttribute("aria-expanded", String(open));
  if (open) ctx.state.expanded.add(id);
  else ctx.state.expanded.delete(id);
}

// onTreeKey implements the tree's keyboard contract (WAI-ARIA's, which PF's own TreeView follows):
// up/down walk the VISIBLE rows, right opens a branch then descends, left closes it then climbs,
// Home/End jump to the ends, Enter/Space select. ctx.order is the visible rows in paint order, so
// walking it is walking what the reader can see.
function onTreeKey(
  ev: Event,
  li: HTMLLIElement,
  ctx: TreeCtx,
  spec: NodeSpec,
  hasKids: boolean,
  activate: () => void,
): void {
  const k = (ev as KeyboardEvent).key;
  const visible = ctx.order.filter((n) => n.offsetParent !== null || n === li);
  const i = visible.indexOf(li);
  const focus = (n: HTMLLIElement | undefined): void => {
    if (!n) return;
    ev.preventDefault();
    seatTabStop(ctx, n);
    n.focus();
  };
  switch (k) {
    case "ArrowDown":
      focus(visible[i + 1]);
      break;
    case "ArrowUp":
      focus(visible[i - 1]);
      break;
    case "ArrowRight":
      if (hasKids && !li.classList.contains("pf-m-expanded")) {
        ev.preventDefault();
        setExpanded(li, true, ctx, spec.id);
      } else if (hasKids) {
        focus(visible[i + 1]);
      }
      break;
    case "ArrowLeft":
      if (hasKids && li.classList.contains("pf-m-expanded")) {
        ev.preventDefault();
        setExpanded(li, false, ctx, spec.id);
      } else {
        const parent = li.parentElement?.closest<HTMLLIElement>(".pf-v6-c-tree-view__list-item");
        focus(parent ?? undefined);
      }
      break;
    case "Home":
      focus(visible[0]);
      break;
    case "End":
      focus(visible[visible.length - 1]);
      break;
    case "Enter":
    case " ":
      // SELECT only, deliberately unlike the click. The arrow keys already own expansion here, so a
      // Enter that also toggled would collapse the branch the reader just opened with ArrowRight.
      ev.preventDefault();
      if (spec.select) activate();
      else if (hasKids) setExpanded(li, !li.classList.contains("pf-m-expanded"), ctx, spec.id);
      break;
    default:
      return;
  }
}

// TreeCtx is the per-render context makeNode threads down: where selections go, the state that
// survives the render, and the flat paint order the keyboard walks.
interface TreeCtx {
  root: HTMLElement;
  state: TreeState;
  order: HTMLLIElement[];
  onSelect: (sel: Selection) => void;
}

function seatTabStop(ctx: TreeCtx, item: HTMLLIElement): void {
  for (const it of ctx.order) it.tabIndex = it === item ? 0 : -1;
}

// renderRunTree (re)builds the tree into container from an already-grouped spec. emptyNote lets the
// caller explain WHY the panel is empty - no server, no stored runs, or a filter that matched
// nothing are three different states and only the caller can tell them apart.
export function renderRunTree(
  container: HTMLElement,
  specs: NodeSpec[],
  state: TreeState,
  onSelect: (sel: Selection) => void,
  emptyNote?: string | Node,
  label = "Recent runs",
): void {
  container.replaceChildren();
  if (specs.length === 0) {
    const empty = document.createElement("div");
    empty.className = "console-log-runs__empty";
    empty.append(emptyNote ?? "No stored runs. Run a target, then reopen this panel.");
    container.append(empty);
    return;
  }

  const tree = document.createElement("div");
  tree.className = "pf-v6-c-tree-view pf-m-compact pf-m-no-background pf-m-truncate";
  const list = document.createElement("ul");
  list.className = "pf-v6-c-tree-view__list";
  list.setAttribute("role", "tree");
  list.setAttribute("aria-label", label);
  const ctx: TreeCtx = { root: tree, state, order: [], onSelect };
  // The first branch opens on load so the newest run's targets are visible without a click. Only
  // on a FIRST paint: once the reader has opened or closed anything, their state is the answer.
  if (!state.expanded.size && specs.length && specs[0].children?.length) {
    state.expanded.add(specs[0].id);
  }
  for (const spec of specs) list.append(makeNode(spec, ctx));
  tree.append(list);
  container.append(tree);
  // Give the roving tabindex a home: the current node if it survived the refresh, else the first.
  const entry =
    ctx.order.find((n) => n.getAttribute("aria-selected") === "true") ?? ctx.order[0] ?? null;
  if (entry) entry.tabIndex = 0;
}

// iconButton builds a small plain PF button carrying one inline-SVG glyph (refresh, hide), matching
// the viewer's icon-button idiom without pulling a component. paths are <path>/<polyline> d-strings.
function iconButton(
  id: string,
  label: string,
  title: string,
  paths: readonly string[],
): HTMLButtonElement {
  const b = document.createElement("button");
  if (id) b.id = id;
  b.type = "button";
  b.className = "pf-v6-c-button pf-m-plain pf-m-small";
  b.title = title;
  b.setAttribute("aria-label", label);
  const s = document.createElementNS(svgNS, "svg");
  s.setAttribute("viewBox", "0 0 24 24");
  s.setAttribute("width", "16");
  s.setAttribute("height", "16");
  s.setAttribute("fill", "none");
  s.setAttribute("stroke", "currentColor");
  s.setAttribute("stroke-width", "2");
  s.setAttribute("stroke-linecap", "round");
  s.setAttribute("stroke-linejoin", "round");
  s.setAttribute("aria-hidden", "true");
  for (const d of paths) {
    const p = document.createElementNS(svgNS, "path");
    p.setAttribute("d", d);
    s.append(p);
  }
  b.append(s);
  return b;
}

// A collapsible master panel docked down the left of a render app's scroll box: a titled header
// (refresh + hide icons) over a caller-filled tree, plus a slim reopen rail. The log viewer's run
// browser and the activity view's event index are the same frame (both sheets import
// styles/layouts/Frame/frame.css, so both reuse the .console-log-runs styles); only what fills treeBox differs.
export interface CollapsiblePanel {
  head: HTMLElement; // the header row, so a caller can inject extra chrome (e.g. a count)
  // The BODY's header, the index header's opposite number across the splitter. It exists so the two
  // rules land on one line: the index header ended in a hairline that stopped dead at the splitter
  // with nothing continuing it, which read as a line drawn halfway across the app (measured at
  // 8.2px above where the body's first row ended). bodyTitle is the text in it - callers write what
  // the body is currently showing, so the row earns its height instead of being spacing in disguise.
  bodyHead: HTMLElement;
  bodyTitle: HTMLElement;
  // A slot after the title for what identifies the body's content (the log viewer's reference id).
  // Hidden until a caller fills it.
  bodyMeta: HTMLElement;
  treeBox: HTMLElement; // the caller (re)renders its tree into this
  refreshBtn: HTMLButtonElement;
  // The slim reopen rail shown while the panel is auto-collapsed on a phone. head's own chrome
  // (including anything a caller injected into it, like a count) is hidden along with the rest
  // of the aside in that state - this is the one element still on screen, so it is the caller's
  // only way to keep a count reachable without the tree.
  reopen: HTMLButtonElement;
  // applyDefault sets the open state after a (re)load from whether the panel now has content: an
  // empty panel collapses (to the rail, or fully hidden when hideWhenEmpty), a populated one opens -
  // except in a narrow pane, where an open aside would crush the content, so it starts collapsed to
  // the rail. A reader who opens it from the rail overrides that, and the choice sticks across loads.
  applyDefault: (hasContent: boolean) => void;
  // open shows the panel as if the reader had pressed the reopen rail.
  open: () => void;
  // dispose stops watching the pane's width. Call it when the app deactivates.
  dispose: () => void;
}

// NARROW_PX is the pane width below which the open aside floats over the content with a scrim
// instead of sitting beside it. It is the same 48rem the container query in frame.css uses.
const NARROW_PX = 768;

// mountCollapsiblePanel reparents `scroll` into a flex split and docks the collapsible aside to its
// left (so no scaffold markup changes). onRefresh fires on the header refresh click. hideWhenEmpty
// picks the empty behavior: the activity index hides entirely (its own empty-state card explains the
// cold state), while the run browser keeps the rail so a reader can open it to an honest note.
export function mountCollapsiblePanel(opts: {
  scroll: HTMLElement;
  title: string;
  label: string;
  bodyTitle: string; // the body header's resting text, before a caller names what is on screen
  onRefresh: () => void;
  hideWhenEmpty: boolean;
}): CollapsiblePanel | null {
  const parent = opts.scroll.parentElement;
  if (!parent) return null;

  const split = document.createElement("div");
  split.className = "console-log-split";
  parent.insertBefore(split, opts.scroll);

  const aside = document.createElement("aside");
  aside.className = "console-log-runs";
  aside.dataset.controlSize = "compact";
  aside.hidden = true;
  aside.setAttribute("aria-label", opts.label);

  const head = document.createElement("div");
  head.className = "console-log-runs__head";
  const title = document.createElement("span");
  title.className = "console-log-runs__title";
  title.textContent = opts.title;
  const refreshBtn = iconButton("", "Refresh", "Refresh", REFRESH);
  const hideBtn = iconButton("", "Hide the panel", "Hide the panel", ["M15 18l-6-6 6-6"]);
  head.append(title, refreshBtn, hideBtn);

  const treeBox = document.createElement("div");
  treeBox.className = "console-log-runs__tree";
  aside.append(head, treeBox);

  const reopen = iconButton("", "Show the panel", "Show the panel", ["M9 18l6-6-6-6"]);
  reopen.classList.add("console-log-runs__reopen");
  reopen.hidden = true;

  // The body column: a header row over the scroller, so the scroller keeps scrolling under a header
  // that stays put. scroll cannot simply gain a border-top - it scrolls, and the line would go with it.
  const body = document.createElement("div");
  body.className = "console-log-body";
  const bodyHead = document.createElement("div");
  bodyHead.className = "console-log-body__head";
  const bodyTitle = document.createElement("span");
  bodyTitle.className = "console-log-body__title";
  bodyTitle.textContent = opts.bodyTitle;
  const bodyMeta = document.createElement("span");
  bodyMeta.className = "console-log-body__meta";
  bodyMeta.hidden = true;
  bodyHead.append(bodyTitle, bodyMeta);
  body.append(bodyHead, opts.scroll);

  // Behind the aside while it floats over the content: a tap anywhere outside the panel closes it.
  const scrim = document.createElement("button");
  scrim.type = "button";
  scrim.className = "console-log-split__scrim";
  scrim.tabIndex = -1;
  scrim.setAttribute("aria-label", "Close " + opts.label.toLowerCase());
  scrim.hidden = true;

  split.append(aside, reopen, scrim, body);

  // The open state is JS-driven (the hidden attribute). Whether the aside is floating is a question
  // about the PANE, not the window, so it is asked of the split's own width; frame.css asks the same
  // question of the same element with a container query.
  let narrow = false;
  // What the reader last decided, which outranks the width default in both directions. Null means
  // they have not touched it and the width still decides.
  let userChoice: "open" | "closed" | null = null;
  let hasContent = false;
  let state: "open" | "closed" | "hidden" = "hidden";
  const apply = (next: "open" | "closed" | "hidden"): void => {
    state = next;
    aside.hidden = next !== "open";
    reopen.hidden = next !== "closed";
    scrim.hidden = !(next === "open" && narrow);
  };
  const applyDefault = (): void => {
    if (!hasContent) {
      apply(opts.hideWhenEmpty ? "hidden" : "closed");
      return;
    }
    apply(userChoice ?? (narrow ? "closed" : "open"));
  };
  // The floating aside takes focus when the reader opens it and gives it back when it closes, so a
  // keyboard reader is neither left behind the panel nor stranded on a button that has gone.
  const close = (restoreFocus: boolean): void => {
    userChoice = "closed";
    apply("closed");
    if (restoreFocus) reopen.focus();
  };
  hideBtn.addEventListener("click", () => close(true));
  scrim.addEventListener("click", () => close(true));
  aside.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape" && narrow && state === "open") {
      ev.stopPropagation();
      close(true);
    }
  });
  const open = (): void => {
    userChoice = "open";
    apply("open");
    if (narrow) {
      (
        aside.querySelector<HTMLElement>('.console-log-runs__tree li[tabindex="0"]') ??
        aside.querySelector<HTMLElement>("input, button")
      )?.focus();
    }
  };
  reopen.addEventListener("click", open);
  refreshBtn.addEventListener("click", opts.onRefresh);

  // The width default is re-evaluated whenever the pane's width crosses the line, not sampled once.
  // Sampled once, a pane that happened to be narrow while the app booted left the panel collapsed
  // for the rest of the session. An explicit open or close still wins, so this only decides for a
  // reader who has not.
  let observer: ResizeObserver | null = null;
  if (typeof ResizeObserver !== "undefined") {
    observer = new ResizeObserver((entries) => {
      const width = entries[entries.length - 1]?.contentRect.width ?? 0;
      // A pane with no width yet (still hidden) says nothing about the layout.
      if (width === 0) return;
      const next = width < NARROW_PX;
      if (next === narrow) return;
      narrow = next;
      applyDefault();
    });
    observer.observe(split);
  }

  return {
    head,
    bodyHead,
    bodyTitle,
    bodyMeta,
    treeBox,
    refreshBtn,
    reopen,
    applyDefault: (content: boolean): void => {
      hasContent = content;
      applyDefault();
    },
    open,
    dispose: (): void => observer?.disconnect(),
  };
}

// browseModeCell remembers which ordering the reader last chose, so reopening the viewer resumes
// their lens rather than snapping back to the default.
const browseModeCell = persisted<BrowseMode>("logs-run-browse-mode", "runs");

// mountBrowserControls adds the two controls the panel needs to be usable without a ref: the
// ordering toggle and the filter box. They STACK under the header rather than sharing its row -
// the aside is a rail, and PF's control scales only disagree when two controls sit on one line.
function mountBrowserControls(
  panel: CollapsiblePanel,
  onChange: () => void,
): {
  mode: () => BrowseMode;
  query: () => string;
  clear: () => void;
  setResults: (visible: string, spoken: string) => void;
  dispose: () => void;
} {
  const bar = document.createElement("div");
  bar.className = "console-log-runs__controls";
  // The rail's two controls sit directly above a dense tree, so they take the compact tier of the
  // shared control height rather than each picking its own - and with it the coarse-pointer floor,
  // which is what makes them touchable on a phone.
  bar.dataset.controlSize = "compact";

  const group = document.createElement("div");
  group.className = "pf-v6-c-toggle-group console-log-runs__modes";
  group.setAttribute("role", "group");
  group.setAttribute("aria-label", "Group runs by");
  const modes: { id: BrowseMode; label: string; title: string }[] = [
    { id: "runs", label: "Runs", title: "Group by the command that produced them" },
    { id: "projects", label: "Projects", title: "Group by project, then target" },
  ];
  let mode = browseModeCell.get();
  const buttons = new Map<BrowseMode, HTMLButtonElement>();
  for (const m of modes) {
    const item = document.createElement("div");
    item.className = "pf-v6-c-toggle-group__item";
    const b = document.createElement("button");
    b.type = "button";
    b.className = "pf-v6-c-toggle-group__button";
    b.title = m.title;
    b.setAttribute("aria-pressed", String(m.id === mode));
    if (m.id === mode) b.classList.add("pf-m-selected");
    const t = document.createElement("span");
    t.className = "pf-v6-c-toggle-group__text";
    t.textContent = m.label;
    b.append(t);
    b.addEventListener("click", () => {
      if (mode === m.id) return;
      mode = m.id;
      browseModeCell.set(mode);
      for (const [id, btn] of buttons) {
        btn.classList.toggle("pf-m-selected", id === mode);
        btn.setAttribute("aria-pressed", String(id === mode));
      }
      onChange();
    });
    buttons.set(m.id, b);
    item.append(b);
    group.append(item);
  }

  // The keys are the same shape the log filter uses, so a reader learns one grammar for the app; the
  // placeholder stays short and the syntax lives behind the "?".
  const field = createFilterField({
    label: "Filter runs",
    placeholder: "Filter runs",
    onChange: () => onChange(),
  });
  const help = createHelpButton("Run filter syntax");
  const disposeHelp = attachHelpPopover(help, {
    text:
      "Free text matches the project, target, ref, error and command line. " +
      "Keys: project: target: status:pass|fail trigger: ref: cmd:",
    label: "Run filter syntax",
  });
  const searchRow = document.createElement("div");
  searchRow.className = "console-log-runs__search";
  searchRow.append(field.el, help);

  bar.append(group, searchRow);
  panel.head.insertAdjacentElement("afterend", bar);
  return {
    mode: () => mode,
    query: () => field.value(),
    clear: () => {
      field.setValue("");
      onChange();
    },
    setResults: field.setResults,
    dispose: () => {
      field.dispose();
      disposeHelp();
    },
  };
}

// initRunBrowser docks the run browser to the left of the viewer's scroll box and populates it: it
// fetches both feeds (or, in #demo, the synthetic set), groups them into the chosen ordering, and
// renders the tree; selecting a row calls deps.onSelect. Demo rows appear ONLY in explicit demo
// mode - with no server and no demo it fetches nothing (a fresh install must not show fabricated
// runs as if real) and the reopen rail opens to an honest note. Returns a refresh handle the viewer
// can call (e.g. after a live run finishes), and a setBodyTitle handle for naming what is loaded.
export interface RunBrowserHandle {
  refresh: () => void;
  setBodyTitle: (text: string) => void;
  // The slot after the body title, for the loaded output's reference id.
  bodyMeta: HTMLElement | null;
  // Shows the panel, for an empty state that points at it.
  open: () => void;
  dispose: () => void;
}

export function initRunBrowser(deps: RunBrowserDeps): RunBrowserHandle {
  const panel = mountCollapsiblePanel({
    scroll: deps.scroll,
    title: "Recent runs",
    label: "Recent runs",
    bodyTitle: "Output",
    onRefresh: () => {
      void load();
    },
    hideWhenEmpty: false,
  });
  if (!panel) {
    return {
      refresh: () => {},
      setBodyTitle: () => {},
      bodyMeta: null,
      open: () => {},
      dispose: () => {},
    };
  }
  const runsPanel = panel;
  // One state object for the panel's lifetime: which branches are open and which row is current
  // survive a refresh, a filter change and a mode switch.
  const treeState: TreeState = { expanded: new Set(), current: null };
  let runs: RunSummary[] = [];
  let logs: RunLog[] = [];
  let loaded = false;
  let unreachable = false;
  let loadGeneration = 0;

  const controls = mountBrowserControls(runsPanel, () => paint());

  function paint(): void {
    const filter = parseRunFilter(controls.query());
    const now = deps.nowMs();
    const specs = buildRunTree({ runs, logs, mode: controls.mode(), filter, now });
    renderRunTree(runsPanel.treeBox, specs, treeState, deps.onSelect, emptyNote(filter.empty));
    countBadge.textContent = specs.length ? String(specs.length) : "";
    // The filter says what it left. Top-level rows are compared with the unfiltered tree's, so
    // "3 of 12" and the badge beside the title agree about what a row is.
    if (filter.empty || !loaded) {
      controls.setResults("", "");
      return;
    }
    const total = buildRunTree({
      runs,
      logs,
      mode: controls.mode(),
      filter: parseRunFilter(""),
      now,
    }).length;
    controls.setResults(
      specs.length + " of " + total,
      specs.length === 0
        ? "No runs match the filter"
        : specs.length + " of " + total + " runs match the filter",
    );
  }

  // The empty states are different facts, and only one of them is a problem the reader can act on.
  // Saying "no stored runs" to someone whose filter simply matched nothing is the version that
  // reads as data loss. The connection states are short here because the viewer's own empty state
  // carries the full prompt; the two must never disagree about the server.
  function emptyNote(unfiltered: boolean): string | Node {
    if (!deps.host && !deps.demo) return "No server connected.";
    if (!loaded) return "Loading runs...";
    if (unreachable) return "Could not reach the server.";
    if (!unfiltered) {
      const note = document.createElement("span");
      const text = document.createElement("span");
      text.textContent = "No runs match this filter.";
      const clear = document.createElement("button");
      clear.type = "button";
      clear.className = "pf-v6-c-button pf-m-link";
      clear.textContent = "Clear filter";
      clear.addEventListener("click", controls.clear);
      note.append(text, clear);
      return note;
    }
    return "No runs kept yet. Run a target, then refresh.";
  }

  // How many top-level rows are showing, beside the title - which is also how a filter reports that
  // it narrowed something, since the rows it removed leave no other trace.
  const countBadge = document.createElement("span");
  countBadge.className = "console-log-runs__count";
  runsPanel.head.insertBefore(countBadge, runsPanel.head.children[1] ?? null);

  // The demo scenario is written relative to ONE instant, so it is stamped once here rather than
  // re-derived on every load - re-stamping would slide every demo run forward on each refresh.
  const demoNow = deps.nowMs();

  async function load(): Promise<void> {
    const generation = ++loadGeneration;
    let nextRuns: RunSummary[] = [];
    let nextLogs: RunLog[] = [];
    let nextUnreachable = false;
    if (deps.demo) {
      nextRuns = demoRuns(demoNow);
      nextLogs = demoRunLogs(demoNow);
    } else if (deps.host) {
      // Both feeds at once: they are independent reads and the tree needs both to group by run.
      [nextRuns, nextLogs] = await Promise.all([
        fetchRuns(deps.host, deps.token),
        fetchRunLogs(deps.host, deps.token),
      ]);
      // The feeds read a refused connection as an empty list, so nothing answering at the address
      // is asked separately: "nothing kept yet" would send the reader to run a target.
      if (nextRuns.length === 0 && nextLogs.length === 0) {
        nextUnreachable = !(await probeServer(deps.host)).ok;
      }
    }
    // A newer load, or a closed panel, owns the answer now.
    if (disposed || generation !== loadGeneration) return;
    runs = nextRuns;
    logs = nextLogs;
    unreachable = nextUnreachable;
    loaded = true;
    paint();
    runsPanel.applyDefault(runs.length > 0 || logs.length > 0);
    deps.onState?.({ loaded, runs: buildRunRows(runs, logs, true).length, unreachable });
  }

  let disposed = false;
  paint();
  void load();
  return {
    bodyMeta: runsPanel.bodyMeta,
    open: runsPanel.open,
    dispose: () => {
      disposed = true;
      runsPanel.dispose();
      controls.dispose();
    },
    refresh: () => {
      void load();
    },
    // The body header names what is on screen, which is the job CollapsiblePanel.bodyTitle exists
    // for. Its resting "Output" says nothing once a specific run is loaded, and the tree's own
    // current-row highlight scrolls out of view in a long list. The VIEWER drives it rather than the
    // click handler here, so a run opened from a #inv= link is named the same as one clicked.
    setBodyTitle: (text: string): void => {
      runsPanel.bodyTitle.textContent = text || "Output";
    },
  };
}
