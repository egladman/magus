// main.ts - the dashboard composition root. It builds the store, wires the two live
// feeds (transport.ts) into it, constructs every tile and mounts it into the panels
// container, and owns the page chrome that is NOT a tile: the app-bar health/mode/
// connection chips, the connect/resume panel, and the sibling-tool launchers.
//
// Tiles own their own DOM and only ever see mapped view-model (state.ts); the
// security-critical loopback lock, token handling, and stream clients live in
// lib/daemon.ts, shared with the graph explorer and log viewer.

import {
  parseHash, validateLiveHost, consumeLiveToken, wantsDemo,
} from "../../lib/daemon";
import { createStore } from "../../lib/store";
import { persisted } from "../../lib/persist";
import { initialState, type DashboardState, type ConnView } from "./state";
import { DashboardTransport } from "./transport";
import { startDemo, type DemoHandle } from "./demo";
import type { Tile } from "./tiles/card";
import { poolTile } from "./tiles/pool";
import { utilizationTile } from "./tiles/utilization";
import { cacheStatsTile } from "./tiles/cacheStats";
import { cacheRateTile } from "./tiles/cacheRate";
import { latencyTile } from "./tiles/latency";
import { remoteTile } from "./tiles/remote";
import { targetsTile } from "./tiles/targets";
import { mcpTile } from "./tiles/mcp";
import { buzzTile } from "./tiles/buzz";
import { sandboxTile } from "./tiles/sandbox";
import { attentionTile } from "./tiles/attention";
import { activityTile } from "./tiles/activity";
import { workspacesTile } from "./tiles/workspaces";
import { servicesTile } from "./tiles/services";
import { configTile } from "./tiles/config";
import { ganttTile } from "./tiles/gantt";
import { insightSection } from "./tiles/insight";
// This app does NOT load the docs main.js bundle, so it wires the shared console chrome
// itself: the nav dropdown, the docs search (relocated into the drawer), the reference drawer
// (from src/ui/), and the settings gear (from src/ui/). Each is an exported init function
// (post-ESM-refactor), so they are CALLED here, not run on import. Order matters: ref-drawer
// runs after search so it can pull the search bar (.page-tools) into the drawer.
import { initNav } from "../../site/nav.js";
import { initSearch } from "../../site/search.js";
import { initRefDrawer } from "../../ui/ref-drawer.js";
import { initConsoleSettings } from "../../ui/console-settings.js";
import { getDefaultHost } from "../../lib/settings";

initNav();
initSearch();
initRefDrawer();
initConsoleSettings();

const el = (id: string): HTMLElement => document.getElementById(id) as HTMLElement;
const opt = (id: string): HTMLElement | null => document.getElementById(id);
function setText(id: string, text: string): void { const e = opt(id); if (e) e.textContent = text; }

// ---- daemon persistence ----------------------------------------------------
const daemonCell = persisted<string | null>("dashboard-daemon", null);
const DISCONNECT_GRACE = 3; // consecutive stream failures before the pill flips to "disconnected"
function saveDaemon(host: string): void { daemonCell.set(host); }
function savedDaemon(): string | null { return daemonCell.get(); }
function forgetDaemon(): void { daemonCell.set(null); }

// ---- store + transport -----------------------------------------------------
const store = createStore<DashboardState>(initialState());
const transport = new DashboardTransport(store, {
  onStatusOpen: (host) => onLiveOpen(host),
  onStatusError: (host) => onLiveError(host),
});

// ---- connection state ------------------------------------------------------
let everConnected = false;
let failCount = 0;

function setConn(conn: ConnView): void {
  store.set({ conn });
}

// renderChrome reflects the store into the app bar and the panel visibility. It is
// subscribed BEFORE the tiles so the panels are revealed (width > 0) before a chart
// tile tries to build in the same publish.
function renderChrome(s: DashboardState): void {
  const demoing = s.conn.state === "demo";

  // Connection dot: ONE indicator - a colored dot that reads "connected" (green) when live, and
  // "not connected" otherwise. When connected, the dot takes the daemon's HEALTH color (green ok /
  // amber degraded / red down) so a single element carries both "is it live" and "is it well" - no
  // separate health/mode chips. In demo the board is streaming synthesized data, so the dot reads
  // "connected" (the app-bar "Demo data" chip is what marks it as synthetic).
  const c = el("console-conn");
  if (demoing) {
    c.textContent = "connected";
    c.dataset.state = "connected";
    c.dataset.health = s.status ? s.status.health.cls : "ok";
  } else {
    const map: Record<string, string> = {
      connecting: "connecting...", connected: "connected",
      disconnected: s.conn.detail || "reconnecting", none: "not connected",
    };
    c.textContent = map[s.conn.state] || s.conn.state;
    c.dataset.state = s.conn.state;
    if (s.conn.state === "connected" && s.status) { c.dataset.health = s.status.health.cls; } else { delete c.dataset.health; }
  }

  // Demo-data flag: the daemon-free showcase, called out by the shared #console-demo pill in the
  // app bar (the one demo affordance every console app shares), not a dashboard-only corner chip.
  el("console-demo").hidden = !demoing;

  if (s.status) {
    el("dash-connect").hidden = true;
    el("dash-panels").hidden = false;
  }

  // Observing-since: a brief note of when the daemon began collecting these counters, so it is
  // clear the numbers are cumulative from then and are NOT persisted across daemon restarts.
  const obs = el("console-observing");
  if (s.observingSince) {
    const t = new Date(s.observingSince).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
    obs.textContent = "observing since " + t;
    obs.title = "The telemetry and cache counters are cumulative since the daemon started observing (" + t + "). They are not persisted across daemon restarts.";
    obs.hidden = false;
  } else {
    obs.hidden = true;
  }
}

// ---- tiles -----------------------------------------------------------------
let tiles: Tile[] = [];

function mountTiles(): void {
  const host = el("dash-panels");
  host.replaceChildren();

  // Board order is triage-first, so a fresh landing reads top-down as "anything wrong? ->
  // what's running? -> instantaneous state -> live timeline -> trends -> heavy metrics
  // (folded) -> code insight":
  //
  //  1. attention hero      - the headline: failing / running / queued at a glance.
  //  2. live activity       - what's running now, with a streaming log preview + deep-link.
  //  3. pool + cache (half)  - the two instantaneous-state summaries, side by side.
  //  4. execution timeline   - the live gantt of runs.
  //  5. cache rate / util    - the two live history charts.
  //  6. per-target / remote / workspaces / services / config - the denser but still legible readouts.
  //  7. latency / buzz / sandbox / mcp - the heavy metric families, DEFAULT-COLLAPSED so
  //     they sit out of the way until asked (see each tile's Card defaultCollapsed).
  //  8. insight section      - the VCS/run-outcome lenses (on-demand poll).
  const pool = poolTile();
  const cacheStats = cacheStatsTile();
  pool.el.classList.add("tile-half");
  cacheStats.el.classList.add("tile-half");

  const attention = attentionTile();
  const activity = activityTile();
  const gantt = ganttTile(); // the live execution timeline (fed by Status.runs)
  const utilization = utilizationTile();
  const cacheRate = cacheRateTile();
  const targets = targetsTile();
  const remote = remoteTile();
  const workspaces = workspacesTile();
  const services = servicesTile();
  const config = configTile();
  const latency = latencyTile();
  const buzz = buzzTile();
  const sandbox = sandboxTile();
  const mcp = mcpTile();

  // Ordered, full-width by default (pool/cacheStats opt into the half-width pair row).
  const ordered: Tile[] = [
    attention, activity,
    pool, cacheStats,
    remote,
    gantt,
    cacheRate, utilization,
    targets, workspaces, services, config,
    latency, buzz, sandbox, mcp,
  ];
  for (const t of ordered) host.append(t.el);

  // The Insight section: the five VCS/run-outcome lenses, fed by the on-demand
  // /api/v1/insight poll. Its refresh button forces an out-of-band refetch.
  const insight = insightSection(() => transport.refreshInsight());
  host.append(insight.el);
  for (const t of insight.tiles) host.append(t.el);

  tiles = [...ordered, ...insight.tiles];

  // Chrome first, then tiles: the panels are revealed before a chart tile builds.
  store.subscribe(renderChrome);
  for (const t of tiles) store.subscribe((s) => t.update(s));
}

// ---- demo mode -------------------------------------------------------------
// The daemon-free showcase: synthesize a live-looking DashboardState (demo.ts) and
// push it into the store, so the whole board can be shown off with nothing running.
// No socket is opened; the connection pill reads "demo data".
let demo: DemoHandle | null = null;
function beginDemo(): void {
  transport.stop();      // make sure no resume loop is racing the demo feed
  demo?.stop();
  setConn({ state: "demo" });
  // Synthesize an observing-since ~92 minutes back so the demo shows the same since-caption a live
  // daemon would (the real value comes from the JSON status endpoint on connect).
  store.set({ observingSince: Date.now() - 92 * 60 * 1000 });
  store.set({ config: { defaultCharms: ["rw"], concurrency: 8, sandbox: true } });
  demo = startDemo(store);
  // Chain the sibling apps' demos off the same `#demo` fragment so the showcase flows
  // across all three surfaces as one unified demo. Both the graph explorer and the log
  // viewer have their own #demo mode.
  for (const id of ["launch-graph", "menu-graph"]) {
    const a = opt(id) as HTMLAnchorElement | null;
    if (a) a.href = "../graph/#demo";
  }
  for (const id of ["launch-logs", "menu-logs"]) {
    const a = opt(id) as HTMLAnchorElement | null;
    if (a) a.href = "../logs/#demo";
  }
}

// ---- live connection lifecycle ---------------------------------------------
function connectLive(host: string): void {
  if (!everConnected) setConn({ state: "connecting" });
  transport.connect(host);
}

function onLiveOpen(host: string): void {
  failCount = 0;
  everConnected = true;
  setConn({ state: "connected" });
  wireLaunchers(host);
  saveDaemon(host); // remember it so a reload resumes
}

// onLiveError debounces disconnection: a brief blip stays "reconnecting" and keeps the
// last data on screen; only after DISCONNECT_GRACE consecutive failures does the pill go
// "disconnected". A never-connected resume attempt that gives up shows the confirm form.
function onLiveError(host: string): void {
  failCount++;
  if (everConnected) {
    setConn({ state: "disconnected", detail: failCount >= DISCONNECT_GRACE ? "disconnected" : "reconnecting" });
  } else if (failCount >= DISCONNECT_GRACE) {
    setConn({ state: "disconnected", detail: "disconnected" });
    showResume(host, true);
    transport.stop(); // give up: tear down all feeds so nothing hammers an absent daemon
  } else {
    setConn({ state: "connecting" });
  }
}

// showResume reveals the connect panel's reconnect form, pre-filled with host.
function showResume(host: string | null, failed: boolean): void {
  el("dash-connect").hidden = false;
  el("dash-panels").hidden = true;
  const form = el("dash-resume");
  form.hidden = false;
  (el("dash-resume-host") as HTMLInputElement).value = host || "";
  setText("dash-connect-title", failed ? "Couldn't reach the daemon" : "Reconnect to the daemon");
  setText("dash-connect-sub", failed
    ? "The saved address didn't respond. Confirm it below, or start the daemon and open the link it prints."
    : "Resume your last daemon, or start a new one below.");
}

// wireDemoButton wires the empty-state "See a demo" button. It sets the #demo
// fragment and reloads so the showcase re-enters through boot()'s normal path (and a
// shared /dashboard/#demo link lands straight in the demo).
function wireDemoButton(): void {
  const btn = opt("dash-demo-btn");
  if (!btn) return;
  btn.addEventListener("click", () => { location.hash = "demo"; location.reload(); });
}

function wireResumeForm(): void {
  const form = opt("dash-resume") as HTMLFormElement | null;
  if (!form) return;
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    const host = validateLiveHost((el("dash-resume-host") as HTMLInputElement).value.trim());
    if (!host) {
      setText("dash-connect-sub", "That host must be literally 127.0.0.1 or [::1] with a port.");
      return;
    }
    everConnected = false;
    failCount = 0;
    setConn({ state: "connecting" });
    connectLive(host);
  });
  el("dash-resume-forget").addEventListener("click", () => {
    forgetDaemon();
    form.hidden = true;
    setText("dash-connect-title", "No daemon connected");
    setText("dash-connect-sub",
      "The dashboard streams a running magus daemon's pool, cache, and health. Start the daemon, then open the live link it prints.");
    setConn({ state: "none" });
  });
}

// wireLaunchers points the log-viewer / graph-explorer links at live mode when the
// dashboard is connected (host only - the token is shared via sessionStorage).
function wireLaunchers(host: string): void {
  const logs = "../logs/#live=" + encodeURIComponent(host);
  const graph = "../graph/#live=" + encodeURIComponent(host);
  (el("launch-logs") as HTMLAnchorElement).href = logs;
  (el("launch-graph") as HTMLAnchorElement).href = graph;
  (el("menu-logs") as HTMLAnchorElement).href = logs;
  (el("menu-graph") as HTMLAnchorElement).href = graph;
}

// ---- service worker --------------------------------------------------------
// This page does NOT load the docs main.js (which injects site search + nav chrome),
// so it registers the site sw.js itself to stay an installable, offline-capable PWA
// surface, at the gen-root scope that also covers /dashboard/. Only on a secure origin.
function registerServiceWorker(): void {
  if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) return;
  const secure = location.protocol === "https:" || location.hostname === "localhost";
  if (!secure) return;
  window.addEventListener("load", () => {
    navigator.serviceWorker.register(new URL("../sw.js", import.meta.url)).catch(() => {});
  });
}

// ---- boot ------------------------------------------------------------------
function boot(): void {
  document.documentElement.classList.remove("no-js");
  registerServiceWorker();
  mountTiles();
  wireResumeForm();
  wireDemoButton();

  const badge = opt("offline-badge");
  const updateOffline = (): void => { if (badge) badge.hidden = navigator.onLine; };
  updateOffline();
  window.addEventListener("online", updateOffline);
  window.addEventListener("offline", updateOffline);

  const params = parseHash();
  consumeLiveToken(params);

  // A #demo fragment enters the daemon-free showcase and wins over any saved daemon.
  if (wantsDemo(params)) {
    beginDemo();
    return;
  }

  // A #live=host in the URL (the link magus printed) always wins.
  if (params.live !== undefined) {
    const host = validateLiveHost(params.live);
    if (!host) {
      setConn({ state: "disconnected", detail: "invalid host" });
      setText("dash-connect-title", "Can't connect");
      setText("dash-connect-sub",
        "The #live host must be literally 127.0.0.1 or [::1]. Re-open the link magus printed.");
      return;
    }
    connectLive(host);
    return;
  }

  // No link in the URL: optimistically resume the last daemon we connected to.
  const saved = savedDaemon();
  const savedHost = saved ? validateLiveHost(saved) : null;
  if (savedHost) {
    setText("dash-connect-title", "Reconnecting...");
    setText("dash-connect-sub", "Resuming your last daemon at " + savedHost + ".");
    connectLive(savedHost); // the normalized host, matching the #live= and resume-form paths
    return;
  }
  // No remembered daemon, but the operator set a default host in Settings (the loopback override):
  // connect to it.
  const configured = getDefaultHost();
  const configuredHost = configured ? validateLiveHost(configured) : null;
  if (configuredHost) {
    setText("dash-connect-title", "Reconnecting...");
    setText("dash-connect-sub", "Connecting to your configured daemon at " + configuredHost + ".");
    connectLive(configuredHost);
    return;
  }
  setConn({ state: "none" });
}

boot();
