// main.ts - the dashboard composition root. It builds the store, wires the two live
// feeds (transport.ts) into it, constructs every tile and mounts it into the panels
// container, and owns the page chrome that is NOT a tile: the app-bar health/mode/
// connection chips, the connect/resume panel, and the sibling-tool launchers.
//
// Tiles own their own DOM and only ever see mapped view-model (state.ts); the
// security-critical loopback lock, token handling, and stream clients live in
// lib/daemon.ts, shared with the graph explorer and log viewer.

import {
  parseHash,
  daemonAttach,
  validateLoopbackHost,
  normalizeDaemonHost,
  consumeLiveToken,
  wantsDemo,
  logsLink,
} from "../../lib/daemon";
import { createStore } from "../../lib/store";
import { persisted } from "../../lib/persist";
import { notify } from "../../lib/notifications";
import { bind } from "../view";
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
import { viewMode, dashboardHeader, bigPictureTile, activeWorkspace } from "./tiles/bigPicture";
// The dashboard is only ever mounted as a console surface now (the decoupled console has no standalone
// docs page), so it wires NO docs-site chrome of its own - the console frame owns the title bar, tab
// strip, settings gear, and status bar. (Its old standalone-only initNav/initSearch/initRefDrawer/
// initConsoleSettings self-wiring was dropped with the docs-page decoupling.)
import { getDefaultHost } from "../../lib/settings";

const el = (id: string): HTMLElement => document.getElementById(id) as HTMLElement;
const opt = (id: string): HTMLElement | null => document.getElementById(id);
function setText(id: string, text: string): void {
  const e = opt(id);
  if (e) e.textContent = text;
}

// ---- daemon persistence ----------------------------------------------------
const daemonCell = persisted<string | null>("dashboard-daemon", null);
const DISCONNECT_GRACE = 3; // consecutive stream failures before the pill flips to "disconnected"
function saveDaemon(host: string): void {
  daemonCell.set(host);
}
function savedDaemon(): string | null {
  return daemonCell.get();
}
function forgetDaemon(): void {
  daemonCell.set(null);
}

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

// surfaceHidden is set true by the exported setVisible() when the dashboard is mounted in the console
// and its tab is backgrounded. While hidden, renderStatusBar skips the SHARED status-bar writes (the
// console detaches this tab's status bar, so those el() lookups would resolve to the ACTIVE tab's bar
// and leak "connected / observing since" into, say, the log viewer). The dashboard's OWN panel reveal
// and its tiles keep updating in the background. lastState lets setVisible(true) replay the current
// state so the bar catches up on return. Standalone (no console) this stays false, unchanged.
let surfaceHidden = false;
let lastState: DashboardState | null = null;

export function setVisible(visible: boolean): void {
  surfaceHidden = !visible;
  if (visible && lastState) renderStatusBar(lastState);
}

// renderStatusBar reflects the store into the app bar and the panel visibility. It is
// subscribed BEFORE the tiles so the panels are revealed (width > 0) before a chart
// tile tries to build in the same publish.
function renderStatusBar(s: DashboardState): void {
  lastState = s;

  // Drive both every render so the tiles and the "No daemon connected" front door stay mutually exclusive
  // (the old latch only ever revealed, leaving stale tiles up when the daemon dropped). Show tiles only
  // with a status frame in hand AND a live link (connected/demo) or a brief reconnect blip; else the door.
  const reconnecting = s.conn.state === "disconnected" && s.conn.detail === "reconnecting";
  const showPanels =
    !!s.status && (s.conn.state === "connected" || s.conn.state === "demo" || reconnecting);
  el("dash-connect").hidden = showPanels;
  el("dash-panels").hidden = !showPanels;

  // Everything below writes the SHARED bottom status bar; skip it while this tab is hidden.
  if (surfaceHidden) return;

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
      connecting: "connecting...",
      connected: "connected",
      disconnected: s.conn.detail || "reconnecting",
      none: "not connected",
    };
    c.textContent = map[s.conn.state] || s.conn.state;
    c.dataset.state = s.conn.state;
    if (s.conn.state === "connected" && s.status) {
      c.dataset.health = s.status.health.cls;
    } else {
      delete c.dataset.health;
    }
  }

  // Observing-since: a brief note of when the daemon began collecting these counters, so it is
  // clear the numbers are cumulative from then and are NOT persisted across daemon restarts.
  const obs = el("console-observing");
  if (s.observingSince) {
    const t = new Date(s.observingSince).toLocaleTimeString([], {
      hour: "2-digit",
      minute: "2-digit",
    });
    obs.textContent = "observing since " + t;
    obs.title =
      "The telemetry and cache counters are cumulative since the daemon started observing (" +
      t +
      "). They are not persisted across daemon restarts.";
    obs.hidden = false;
  } else {
    obs.hidden = true;
  }
}

// ---- notification admission ------------------------------------------------
// The dashboard's status frames are where the console already learns two bell-tier facts: the daemon's
// health dropping, and a target turning FAILED. wireNotifications watches for those TRANSITIONS and
// pushes an error-tier notification (notifications.ts). It notifies ONLY on the transition - a key per
// health-state and per failing ref means the same event does not re-fire on every ~1s status frame, or
// when this surface re-mounts in a session. Demo never notifies (s.conn.state === "demo"): synthesized
// data must not light the bell. A failing target with an output ref deep-links to the log viewer at that
// ref (the same href the gantt bar uses); without a ref it stays on the dashboard, so no link is set.
function wireNotifications(): void {
  let lastHealth = ""; // the health cls last seen ("", "ok", "warn", "fail")
  store.subscribe((s) => {
    if (s.conn.state === "demo" || !s.status) return;
    const cls = s.status.health.cls;
    if (cls !== lastHealth) {
      if (cls === "warn")
        notify({
          source: "Dashboard",
          kind: "error",
          key: "dash:health:warn",
          message: "Daemon health degraded. Some components are not fully ready.",
        });
      else if (cls === "fail")
        notify({
          source: "Dashboard",
          kind: "error",
          key: "dash:health:down",
          message: "Daemon health is down. It is not serving requests.",
        });
      lastHealth = cls;
    }
    for (const run of s.status.runs) {
      for (const t of run.targets) {
        if (t.state !== "failed") continue;
        const ref = t.outputRef;
        const key = ref ? "fail:" + ref : "dash:fail:" + run.inv + ":" + t.label;
        const link =
          ref && s.liveHost
            ? { label: "Open in log viewer", href: logsLink(s.liveHost, { ref }) }
            : undefined;
        notify({ source: "Dashboard", kind: "error", key, message: t.label + " failed.", link });
      }
    }
  });
}

// ---- tiles -----------------------------------------------------------------
let tiles: Tile[] = [];

function mountTiles(): void {
  const host = el("dash-panels");
  host.replaceChildren();

  // The dashboard header row (the active-workspace picker, shown past a single workspace, +
  // the Big Picture fullscreen-presentation button) is chrome, not a tile in the ordered board
  // below - it stays visible in both modes, so it is mounted first and excluded from the
  // board/Big Picture hide toggle.
  const header = dashboardHeader();
  host.append(header.el);

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
  pool.el.dataset.half = "";
  cacheStats.el.dataset.half = "";

  const attention = attentionTile();
  const activity = activityTile();
  const gantt = ganttTile(); // the live execution timeline (fed by Status.runs)
  const utilization = utilizationTile();
  const cacheRate = cacheRateTile();
  const targets = targetsTile();
  const remote = remoteTile();
  const workspaces = workspacesTile(activeWorkspace);
  const services = servicesTile();
  const config = configTile();
  const latency = latencyTile();
  const buzz = buzzTile();
  const sandbox = sandboxTile();
  const mcp = mcpTile();

  // Ordered, full-width by default (pool/cacheStats opt into the half-width pair row).
  const ordered: Tile[] = [
    attention,
    activity,
    pool,
    cacheStats,
    remote,
    gantt,
    cacheRate,
    utilization,
    targets,
    workspaces,
    services,
    config,
    latency,
    buzz,
    sandbox,
    mcp,
  ];
  for (const t of ordered) host.append(t.el);

  // The Insight section: the five VCS/run-outcome lenses, fed by the on-demand
  // /api/v1/insight poll. Its refresh button forces an out-of-band refetch.
  const insight = insightSection(() => transport.refreshInsight());
  host.append(insight.el);
  for (const t of insight.tiles) host.append(t.el);

  // Big Picture: the TV-friendly summary the fullscreen button swaps in. Mounted alongside the
  // board (not built only when entered) so it keeps updating in the background and flipping to
  // it is instant.
  const bigPicture = bigPictureTile();
  host.append(bigPicture.el);

  tiles = [header, ...ordered, ...insight.tiles, bigPicture];

  // Chrome first, then tiles: the panels are revealed before a chart tile builds.
  store.subscribe(renderStatusBar);
  for (const t of tiles) store.subscribe((s) => t.update(s));

  // Board vs Big Picture: exactly one shows at a time; the header row itself always stays
  // visible. A data attribute, not .hidden: several tiles (config/services/remote/buzz/sandbox)
  // already manage their OWN .hidden as a "waiting for data" latch inside update(), which runs
  // on every store tick AFTER this - fighting over .hidden would flicker the board back on.
  // [data-view-hide] is touched only here, so the two never collide (dashboard.css draws the
  // actual display:none). Bound once here, un-disposed on a later remount - the same lifetime
  // the store.subscribe calls above already have (mountTiles rebuilds the whole panel host on
  // re-mount).
  const boardEls: HTMLElement[] = [
    ...ordered.map((t) => t.el),
    insight.el,
    ...insight.tiles.map((t) => t.el),
  ];
  bind(viewMode, (mode) => {
    for (const e of boardEls) e.toggleAttribute("data-view-hide", mode !== "board");
    bigPicture.el.toggleAttribute("data-view-hide", mode !== "bigPicture");
  });
}

// ---- demo mode -------------------------------------------------------------
// The daemon-free showcase: synthesize a live-looking DashboardState (demo.ts) and
// push it into the store, so the whole board can be shown off with nothing running.
// No socket is opened; the connection pill reads "demo data".
let demo: DemoHandle | null = null;
function beginDemo(): void {
  transport.stop(); // make sure no resume loop is racing the demo feed
  demo?.stop();
  setConn({ state: "demo" });
  // Synthesize an observing-since ~92 minutes back so the demo shows the same since-caption a live
  // daemon would (the real value comes from the JSON status endpoint on connect).
  store.set({ observingSince: Date.now() - 92 * 60 * 1000 });
  store.set({ config: { defaultCharms: ["rw"], concurrency: 8, sandbox: true } });
  demo = startDemo(store);
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
  saveDaemon(host); // remember it so a reload resumes
}

// onLiveError debounces disconnection: a brief blip stays "reconnecting" and keeps the
// last data on screen; only after DISCONNECT_GRACE consecutive failures does the pill go
// "disconnected". A never-connected resume attempt that gives up shows the confirm form.
function onLiveError(host: string): void {
  failCount++;
  if (everConnected) {
    setConn({
      state: "disconnected",
      detail: failCount >= DISCONNECT_GRACE ? "disconnected" : "reconnecting",
    });
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
  setText(
    "dash-connect-sub",
    failed
      ? "The saved address didn't respond. Confirm it below, or start the daemon and open the link it prints."
      : "Resume your last daemon, or start a new one below.",
  );
}

// wireDemoButton wires the empty-state "See a demo" button. It enters the showcase in place by calling
// beginDemo() directly - NOT by reloading. A reload was fine on the standalone page but wrong inside the
// console, where it would tear down the whole SPA (every tab) instead of just this surface. The #demo
// fragment is still recorded (via replaceState, so no reload and no hashchange that a sibling pane would
// react to) so a standalone refresh stays in the demo and the URL reads as a shareable /#demo.
function wireDemoButton(): void {
  const btn = opt("dash-demo-btn");
  if (!btn) return;
  btn.addEventListener("click", () => {
    history.replaceState(null, "", "#demo");
    beginDemo();
  });
}

function wireResumeForm(): void {
  const form = opt("dash-resume") as HTMLFormElement | null;
  if (!form) return;
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    const host = normalizeDaemonHost((el("dash-resume-host") as HTMLInputElement).value.trim());
    if (!host) {
      setText("dash-connect-sub", "Enter a port (for example 8787) or a full 127.0.0.1:port.");
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
    setText(
      "dash-connect-sub",
      "The dashboard streams a running magus daemon's pool, cache, and health. Start the daemon, then open the live link it prints.",
    );
    setConn({ state: "none" });
  });
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
// activate boots the dashboard against the scaffold already in the document. Every DOM handle is
// resolved at call time (el()/opt() are getElementById), so it needs no separate resolve step - it
// just needs the scaffold present. Exported so the console's dashboard PageModule can drive it after
// injecting the scaffold into a host; the standalone page auto-boots below.
let notificationsWired = false;
export function activate(): void {
  document.documentElement.classList.remove("no-js");
  registerServiceWorker();
  mountTiles();
  // Subscribe the notification watcher once per page lifetime: the module-scoped store outlives a
  // console tab close/reopen, so re-subscribing on every activate() would double-fire.
  if (!notificationsWired) {
    notificationsWired = true;
    wireNotifications();
  }
  wireResumeForm();
  wireDemoButton();

  const badge = opt("offline-badge");
  const updateOffline = (): void => {
    if (badge) badge.hidden = navigator.onLine;
  };
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

  // An explicit attach (a #port link magus printed, or the daemon-origin/shared console) always wins.
  const attach = daemonAttach(params);
  if (attach) {
    connectLive(attach);
    return;
  }
  // A malformed #port is an explicit-but-broken attach: say so rather than silently resuming something else.
  if (params.port !== undefined) {
    setConn({ state: "disconnected", detail: "invalid port" });
    setText("dash-connect-title", "Can't connect");
    setText(
      "dash-connect-sub",
      "The #port must be a plain port number (1-65535). Re-open the link magus printed.",
    );
    return;
  }

  // No link in the URL: optimistically resume the last daemon we connected to.
  const saved = savedDaemon();
  const savedHost = saved ? validateLoopbackHost(saved) : null;
  if (savedHost) {
    setText("dash-connect-title", "Reconnecting...");
    setText("dash-connect-sub", "Resuming your last daemon at " + savedHost + ".");
    connectLive(savedHost); // the normalized host, matching the #port and resume-form paths
    return;
  }
  // No remembered daemon, but the operator set a default host in Settings (the loopback override):
  // connect to it.
  const configured = getDefaultHost();
  const configuredHost = configured ? validateLoopbackHost(configured) : null;
  if (configuredHost) {
    setText("dash-connect-title", "Reconnecting...");
    setText("dash-connect-sub", "Connecting to your configured daemon at " + configuredHost + ".");
    connectLive(configuredHost);
    return;
  }
  setConn({ state: "none" });
}

// deactivate tears down the dashboard's live feeds and the demo timer, so closing its console tab or
// pane leaves no SSE stream reconnecting or synthesized-demo interval ticking in the background.
// transport.stop() latches the give-up flag and aborts every feed; the demo handle stops its interval.
// The standalone page never calls this (the surface lives for the page's lifetime); the console's
// dashboard PageModule calls it on deactivate.
export function deactivate(): void {
  transport.stop();
  demo?.stop();
  demo = null;
}

// Standalone auto-boot: only when the scaffold is already in the document at load. In the console the
// scaffold is injected into a host AFTER this module imports, so the console calls activate() itself.
if (document.getElementById("dash-connect")) activate();
