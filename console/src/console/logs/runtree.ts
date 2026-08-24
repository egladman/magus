import { must } from "../../lib/guards";
// runtree.ts - the Log Viewer's run browser: a PatternFly TreeView down the left of the viewer
// that lists prior runs so a reader can find one WITHOUT a ref somebody printed on a terminal.
// Two orderings of the same rows, switched in the panel header: "Runs" nests invocation ->
// target (the command you actually typed, newest first) and "Projects" nests project -> target ->
// run. A filter box narrows both.
//
// It reads two read-only daemon feeds - /api/v1/outputs (the retained per-target outputs) and
// /api/v1/runs (the retained invocation journals) - and joins them on each output's invocation id.
// On selection it hands the choice to the viewer, which loads that run's journal from
// /api/v1/run and renders it structurally, falling back to the verbatim blob at /api/v1/output
// when the journal has rotated away. The whole browser is a no-op with no reachable daemon (the
// tree stays empty and says so), so the offline #data/#src paths are unaffected. PF owns the tree
// chrome (pf-v6-c-tree-view); only the panel frame, the filter row, and the status dot are ours.
//
// This file is the DOM half. The grouping, the filter grammar, and the row shapes are in
// runindex.ts, which has no DOM dependency and carries the unit tests.

import { authHeaders, fetchSSE } from "../../lib/daemon";
import { persisted } from "../../lib/persist";
import { scenarioInvocations, scenarioRuns } from "../demo-scenario";
import {
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
// (the tree docks to its left, sharing the panel below the toolbar). host/token address the daemon
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
}

// fetchRuns reads the daemon's run list. Resolves to [] on any failure (no daemon, auth, an old
// daemon without the route) so the browser degrades to empty rather than throwing - the viewer's
// other load paths never depend on it.
export async function fetchRuns(host: string, token: string | null): Promise<RunSummary[]> {
  try {
    const res = await fetch("http://" + host + "/api/v1/outputs", {
      headers: authHeaders(token),
      cache: "no-store",
      signal: AbortSignal.timeout(4000),
    });
    if (!res.ok) return [];
    const body = (await res.json()) as { outputs?: RunSummary[] };
    return Array.isArray(body.outputs) ? body.outputs : [];
  } catch {
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
    const res = await fetch("http://" + host + "/api/v1/output?ref=" + encodeURIComponent(ref), {
      headers: authHeaders(token),
      cache: "no-store",
      signal: AbortSignal.timeout(8000),
    });
    if (!res.ok) return null;
    return await res.text();
  } catch {
    return null;
  }
}

// fetchRunLogs reads the daemon's INVOCATION feed - the retained run journals, newest first. Same
// degradation as fetchRuns: [] on any failure, including a daemon too old to serve the route, so a
// mixed-version pair falls back to the project ordering rather than showing an error.
export async function fetchRunLogs(host: string, token: string | null): Promise<RunLog[]> {
  try {
    const res = await fetch("http://" + host + "/api/v1/runs", {
      headers: authHeaders(token),
      cache: "no-store",
      signal: AbortSignal.timeout(4000),
    });
    if (!res.ok) return [];
    const body = (await res.json()) as { runs?: RunLog[] };
    return Array.isArray(body.runs) ? body.runs : [];
  } catch {
    return [];
  }
}

// fetchRunJournal reads one past run back as a magus.viewer.v1alpha1 Journal (binary protobuf) -
// the SAME message a `#data=` link carries, which is what lets a browsed run render structurally
// instead of through the text heuristic. Addressed by invocation id, or by an output ref (the
// daemon resolves it to the run that produced it). null on any failure, including the 404 a run
// whose journal has rotated away returns - the caller then falls back to the verbatim blob.
export async function fetchRunJournal(
  host: string,
  token: string | null,
  q: { inv?: string; ref?: string },
): Promise<Uint8Array | null> {
  const param = q.inv
    ? "inv=" + encodeURIComponent(q.inv)
    : "ref=" + encodeURIComponent(q.ref ?? "");
  try {
    const res = await fetch("http://" + host + "/api/v1/run?" + param, {
      headers: authHeaders(token),
      cache: "no-store",
      signal: AbortSignal.timeout(8000),
    });
    if (!res.ok) return null;
    return new Uint8Array(await res.arrayBuffer());
  } catch {
    return null;
  }
}

// watchRuns keeps a run browser current without anyone pressing Refresh: it subscribes to the
// daemon's SSE stream and calls onChange whenever the store may have moved.
//
// It listens for `event: status` - the POOL's state, pushed on connect and on every change. That is
// the closest thing the daemon has to "a run finished": a target starting or ending moves the pool,
// and by the time the count drops its output has been persisted. There is no run-completed event to
// subscribe to instead, and inventing one to serve a sidebar would be a wire change for a want a
// change-notification already covers. The payload is ignored entirely - only the FACT that something
// moved matters here, which is also why this needs no protobuf decode.
//
// Both browsers call it, so "when does the list update" has one answer and one implementation.
// Returns a disposer. A caller with no daemon (the demo, an offline page) gets a no-op.
export function watchRuns(host: string, token: string | null, onChange: () => void): () => void {
  if (!host) return () => {};
  const abort = new AbortController();
  let settle: ReturnType<typeof setTimeout> | undefined;
  let retry: ReturnType<typeof setTimeout> | undefined;
  let backoffMs = 1000;
  // The first connect's status frame is the daemon saying hello, not news - the caller has just
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
        // Reconnect with backoff. A daemon that went away is the common case (a restart between
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

// tickRelativeTimes keeps every "3m ago" on a surface honest while nobody touches it.
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
// for the daemon-free showcase (the shared #demo path), so the browser reads as populated without a
// daemon AND tells the SAME story as the activity trail, the waterfall, and the dashboard - the refs
// here are the ones a reader meets on those surfaces. Newest first; timestamps relative to `now`.
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
// and the agent-driven runs each on their own.
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

function makeNode(spec: NodeSpec, ctx: TreeCtx): HTMLLIElement {
  const li = document.createElement("li");
  li.className = "pf-v6-c-tree-view__list-item";
  li.setAttribute("role", "treeitem");
  const hasKids = !!spec.children && spec.children.length > 0;

  const content = document.createElement("div");
  content.className = "pf-v6-c-tree-view__content";
  const node = document.createElement("button");
  node.type = "button";
  node.className = "pf-v6-c-tree-view__node";
  node.dataset.nodeId = spec.id;
  // Roving tabindex: exactly one node in the tree is tabbable and the arrow keys move which. A tree
  // of 500 buttons that each take a Tab stop is unusable with a keyboard, and PF's own TreeView
  // makes the same trade.
  node.tabIndex = -1;
  if (spec.title) node.title = spec.title;

  if (hasKids) {
    const toggle = document.createElement("span");
    toggle.className = "pf-v6-c-tree-view__node-toggle";
    const ticon = document.createElement("span");
    ticon.className = "pf-v6-c-tree-view__node-toggle-icon";
    ticon.append(chevron());
    toggle.append(ticon);
    node.append(toggle);
  }

  const container = document.createElement("span");
  container.className = "pf-v6-c-tree-view__node-container";
  const nodeContent = document.createElement("span");
  nodeContent.className = "pf-v6-c-tree-view__node-content";
  const text = document.createElement("span");
  text.className = "pf-v6-c-tree-view__node-text";
  // The dot goes INSIDE the text node, not beside it. PatternFly's __node-content is
  // display:flex/column - it stacks a label above a description - so a dot appended as a
  // sibling of the label lands on its own line above it. Inside the label it rides the
  // same nowrap line, which is what a status dot is for.
  if (spec.status) {
    const dot = document.createElement("span");
    dot.className = "console-log-runs__dot";
    dot.dataset.status = spec.status;
    text.append(dot);
  }
  // The label goes in its OWN span rather than as a bare text node beside the dot, so the ticker can
  // rewrite just this (see tickRelativeTimes) without touching the dot or rebuilding the row.
  const labelEl = document.createElement("span");
  labelEl.textContent = spec.label;
  if (spec.timeMs) labelEl.dataset.time = String(spec.timeMs);
  text.append(labelEl);
  nodeContent.append(text);
  if (spec.description) {
    const desc = document.createElement("span");
    desc.className = "pf-v6-c-tree-view__node-description";
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

  if (spec.id === ctx.state.current) node.classList.add("pf-m-current");
  // Registered BEFORE the children below, so ctx.order comes out in PAINT order (a row, then its
  // subtree). The arrow keys walk that array as "the next visible row", which a post-order list -
  // every child ahead of its own parent - gets exactly backwards.
  ctx.order.push(node);

  const select = spec.select;
  const markCurrent = (): void => {
    ctx.state.current = spec.id;
    ctx.root
      .querySelectorAll(".pf-v6-c-tree-view__node.pf-m-current")
      .forEach((n) => n.classList.remove("pf-m-current"));
    node.classList.add("pf-m-current");
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
  node.addEventListener("keydown", (ev) => onTreeKey(ev, li, node, ctx, spec, hasKids, activate));
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
  node: HTMLButtonElement,
  ctx: TreeCtx,
  spec: NodeSpec,
  hasKids: boolean,
  activate: () => void,
): void {
  const k = (ev as KeyboardEvent).key;
  const visible = ctx.order.filter((n) => n.offsetParent !== null || n === node);
  const i = visible.indexOf(node);
  const focus = (n: HTMLButtonElement | undefined): void => {
    if (!n) return;
    ev.preventDefault();
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
        focus(parent?.querySelector<HTMLButtonElement>(".pf-v6-c-tree-view__node") ?? undefined);
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
  order: HTMLButtonElement[];
  onSelect: (sel: Selection) => void;
}

// renderRunTree (re)builds the tree into container from an already-grouped spec. emptyNote lets the
// caller explain WHY the panel is empty - no daemon, no stored runs, or a filter that matched
// nothing are three different states and only the caller can tell them apart.
export function renderRunTree(
  container: HTMLElement,
  specs: NodeSpec[],
  state: TreeState,
  onSelect: (sel: Selection) => void,
  emptyNote?: string,
): void {
  container.replaceChildren();
  if (specs.length === 0) {
    const empty = document.createElement("p");
    empty.className = "console-log-runs__empty";
    empty.textContent = emptyNote ?? "No stored runs. Run a target, then reopen this panel.";
    container.append(empty);
    return;
  }

  const tree = document.createElement("div");
  tree.className = "pf-v6-c-tree-view pf-m-guides";
  const list = document.createElement("ul");
  list.className = "pf-v6-c-tree-view__list";
  list.setAttribute("role", "tree");
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
  const entry = ctx.order.find((n) => n.classList.contains("pf-m-current")) ?? ctx.order[0] ?? null;
  if (entry) entry.tabIndex = 0;
}

// iconButton builds a small plain PF button carrying one inline-SVG glyph (refresh, hide), matching
// the viewer's icon-button idiom without pulling a component. paths are <path>/<polyline> d-strings.
function iconButton(id: string, label: string, title: string, paths: string[]): HTMLButtonElement {
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

// A collapsible master panel docked down the left of a render surface's scroll box: a titled header
// (refresh + hide icons) over a caller-filled tree, plus a slim reopen rail. The log viewer's run
// browser and the activity view's event index are the same frame (both load logs.css, so both reuse
// the .console-log-runs styles); only what fills treeBox differs.
export interface CollapsiblePanel {
  head: HTMLElement; // the header row, so a caller can inject extra chrome (e.g. a count)
  // The BODY's header, the index header's opposite number across the splitter. It exists so the two
  // rules land on one line: the index header ended in a hairline that stopped dead at the splitter
  // with nothing continuing it, which read as a line drawn halfway across the surface (measured at
  // 8.2px above where the body's first row ended). bodyTitle is the text in it - callers write what
  // the body is currently showing, so the row earns its height instead of being spacing in disguise.
  bodyHead: HTMLElement;
  bodyTitle: HTMLElement;
  treeBox: HTMLElement; // the caller (re)renders its tree into this
  refreshBtn: HTMLButtonElement;
  // The slim reopen rail shown while the panel is auto-collapsed on a phone. head's own chrome
  // (including anything a caller injected into it, like a count) is hidden along with the rest
  // of the aside in that state - this is the one element still on screen, so it is the caller's
  // only way to keep a count reachable without the tree.
  reopen: HTMLButtonElement;
  // applyDefault sets the open state after a (re)load from whether the panel now has content: an
  // empty panel collapses (to the rail, or fully hidden when hideWhenEmpty), a populated one opens -
  // except on a phone, where an open aside would crush the content pane, so it starts collapsed to
  // the rail. A reader who opens it from the rail overrides that, and the choice sticks across loads.
  applyDefault: (hasContent: boolean) => void;
}

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
  aside.hidden = true;
  aside.setAttribute("aria-label", opts.label);

  const head = document.createElement("div");
  head.className = "console-log-runs__head";
  const title = document.createElement("span");
  title.className = "console-log-runs__title";
  title.textContent = opts.title;
  const refreshBtn = iconButton("", "Refresh", "Refresh", [
    "M21 12a9 9 0 1 1-2.64-6.36",
    "M21 3v6h-6",
  ]);
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
  bodyHead.append(bodyTitle);
  body.append(bodyHead, opts.scroll);

  split.append(aside, reopen, body);

  // The open state is JS-driven (the hidden attribute), so the phone default lives here (matchMedia)
  // rather than duplicated into logs.css - the same breakpoint the app shell uses (console.css).
  const narrow = window.matchMedia("(max-width: 47.999rem)");
  // What the reader last decided, which outranks the width default in both directions. Null means
  // they have not touched it and the width still decides.
  let userChoice: "open" | "closed" | null = null;
  let hasContent = false;
  const apply = (state: "open" | "closed" | "hidden"): void => {
    aside.hidden = state !== "open";
    reopen.hidden = state !== "closed";
  };
  const applyDefault = (): void => {
    if (!hasContent) {
      apply(opts.hideWhenEmpty ? "hidden" : "closed");
      return;
    }
    apply(userChoice ?? (narrow.matches ? "closed" : "open"));
  };
  hideBtn.addEventListener("click", () => {
    userChoice = "closed";
    apply("closed");
  });
  reopen.addEventListener("click", () => {
    userChoice = "open";
    apply("open");
  });
  // The width default is re-evaluated on a breakpoint FLIP, not sampled once. Sampled once, a
  // window that happened to be narrow while the surface booted left the panel collapsed for the
  // rest of the session, and a phone rotated to landscape never got it back - both indistinguishable
  // from the panel simply not existing. An explicit open or close still wins, so this only decides
  // for a reader who has not.
  narrow.addEventListener("change", applyDefault);
  refreshBtn.addEventListener("click", opts.onRefresh);

  return {
    head,
    bodyHead,
    bodyTitle,
    treeBox,
    refreshBtn,
    reopen,
    applyDefault: (content: boolean): void => {
      hasContent = content;
      applyDefault();
    },
  };
}

// browseModeCell remembers which ordering the reader last chose, so reopening the viewer resumes
// their lens rather than snapping back to the default.
const browseModeCell = persisted<BrowseMode>("logs-run-browse-mode", "runs");

// mountBrowserControls adds the two controls the panel needs to be usable without a ref: the
// ordering toggle and the filter box. They STACK under the header rather than sharing its row -
// the aside is a rail, and the size-modifier note at the top of logs.css applies here too:
// controls only mismatch when they sit on one line.
function mountBrowserControls(
  panel: CollapsiblePanel,
  onChange: () => void,
): { mode: () => BrowseMode; query: () => string } {
  const bar = document.createElement("div");
  bar.className = "console-log-runs__controls";

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

  const search = document.createElement("input");
  search.type = "search";
  search.className = "pf-v6-c-form-control console-log-runs__filter";
  search.placeholder = "Filter runs";
  // The keys are the same shape the log filter uses, so a reader learns one grammar for the
  // surface; the placeholder stays short and the syntax lives in the tooltip.
  search.title =
    "Filter the tree. Free text matches the project, target, ref, error and command line. " +
    "Keys: project: target: status:pass|fail trigger: ref: cmd:";
  search.setAttribute("aria-label", "Filter runs");
  let debounce: ReturnType<typeof setTimeout>;
  search.addEventListener("input", () => {
    clearTimeout(debounce);
    debounce = setTimeout(onChange, 140);
  });
  search.addEventListener("keydown", (ev) => {
    if ((ev as KeyboardEvent).key === "Escape" && search.value) {
      ev.stopPropagation();
      search.value = "";
      onChange();
    }
  });

  bar.append(group, search);
  panel.head.insertAdjacentElement("afterend", bar);
  return {
    mode: () => mode,
    query: () => search.value,
  };
}

// initRunBrowser docks the run browser to the left of the viewer's scroll box and populates it: it
// fetches both feeds (or, in #demo, the synthetic set), groups them into the chosen ordering, and
// renders the tree; selecting a row calls deps.onSelect. Demo rows surface ONLY in explicit demo
// mode - with no daemon and no demo it fetches nothing (a fresh install must not show fabricated
// runs as if real) and the reopen rail opens to an honest note. Returns a refresh handle the viewer
// can call (e.g. after a live run finishes), and a setBodyTitle handle for naming what is loaded.
export function initRunBrowser(deps: RunBrowserDeps): {
  refresh: () => void;
  setBodyTitle: (text: string) => void;
} {
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
  if (!panel) return { refresh: () => {}, setBodyTitle: () => {} };
  const runsPanel = panel;
  // One state object for the panel's lifetime: which branches are open and which row is current
  // survive a refresh, a filter change and a mode switch.
  const treeState: TreeState = { expanded: new Set(), current: null };
  let runs: RunSummary[] = [];
  let logs: RunLog[] = [];
  let loaded = false;

  const controls = mountBrowserControls(runsPanel, () => paint());

  function paint(): void {
    const filter = parseRunFilter(controls.query());
    const specs = buildRunTree({
      runs,
      logs,
      mode: controls.mode(),
      filter,
      now: deps.nowMs(),
    });
    renderRunTree(runsPanel.treeBox, specs, treeState, deps.onSelect, emptyNote(filter.empty));
    countBadge.textContent = specs.length ? String(specs.length) : "";
  }

  // The three empty states are three different facts, and only one of them is a problem the
  // reader can act on. Saying "no stored runs" to someone whose filter simply matched nothing is
  // the version that reads as data loss.
  function emptyNote(unfiltered: boolean): string {
    if (!deps.host && !deps.demo) {
      return "No daemon connected. Set a daemon address in Settings, or launch the demo.";
    }
    if (!loaded) return "Loading runs...";
    if (!unfiltered) return "No runs match this filter.";
    return "No stored runs yet. Run a target, then refresh this panel.";
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
    if (deps.demo) {
      runs = demoRuns(demoNow);
      logs = demoRunLogs(demoNow);
    } else if (deps.host) {
      // Both feeds at once: they are independent reads and the tree needs both to group by run.
      [runs, logs] = await Promise.all([
        fetchRuns(deps.host, deps.token),
        fetchRunLogs(deps.host, deps.token),
      ]);
    } else {
      runs = [];
      logs = [];
    }
    loaded = true;
    paint();
    runsPanel.applyDefault(runs.length > 0 || logs.length > 0);
  }

  paint();
  void load();
  return {
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
