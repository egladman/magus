// main.ts - the dashboard composition root. It builds the store, wires the two live
// feeds (transport.ts) into it, constructs every tile and mounts it into the panels
// container, and owns the page chrome that is NOT a tile: the app-bar health/mode/
// connection chips, the connect/resume panel, and the sibling-tool launchers.
//
// Tiles own their own DOM and only ever see mapped view-model (state.ts); the
// security-critical loopback lock, token handling, and stream clients live in
// lib/daemon.ts, shared with the graph explorer and log viewer.

import {
  parseHash, validateLiveHost, consumeLiveToken,
} from "../lib/daemon";
import { createStore } from "../lib/store";
import { initialState, type DashboardState, type ConnView } from "./state";
import { DashboardTransport } from "./transport";
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
import { runningTargetsTile } from "./tiles/runningTargets";
import { workspacesTile } from "./tiles/workspaces";
import { versionsTile } from "./tiles/versions";
import { ganttTile } from "./tiles/gantt";
import { flakePlaceholderTile } from "./tiles/placeholders";
import "../nav.js"; // reuse the site's exact nav dropdown behavior (hamburger <-> X, dismiss)

const el = (id: string): HTMLElement => document.getElementById(id) as HTMLElement;
const opt = (id: string): HTMLElement | null => document.getElementById(id);
function setText(id: string, text: string): void { const e = opt(id); if (e) e.textContent = text; }

// ---- daemon persistence (localStorage) -------------------------------------
const LS_DAEMON = "magus-dashboard-daemon";
const DISCONNECT_GRACE = 3; // consecutive stream failures before the pill flips to "disconnected"
function saveDaemon(host: string): void { try { localStorage.setItem(LS_DAEMON, host); } catch { /* ignore */ } }
function savedDaemon(): string | null { try { return localStorage.getItem(LS_DAEMON); } catch { return null; } }
function forgetDaemon(): void { try { localStorage.removeItem(LS_DAEMON); } catch { /* ignore */ } }

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
  const c = el("dash-conn");
  const map: Record<string, string> = {
    connecting: "connecting...", connected: "connected",
    disconnected: s.conn.detail || "reconnecting", none: "not connected",
  };
  c.textContent = map[s.conn.state] || s.conn.state;
  c.dataset.state = s.conn.state;

  if (s.status) {
    el("dash-connect").hidden = true;
    el("dash-panels").hidden = false;
    const badge = el("dash-health");
    badge.hidden = false;
    badge.textContent = s.status.health.label;
    badge.dataset.health = s.status.health.cls;
    const mode = el("dash-mode");
    if (s.status.pool.mode) { mode.textContent = s.status.pool.mode; mode.hidden = false; } else { mode.hidden = true; }
  }
}

// ---- tiles -----------------------------------------------------------------
let tiles: Tile[] = [];

function mountTiles(): void {
  const host = el("dash-panels");
  host.replaceChildren();

  // Order mirrors the prior hand-authored layout, then the new metric-family tiles,
  // then the two-up calls/workspaces, versions footer, and the Wave-3b seams.
  const single: Tile[] = [
    poolTile(),
    utilizationTile(),
    cacheStatsTile(),
    cacheRateTile(),
    latencyTile(),
    remoteTile(),
    targetsTile(),
    mcpTile(),
    buzzTile(),
    sandboxTile(),
  ];
  for (const t of single) host.append(t.el);

  // Two-up: running targets and loaded workspaces.
  const runningTargets = runningTargetsTile();
  const workspaces = workspacesTile();
  const cols = document.createElement("div");
  cols.className = "dash-cols";
  cols.append(runningTargets.el, workspaces.el);
  host.append(cols);

  const versions = versionsTile();
  host.append(versions.el);

  // The live execution timeline (fed by Status.runs). The flake column is still a
  // deferred seam - see tiles/placeholders.ts for the source it waits on.
  const gantt = ganttTile();
  const flake = flakePlaceholderTile();
  host.append(gantt.el, flake.el);

  tiles = [...single, runningTargets, workspaces, versions, gantt, flake];

  // Chrome first, then tiles: the panels are revealed before a chart tile builds.
  store.subscribe(renderChrome);
  for (const t of tiles) store.subscribe((s) => t.update(s));
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
    setConn({ state: "disconnected", detail: failCount >= DISCONNECT_GRACE ? undefined : "reconnecting" });
  } else if (failCount >= DISCONNECT_GRACE) {
    setConn({ state: "disconnected" });
    showResume(host, true);
    transport.stopStatusReconnect(); // stop hammering a daemon that isn't there
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

  const badge = opt("offline-badge");
  const updateOffline = (): void => { if (badge) badge.hidden = navigator.onLine; };
  updateOffline();
  window.addEventListener("online", updateOffline);
  window.addEventListener("offline", updateOffline);

  const params = parseHash();
  consumeLiveToken(params);

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
  if (saved && validateLiveHost(saved)) {
    setText("dash-connect-title", "Reconnecting...");
    setText("dash-connect-sub", "Resuming your last daemon at " + saved + ".");
    connectLive(saved);
    return;
  }
  setConn({ state: "none" });
}

boot();
