// runtree.ts - the Log Viewer's run browser: a PatternFly TreeView down the left of the viewer that
// lists prior runs (project -> target -> run) so a reader can browse recent captured output and open
// any one. It reads the daemon's read-only /api/v1/outputs feed (the local output store's retained
// executions) and, on selection, hands the chosen run's ref to the viewer, which fetches that run's
// verbatim output from /api/v1/output?ref=. The whole browser is a no-op with no reachable daemon
// (the tree simply stays empty / shows its cold hint), so the offline #data/#src paths are
// unaffected. PF owns the tree chrome (pf-v6-c-tree-view); only the panel frame + status dot are ours.

import { authHeaders } from "../../lib/daemon";
import { scenarioRuns } from "../demo-scenario";

// RunBrowserDeps: what initRunBrowser needs from the log viewer. scroll is the viewer's scroll box
// (the tree docks to its left, sharing the panel below the toolbar). host/token address the daemon
// (empty host => demo/offline: the demo runs render, selection loads a synthetic sample). onOpenRun
// hands a selected run to the viewer to load; nowMs stamps the "how long ago" labels (Date.now is
// unavailable to keep callers testable, so the viewer passes it in).
export interface RunBrowserDeps {
  scroll: HTMLElement;
  host: string;
  token: string | null;
  demo: boolean;
  nowMs: number;
  onOpenRun: (run: RunSummary) => void;
}

// RunSummary mirrors one row of the daemon's /api/v1/outputs JSON (a cache.OutputDescriptor projected
// to the wire). Times are unix milliseconds. target is the REPRO target (a charm suffix like
// "build:rw" is preserved); the tree groups by the bare name.
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
export async function fetchRunOutput(host: string, token: string | null, ref: string): Promise<string | null> {
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

// bareTarget strips the charm suffix ("build:rw" -> "build"), mirroring the Go cache.bareTarget, so a
// target's runs group under its declared name regardless of which charm variant produced each.
function bareTarget(t: string): string {
  const i = t.indexOf(":");
  return i < 0 ? t : t.slice(0, i);
}

// relTime renders a unix-ms timestamp as a compact "how long ago" for a run leaf, falling back to a
// clock time for anything older than a day so distant runs stay distinguishable. now is injected so
// the function is pure and testable.
export function relTime(ms: number, now: number): string {
  const sec = Math.max(0, Math.round((now - ms) / 1000));
  if (sec < 60) return sec + "s ago";
  const min = Math.round(sec / 60);
  if (min < 60) return min + "m ago";
  const hr = Math.round(min / 60);
  if (hr < 24) return hr + "h ago";
  const d = new Date(ms);
  const pad = (n: number): string => (n < 10 ? "0" + n : String(n));
  return pad(d.getMonth() + 1) + "/" + pad(d.getDate()) + " " + pad(d.getHours()) + ":" + pad(d.getMinutes());
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

// A PF tree node. Branches (project/target) toggle expansion; leaves (a run) select and fire onSelect.
interface NodeSpec {
  label: string;
  count?: number;          // a child count badge (branches)
  status?: "pass" | "fail"; // a leaf's outcome dot
  title?: string;
  children?: NodeSpec[];
  run?: RunSummary;        // set on a leaf: what selecting it opens
}

function makeNode(spec: NodeSpec, onSelect: (run: RunSummary) => void, expanded: boolean): HTMLLIElement {
  const li = document.createElement("li");
  li.className = "pf-v6-c-tree-view__list-item";
  li.setAttribute("role", "treeitem");
  const hasKids = !!spec.children && spec.children.length > 0;

  const content = document.createElement("div");
  content.className = "pf-v6-c-tree-view__content";
  const node = document.createElement("button");
  node.type = "button";
  node.className = "pf-v6-c-tree-view__node";
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
  if (spec.status) {
    const dot = document.createElement("span");
    dot.className = "console-log-runs__dot";
    dot.dataset.status = spec.status;
    nodeContent.append(dot);
  }
  const text = document.createElement("span");
  text.className = "pf-v6-c-tree-view__node-text";
  text.textContent = spec.label;
  nodeContent.append(text);
  container.append(nodeContent);
  if (spec.count != null) {
    const badge = document.createElement("span");
    badge.className = "pf-v6-c-tree-view__node-count";
    const b = document.createElement("span");
    b.className = "pf-v6-c-badge pf-m-read";
    b.textContent = String(spec.count);
    badge.append(b);
    container.append(badge);
  }
  node.append(container);
  content.append(node);
  li.append(content);

  if (hasKids) {
    li.setAttribute("aria-expanded", String(expanded));
    if (expanded) li.classList.add("pf-m-expanded");
    const group = document.createElement("ul");
    group.className = "pf-v6-c-tree-view__list";
    group.setAttribute("role", "group");
    for (const child of spec.children!) group.append(makeNode(child, onSelect, false));
    li.append(group);
    node.addEventListener("click", () => {
      const open = li.classList.toggle("pf-m-expanded");
      li.setAttribute("aria-expanded", String(open));
    });
  } else if (spec.run) {
    const run = spec.run;
    node.addEventListener("click", () => {
      // Single selection: clear the prior current node, mark this one.
      const root = li.closest(".pf-v6-c-tree-view");
      root?.querySelectorAll(".pf-v6-c-tree-view__node.pf-m-current").forEach((n) => n.classList.remove("pf-m-current"));
      node.classList.add("pf-m-current");
      onSelect(run);
    });
  }
  return li;
}

// renderRunTree (re)builds the tree into container from runs, grouped project -> target -> run. The
// first project and its first target start expanded so the newest runs are visible without a click.
// runs arrive newest-first (the daemon sorts them), so each target's leaves keep that order.
export function renderRunTree(container: HTMLElement, runs: RunSummary[], onSelect: (run: RunSummary) => void, now: number, emptyNote?: string): void {
  container.replaceChildren();
  if (runs.length === 0) {
    const empty = document.createElement("p");
    empty.className = "console-log-runs__empty";
    // emptyNote lets the caller explain WHY the panel is empty (no daemon vs a daemon with no stored
    // runs); the plain "no stored runs" wording is the daemon-connected default.
    empty.textContent = emptyNote ?? "No stored runs. Run a target, then reopen this panel.";
    container.append(empty);
    return;
  }

  // Group preserving newest-first order via insertion-ordered maps.
  const byProject = new Map<string, Map<string, RunSummary[]>>();
  for (const r of runs) {
    const proj = r.project || "(workspace)";
    const tgt = bareTarget(r.target) || "(run)";
    let targets = byProject.get(proj);
    if (!targets) { targets = new Map(); byProject.set(proj, targets); }
    const list = targets.get(tgt) ?? [];
    if (list.length === 0) targets.set(tgt, list);
    list.push(r);
  }

  const projectSpecs: NodeSpec[] = [];
  for (const [proj, targets] of byProject) {
    const targetSpecs: NodeSpec[] = [];
    for (const [tgt, list] of targets) {
      targetSpecs.push({
        label: tgt,
        count: list.length,
        children: list.map((r) => ({
          label: relTime(r.timestamp_ms, now),
          status: r.failed ? "fail" : "pass",
          title: (r.failed ? "failed" : "passed") + " - " + tgt + " - " + Math.round(r.duration_ms) + "ms - " + r.ref + (r.error ? " - " + r.error : ""),
          run: r,
        })),
      });
    }
    projectSpecs.push({ label: proj, count: targetSpecs.length, children: targetSpecs });
  }

  const tree = document.createElement("div");
  tree.className = "pf-v6-c-tree-view pf-m-guides";
  const list = document.createElement("ul");
  list.className = "pf-v6-c-tree-view__list";
  list.setAttribute("role", "tree");
  projectSpecs.forEach((p, pi) => {
    const li = makeNode(p, onSelect, pi === 0);
    // Expand the first target of the first project too, so the newest runs show on open.
    if (pi === 0) {
      const firstTarget = li.querySelector<HTMLLIElement>(".pf-v6-c-tree-view__list > .pf-v6-c-tree-view__list-item");
      if (firstTarget) { firstTarget.classList.add("pf-m-expanded"); firstTarget.setAttribute("aria-expanded", "true"); }
    }
    list.append(li);
  });
  tree.append(list);
  container.append(tree);
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
  head: HTMLElement;         // the header row, so a caller can inject extra chrome (e.g. a count)
  treeBox: HTMLElement;      // the caller (re)renders its tree into this
  refreshBtn: HTMLButtonElement;
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
  const refreshBtn = iconButton("", "Refresh", "Refresh", ["M21 12a9 9 0 1 1-2.64-6.36", "M21 3v6h-6"]);
  const hideBtn = iconButton("", "Hide the panel", "Hide the panel", ["M15 18l-6-6 6-6"]);
  head.append(title, refreshBtn, hideBtn);

  const treeBox = document.createElement("div");
  treeBox.className = "console-log-runs__tree";
  aside.append(head, treeBox);

  const reopen = iconButton("", "Show the panel", "Show the panel", ["M9 18l6-6-6-6"]);
  reopen.classList.add("console-log-runs__reopen");
  reopen.hidden = true;

  split.append(aside, reopen, opts.scroll);

  // The open state is JS-driven (the hidden attribute), so the phone default lives here (matchMedia)
  // rather than duplicated into logs.css - the same breakpoint the app shell uses (console.css).
  const narrow = window.matchMedia("(max-width: 47.999rem)");
  let userOpened = false;
  const apply = (state: "open" | "closed" | "hidden"): void => {
    aside.hidden = state !== "open";
    reopen.hidden = state !== "closed";
  };
  hideBtn.addEventListener("click", () => apply("closed"));
  reopen.addEventListener("click", () => { userOpened = true; apply("open"); });
  refreshBtn.addEventListener("click", opts.onRefresh);

  return {
    head, treeBox, refreshBtn,
    applyDefault: (hasContent: boolean): void => {
      if (!hasContent) { apply(opts.hideWhenEmpty ? "hidden" : "closed"); return; }
      apply(userOpened || !narrow.matches ? "open" : "closed");
    },
  };
}

// initRunBrowser docks the run browser to the left of the viewer's scroll box and populates it: it
// fetches the run list (or, in #demo, the synthetic set) and renders the tree; selecting a run calls
// deps.onOpenRun. Demo runs surface ONLY in explicit demo mode - with no daemon and no demo it fetches
// nothing (a fresh install must not show fabricated runs as if real) and the reopen rail opens to an
// honest note. Returns a refresh handle the viewer can call (e.g. after a live run finishes).
export function initRunBrowser(deps: RunBrowserDeps): { refresh: () => void } {
  const panel = mountCollapsiblePanel({
    scroll: deps.scroll,
    title: "Recent runs",
    label: "Recent runs",
    onRefresh: () => { void load(); },
    hideWhenEmpty: false,
  });
  if (!panel) return { refresh: () => {} };
  const runsPanel = panel; // narrowed non-null, so the load() closure below sees a definite panel

  async function load(): Promise<void> {
    const runs = deps.demo ? demoRuns(deps.nowMs) : deps.host ? await fetchRuns(deps.host, deps.token) : [];
    const note = !deps.host && !deps.demo
      ? "No daemon connected. Set a daemon address in Settings, or launch the demo."
      : undefined;
    renderRunTree(runsPanel.treeBox, runs, deps.onOpenRun, deps.nowMs, note);
    runsPanel.applyDefault(runs.length > 0);
  }

  void load();
  return { refresh: () => { void load(); } };
}
