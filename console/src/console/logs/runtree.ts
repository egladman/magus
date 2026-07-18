// runtree.ts - the Log Viewer's run browser: a PatternFly TreeView down the left of the viewer that
// lists prior runs (project -> target -> run) so a reader can browse recent captured output and open
// any one. It reads the daemon's read-only /api/v1/outputs feed (the local output store's retained
// executions) and, on selection, hands the chosen run's ref to the viewer, which fetches that run's
// verbatim output from /api/v1/output?ref=. The whole browser is a no-op with no reachable daemon
// (the tree simply stays empty / shows its cold hint), so the offline #data/#src/#live paths are
// unaffected. PF owns the tree chrome (pf-v6-c-tree-view); only the panel frame + status dot are ours.

import { authHeaders } from "../../lib/daemon";

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

// demoRuns is a small synthetic run set for the daemon-free showcase (the shared #demo path), so the
// browser reads as populated without a daemon. Timestamps are relative to `now` so the leaves show
// plausible "how long ago" labels.
export function demoRuns(now: number): RunSummary[] {
  const min = 60_000;
  return [
    { ref: "outdemo001", project: "svc/api", target: "build:rw", failed: false, timestamp_ms: now - 2 * min, duration_ms: 1840 },
    { ref: "outdemo002", project: "svc/api", target: "test", failed: true, error: "2 assertions failed", timestamp_ms: now - 6 * min, duration_ms: 4200 },
    { ref: "outdemo003", project: "svc/api", target: "test", failed: false, timestamp_ms: now - 40 * min, duration_ms: 3900 },
    { ref: "outdemo004", project: "svc/api", target: "lint", failed: false, timestamp_ms: now - 55 * min, duration_ms: 620 },
    { ref: "outdemo005", project: "web/app", target: "build", failed: false, timestamp_ms: now - 3 * 60 * min, duration_ms: 9100 },
    { ref: "outdemo006", project: "web/app", target: "typecheck", failed: true, error: "TS2345 in main.ts", timestamp_ms: now - 5 * 60 * min, duration_ms: 2600 },
  ];
}

const svgNS = "http://www.w3.org/2000/svg";
function chevron(): SVGElement {
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
export function renderRunTree(container: HTMLElement, runs: RunSummary[], onSelect: (run: RunSummary) => void, now: number): void {
  container.replaceChildren();
  if (runs.length === 0) {
    const empty = document.createElement("p");
    empty.className = "console-log-runs__empty";
    empty.textContent = "No stored runs. Run a target, then reopen this panel.";
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
  b.id = id;
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

// initRunBrowser docks the run browser to the left of the viewer's scroll box and populates it. It
// reparents `scroll` into a flex split with a collapsible aside (so no scaffold change is needed),
// fetches the run list (or the demo set), and renders the tree; selecting a run calls deps.onOpenRun.
// The aside stays hidden when there are no runs, so a bare viewer is unchanged. Returns a refresh
// handle the viewer can call (e.g. after a live run finishes) - refetch and re-render in place.
export function initRunBrowser(deps: RunBrowserDeps): { refresh: () => void } {
  const parent = deps.scroll.parentElement;
  if (!parent) return { refresh: () => {} };

  const split = document.createElement("div");
  split.className = "console-log-split";
  parent.insertBefore(split, deps.scroll);

  const aside = document.createElement("aside");
  aside.className = "console-log-runs";
  aside.id = "log-runs";
  aside.hidden = true;
  aside.setAttribute("aria-label", "Recent runs");

  const head = document.createElement("div");
  head.className = "console-log-runs__head";
  const title = document.createElement("span");
  title.className = "console-log-runs__title";
  title.textContent = "Recent runs";
  const refreshBtn = iconButton("log-runs-refresh", "Refresh runs", "Refresh the run list", ["M21 12a9 9 0 1 1-2.64-6.36", "M21 3v6h-6"]);
  const hideBtn = iconButton("log-runs-hide", "Hide the run browser", "Hide the run browser", ["M15 18l-6-6 6-6"]);
  head.append(title, refreshBtn, hideBtn);

  const treeBox = document.createElement("div");
  treeBox.className = "console-log-runs__tree";
  aside.append(head, treeBox);

  // A slim reopen rail, shown only while the aside is hidden BUT runs exist, so the browser can be
  // brought back without leaving the viewer.
  const reopen = iconButton("log-runs-show", "Show the run browser", "Show the run browser", ["M9 18l6-6-6-6"]);
  reopen.classList.add("console-log-runs__reopen");
  reopen.hidden = true;

  split.append(aside, reopen, deps.scroll);

  let hasRuns = false;
  const setAsideOpen = (open: boolean): void => {
    aside.hidden = !open;
    reopen.hidden = open || !hasRuns;
  };
  hideBtn.addEventListener("click", () => setAsideOpen(false));
  reopen.addEventListener("click", () => setAsideOpen(true));

  const load = async (): Promise<void> => {
    const runs = deps.demo || !deps.host ? demoRuns(deps.nowMs) : await fetchRuns(deps.host, deps.token);
    hasRuns = runs.length > 0;
    renderRunTree(treeBox, runs, deps.onOpenRun, deps.nowMs);
    setAsideOpen(hasRuns);
  };
  refreshBtn.addEventListener("click", () => { void load(); });
  void load();

  return { refresh: () => { void load(); } };
}
