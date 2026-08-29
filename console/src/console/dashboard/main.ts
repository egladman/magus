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
import { showCountdownToast, showRefreshToast } from "../../lib/refresh-toast";
import { registerServiceWorker } from "../../lib/sw";
import { bind } from "../view";
import { initialState, type DashboardState, type ConnView } from "./state";
import { DashboardTransport } from "./transport";
import { startDemo, type DemoHandle } from "./demo";
import { helpGlyph, type Tile } from "./tiles/card";
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
import { agentsTile } from "./tiles/agents";
import { leaseTile } from "./tiles/lease";
import { openSurface } from "../surface-navigation";
import { workspacesTile } from "./tiles/workspaces";
import { locksTile } from "./tiles/locks";
import { servicesTile } from "./tiles/services";
import { configTile } from "./tiles/config";
import { ganttTile } from "./tiles/gantt";
import { insightSection } from "./tiles/insight";
import { toolchainTile } from "./tiles/toolchain";
import { mountAlertRail } from "./tiles/alerts";
import { mountSplitHandle } from "./tiles/split";
import { mountRotator, type Rotator } from "./tiles/rotator";
import {
  viewMode,
  dashboardHeader,
  activeWorkspace,
  enterBigPictureRoute,
  resetBigPicture,
} from "./tiles/bigPicture";
// The dashboard is only ever mounted as a console surface now (the decoupled console has no standalone
// docs page), so it wires NO docs-site chrome of its own - the console frame owns the title bar, tab
// strip, settings gear, and status bar. (Its old standalone-only initNav/initSearch/initRefDrawer/
// initConsoleSettings self-wiring was dropped with the docs-page decoupling.)
import { getDefaultHost } from "../../lib/settings";
import { activate as activatePlan } from "../plan/main";
import type { SurfaceInstance } from "../standalone";
import { publishStatus } from "../status";

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
type DashboardMode = "overview" | "plan";
type DashboardViewWindow = Window & { __magusConsoleDashboardView?: DashboardMode };
let dashboardMode: DashboardMode = "overview";
let planMount: SurfaceInstance | null = null;

function disposePlan(): void {
  planMount?.deactivate();
  planMount = null;
}

function setDashboardMode(mode: DashboardMode): void {
  const main = el("dash-main");
  const overview = el("dash-overview");
  const planHost = el("dash-plan-host");
  opt("dash-plan-controls")?.toggleAttribute("hidden", mode !== "plan");
  dashboardMode = mode;
  main.dataset.mode = mode;
  overview.hidden = mode !== "overview";
  planHost.hidden = mode !== "plan";
  if (mode === "plan") {
    if (!planMount) planMount = activatePlan(planHost);
    planMount.setVisible?.(!surfaceHidden);
    return;
  }
  // The plan's keyboard commands and poller only make sense while its mode is in front. Rebuilding
  // it on return is deliberate: it leaves no hidden command owner or retained canvas behind.
  disposePlan();
}

function takeDashboardViewIntent(): DashboardMode | null {
  const win = window as DashboardViewWindow;
  const mode = win.__magusConsoleDashboardView;
  delete win.__magusConsoleDashboardView;
  return mode === "plan" || mode === "overview" ? mode : null;
}

export function setVisible(visible: boolean): void {
  surfaceHidden = !visible;
  if (visible) transport.resume();
  else transport.suspend();
  planMount?.setVisible?.(visible && dashboardMode === "plan");
  const big = viewMode.get() === "bigPicture";
  for (const tile of tiles) tile.setVisible?.(visible && (!big || !boardOnlyTiles.has(tile)));
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

  // The status bar distinguishes a live daemon from the synthesized demo feed.
  let connection: "none" | "connecting" | "connected" | "disconnected" | "demo" = "none";
  let connectionLabel = "not connected";
  let health: string | undefined;
  if (demoing) {
    connection = "demo";
    connectionLabel = "demo";
  } else {
    const map: Record<string, string> = {
      connecting: "connecting...",
      connected: "connected",
      disconnected: s.conn.detail || "reconnecting",
      none: "not connected",
    };
    connectionLabel = map[s.conn.state] || s.conn.state;
    connection =
      s.conn.state === "connected" ||
      s.conn.state === "connecting" ||
      s.conn.state === "disconnected"
        ? s.conn.state
        : "none";
    if (s.conn.state === "connected" && s.status) {
      health = s.status.health.cls;
    }
  }

  // Observing-since: a brief note of when the daemon began collecting these counters, so it is
  // clear the numbers are cumulative from then and are NOT persisted across daemon restarts.
  let observing: { text: string; title: string } | undefined;
  if (s.observingSince) {
    const t = new Date(s.observingSince).toLocaleTimeString([], {
      hour: "2-digit",
      minute: "2-digit",
    });
    observing = {
      text: "observing since " + t,
      title:
        "The telemetry and cache counters are cumulative since the daemon started observing (" +
        t +
        "). They are not persisted across daemon restarts.",
    };
  }
  publishStatus({
    connection,
    label: connectionLabel,
    health,
    hint: demoing ? "Demo data is synthetic. Click to change the daemon address." : "",
    observing,
  });
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
let rotator: Rotator | null = null;

// Board sections label direct panel children. asHint puts detail behind the shared "?" glyph
// instead of an always-visible sentence - for a section read on every load (unlike a tile's own
// why, opened only on demand), a full sentence in the default view competes with the cards it
// introduces for exactly the attention those cards are trying to earn.
function boardSection(label: string, detail: string, asHint = false): HTMLElement {
  const section = document.createElement("div");
  section.className = "console-dashboard-section";
  section.dataset.boardOnly = "";
  const title = document.createElement("h2");
  title.textContent = label;
  section.append(title);
  if (asHint) {
    section.append(helpGlyph(detail, label));
  } else {
    const sub = document.createElement("p");
    sub.textContent = detail;
    section.append(sub);
  }
  return section;
}
// Store subscriptions taken out by mountTiles, and the controller for activate()'s
// window-level listeners. mountTiles runs again on every reopen (the rotator comment below
// says so), so without dropping these the previous generation of tiles stays subscribed:
// each dead board keeps its own live uPlot canvas and interval, updating on every status
// frame, forever. The rotator already handled this one tile at a time; this is the same
// reasoning applied to the whole set.
let tileDisposers: (() => void)[] = [];
let boardDisposers: (() => void)[] = [];
let boardOnlyTiles = new Set<Tile>();
let lifecycleAbort: AbortController | null = null;

// releaseTiles drops the previous board: unsubscribe first so nothing can be updated while it
// is being torn down, then destroy each tile (charts, intervals, observers).
function releaseTiles(): void {
  for (const off of tileDisposers) off();
  tileDisposers = [];
  for (const dispose of boardDisposers) dispose();
  boardDisposers = [];
  for (const t of tiles) t.destroy();
  tiles = [];
  boardOnlyTiles = new Set();
}

function mountTiles(): void {
  const host = el("dash-panels");
  host.replaceChildren();

  // The dashboard header (the active-workspace picker, shown past a single workspace, + the Big
  // Picture button) is chrome, not a tile in the ordered board below, so it is excluded from the
  // board/Big Picture hide toggle.
  //
  // It mounts into the surface BAR rather than into the board, at the bar's trailing edge. As its
  // own row inside #dash-panels it was a second full-width strip carrying one right-aligned
  // button, stacked under the related-work links that are now its other half.
  const header = dashboardHeader();
  const bar = document.querySelector(".console-dashboard-related");
  (bar ?? host).append(header.el);

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
  const lease = leaseTile();
  const activity = activityTile();
  const agents = agentsTile();
  const gantt = ganttTile(); // the live execution timeline (fed by Status.runs)
  const utilization = utilizationTile();
  const cacheRate = cacheRateTile();
  const targets = targetsTile();
  const remote = remoteTile();
  const workspaces = workspacesTile(activeWorkspace);
  const services = servicesTile();
  const locks = locksTile();
  const config = configTile();
  const latency = latencyTile();
  const buzz = buzzTile();
  const sandbox = sandboxTile();
  const mcp = mcpTile();
  const toolchain = toolchainTile();
  const insight = insightSection(() => transport.refreshInsight());

  type BoardSection = "live" | "runtime" | "diagnostics" | "intelligence";
  type BigPictureMembership = "always" | "rotate" | "board";
  interface BoardTile {
    tile: Tile;
    section: BoardSection;
    bigPicture: BigPictureMembership;
  }

  // One composition list owns both reading order and Big Picture membership. A tile never gets a
  // mode-specific renderer; it only declares whether its existing renderer is permanent, rotated,
  // or board-only in the presentation layout.
  const boardTiles: BoardTile[] = [
    { tile: attention, section: "live", bigPicture: "always" },
    { tile: lease, section: "live", bigPicture: "always" },
    { tile: agents, section: "live", bigPicture: "always" },
    { tile: activity, section: "live", bigPicture: "always" },
    { tile: gantt, section: "live", bigPicture: "always" },
    { tile: pool, section: "runtime", bigPicture: "always" },
    { tile: cacheStats, section: "runtime", bigPicture: "always" },
    { tile: locks, section: "runtime", bigPicture: "always" },
    { tile: workspaces, section: "runtime", bigPicture: "always" },
    { tile: remote, section: "runtime", bigPicture: "rotate" },
    { tile: cacheRate, section: "runtime", bigPicture: "rotate" },
    { tile: utilization, section: "runtime", bigPicture: "rotate" },
    { tile: targets, section: "diagnostics", bigPicture: "rotate" },
    { tile: services, section: "diagnostics", bigPicture: "rotate" },
    { tile: config, section: "diagnostics", bigPicture: "board" },
    { tile: latency, section: "diagnostics", bigPicture: "board" },
    { tile: buzz, section: "diagnostics", bigPicture: "board" },
    { tile: sandbox, section: "diagnostics", bigPicture: "board" },
    { tile: mcp, section: "diagnostics", bigPicture: "board" },
    { tile: toolchain, section: "intelligence", bigPicture: "board" },
    ...insight.tiles.map((tile) => ({
      tile,
      section: "intelligence" as const,
      bigPicture: "board" as const,
    })),
  ];
  const appendSection = (section: BoardSection, title: string, description: string): void => {
    host.append(boardSection(title, description, true));
    for (const item of boardTiles) if (item.section === section) host.append(item.tile.el);
  };
  appendSection("live", "Live work", "Decide, coordinate, and follow the work that is moving now.");
  appendSection(
    "runtime",
    "Runtime",
    "Capacity, locks, and cache health: the constraints around that work.",
  );
  appendSection(
    "diagnostics",
    "Diagnostics",
    "Detailed target, service, and tool health when the live picture needs explanation.",
  );
  host.append(
    boardSection(
      "Workspace intelligence",
      "Longer-horizon signals that explain why this workspace behaves the way it does.",
      true,
    ),
  );
  for (const item of boardTiles) {
    if (item.section !== "intelligence") continue;
    if (item.tile === toolchain) host.append(item.tile.el, insight.el);
    else host.append(item.tile.el);
  }

  // Big Picture keeps alerts outside the tile grid.
  const alerts = mountAlertRail();
  host.append(alerts.el);

  // The split handle tracks the activity column.
  const split = mountSplitHandle(host);
  host.append(split.el);
  boardDisposers = [() => alerts.destroy(), () => split.destroy()];

  tiles = [header, insight, ...boardTiles.map((item) => item.tile)];
  boardOnlyTiles = new Set(
    boardTiles.filter((item) => item.bigPicture === "board").map((item) => item.tile),
  );

  // Render chrome before tiles.
  tileDisposers.push(store.subscribe(renderStatusBar));
  for (const t of tiles) tileDisposers.push(store.subscribe((s) => t.update(s)));

  // Big Picture reuses these tiles; membership controls what remains visible.
  const rotating = boardTiles
    .filter((item) => item.bigPicture === "rotate")
    .map((item) => item.tile.el);
  const bigPictureEls = new Set(
    boardTiles.filter((item) => item.bigPicture !== "board").map((item) => item.tile.el),
  );

  // Pause rotation while the verdict needs attention.
  rotator?.destroy();
  rotator = mountRotator(rotating, {
    paused: () => attention.el.dataset.state !== "clear",
  });

  // Keep view visibility separate from tile state.
  const boardEls: HTMLElement[] = [
    ...boardTiles.map((item) => item.tile.el),
    insight.el,
    ...[...host.querySelectorAll<HTMLElement>("[data-board-only]")],
  ];
  tileDisposers.push(
    bind(viewMode, (mode) => {
      const big = mode === "bigPicture";
      for (const e of boardEls) e.toggleAttribute("data-view-hide", big && !bigPictureEls.has(e));
      for (const tile of boardOnlyTiles) tile.setVisible?.(!big && !surfaceHidden);
    }),
  );
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
// The registration itself lives in lib/sw.ts (the shell registers the same worker at boot, so a console
// that never opens this surface still has one). What stays HERE is the dashboard's own reaction to a new
// version, which no other surface wants: this is the screen that is left running unattended.
function registerDashboardServiceWorker(): void {
  void registerServiceWorker(new URL("../sw.js", import.meta.url)).then((reg) => {
    if (!reg) return;
    watchForNewVersion(reg);
    pollForNewVersion(reg);
  });
}

// How often a tab re-checks the server for a new worker. Registration alone does not: the browser
// revalidates sw.js on navigation, and the surface this matters most for is the one that never
// navigates. A wall display opened on Monday would sit on Monday's bundle until someone walked over
// and reloaded it, which is precisely what the display exists to avoid.
//
// Cheap enough to be uninteresting - one conditional request for a file of a few kB, and a 304 when
// nothing moved. update() resolves without installing anything when the bytes match, so the
// downstream announcement only ever fires on a real change.
const UPDATE_POLL_MS = 15 * 60 * 1000;

function pollForNewVersion(reg: ServiceWorkerRegistration): void {
  const check = () => {
    reg.update().catch(() => {});
  };
  check(); // on boot, so a tab restored from the bfcache is current straight away
  setInterval(check, UPDATE_POLL_MS);
}

// How long the board announces a pending refresh before taking it. Long enough that someone walking
// past can read it and hit Cancel, short enough that a wall display is not left on a stale build.
const AUTO_REFRESH_SECONDS = 10;

// watchForNewVersion turns a service-worker update into a refresh.
//
// Nothing did this before: sw.js calls skipWaiting()/clients.claim(), so a new worker takes over at
// once, but the PAGE keeps executing the bundle it loaded until something reloads it. On a desk that
// is a stale tab until you happen to hit refresh. On a wall display it is indefinite - the whole
// point of the screen is that nobody touches it, so nobody ever reloads it, and it can sit on a
// build from days ago while looking perfectly live.
//
// The response splits on whether a person is there:
//   Big Picture - announce and DO IT. A "click to refresh" prompt on an unattended screen is a
//     prompt that is never clicked. It still counts down visibly and offers Cancel, so someone who
//     IS standing there is not ambushed.
//   Board - ASK, via the existing prompt. Someone is working: reloading out from under them could
//     lose a scroll position, a filter, a half-read log.
function watchForNewVersion(reg: ServiceWorkerRegistration): void {
  reg.addEventListener("updatefound", () => {
    const next = reg.installing;
    if (!next) return;
    next.addEventListener("statechange", () => {
      // `installed` WITH an existing controller is an update. Without one it is the very first
      // install on this origin, which is not a new version and must not trigger a reload loop.
      if (next.state !== "installed" || !navigator.serviceWorker.controller) return;
      onNewVersion();
    });
  });
}

let refreshPending = false;
function onNewVersion(): void {
  if (refreshPending) return; // one announcement per page lifetime
  refreshPending = true;

  if (viewMode.get() !== "bigPicture") {
    showRefreshToast("Dashboard", "A new version of the console is available.");
    return;
  }
  const cancel = showCountdownToast(
    "Dashboard",
    (s) => "New version available. Refreshing in " + s + "s.",
    AUTO_REFRESH_SECONDS,
    () => location.reload(),
  );
  // Leaving Big Picture mid-countdown means a person just took control, so the automatic reload is
  // no longer the right call - fall back to the prompt they can act on.
  const unbind = viewMode.subscribe((mode) => {
    if (mode === "bigPicture") return;
    cancel();
    unbind();
    showRefreshToast("Dashboard", "A new version of the console is available.");
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
  registerDashboardServiceWorker();
  // Drop the previous generation before building the next one. activate() runs again every
  // time the console reopens this surface, and the module (with its store and tile list)
  // outlives the tab.
  releaseTiles();
  disposePlan();
  lifecycleAbort?.abort();
  lifecycleAbort = new AbortController();
  mountTiles();
  // Subscribe the notification watcher once per page lifetime: the module-scoped store outlives a
  // console tab close/reopen, so re-subscribing on every activate() would double-fire.
  if (!notificationsWired) {
    notificationsWired = true;
    wireNotifications();
  }
  wireResumeForm();
  opt("dash-plan-back")?.addEventListener("click", () => setDashboardMode("overview"), {
    signal: lifecycleAbort?.signal,
  });

  window.addEventListener(
    "console:dashboard-view",
    (event) => {
      const mode = (event as CustomEvent<{ mode?: DashboardMode }>).detail?.mode;
      if (mode === "plan" || mode === "overview") setDashboardMode(mode);
    },
    { signal: lifecycleAbort?.signal },
  );
  setDashboardMode(takeDashboardViewIntent() ?? "overview");

  document.querySelectorAll<HTMLElement>("#dash-main [data-open-surface]").forEach((button) => {
    button.addEventListener(
      "click",
      () => {
        const pageId = button.dataset.openSurface;
        if (pageId) openSurface({ pageId });
      },
      { signal: lifecycleAbort?.signal },
    );
  });

  const badge = opt("offline-badge");
  const updateOffline = (): void => {
    if (badge) badge.hidden = navigator.onLine;
  };
  updateOffline();
  const sig = lifecycleAbort?.signal;
  window.addEventListener("online", updateOffline, { signal: sig });
  window.addEventListener("offline", updateOffline, { signal: sig });

  const params = parseHash();
  consumeLiveToken(params);

  // A `#big-picture` fragment enters the presentation mode with NO user gesture, which is the whole
  // reason it exists: the Fullscreen API requires one, so a TV, an HDMI stick, or a kiosk browser
  // pointed at a link could never reach the mode through the button. Applied here, before any
  // connection path is chosen, so it composes with all of them - `#port=7391&big-picture` for a live
  // daemon and `#demo&big-picture` for an offline showcase both land in the mode.
  if (params["big-picture"] !== undefined) enterBigPictureRoute();

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
  resetBigPicture();
  disposePlan();
  transport.stop();
  demo?.stop();
  demo = null;
  rotator?.destroy();
  rotator = null;
  releaseTiles();
  lifecycleAbort?.abort();
  lifecycleAbort = null;
}

// Standalone auto-boot: only when the scaffold is already in the document at load. In the console the
// scaffold is injected into a host AFTER this module imports, so the console calls activate() itself.
if (document.getElementById("dash-connect")) activate();
