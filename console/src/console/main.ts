// main.ts - the console composition root and SPA-island entry. The page supplies three slots - the
// tab bar host (#console-tabs), the content outlet (#console-outlet), and the status-bar footer
// (#console-statusbar) - and this wires them: it mounts one surface per open tab (each kept in the
// DOM and hidden when inactive, so switching is instant and closing tears down), and swaps the active
// tab's status bar into the footer so the bottom bar is PER-TAB. The active set is the persisted
// Workspace (tabs.ts), so the console reopens exactly as you left it. Surfaces are PageModules
// (page.ts); a heavy one activates lazily (its bundle a dynamic import) so a tab stays cheap until
// opened. The four core lenses are real surfaces (logs/graph/dashboard/activity). The launcher
// (home.ts) is NOT a tab - it is the outlet's empty state, shown whenever zero tabs are open.

import { openTab, closeTab, setActive, setLayout, workspaceStore, type TabState } from "./tabs";
import { createTabBar } from "./tabBar";
import { buildLauncher, type Launchable } from "./home";
import { standaloneSurface, moduleSurface } from "./standalone";
import { registerCommand, dispatchCommand, listCommands, installKeybindings, mergeKeymap, formatChord, isMac, type Keymap } from "./commands";
import { createCommandBar } from "./commandBar";
import { createKeybindingsOverlay } from "./keybindings";
import { settingsSurface } from "./settings/surface";
import { createCheatsheet } from "./cheatsheet";
import { createActionsSurface } from "./actions";
import { createTileView, type TileView } from "./tileView";
import { leaves, type Pane, type Leaf, type Split } from "./tiling";
import { initRefDrawer } from "../ui/ref-drawer";
import { initAppMenu } from "../ui/app-menu";
import { openSurfaceWindow } from "../lib/appwindow";
import { persisted } from "../lib/persist";
import { parseHash, wantsDemo, validateLiveHost, getLiveToken, authHeaders, fetchReadiness, type ReadinessReport, type ReadinessComponent } from "../lib/daemon";
import { applyFocusRing, getFocusRing, getDefaultHost } from "../lib/settings";
import type { PageController, PageModule } from "./page";

// The console's default tab keybindings. Flat commandId -> chord, layered over the user's persisted
// "keymap" overrides (the same cell the surfaces read). mod = Cmd on macOS, Ctrl elsewhere. Cmd+Opt
// arrows match a browser/editor's next/prev-tab feel; new/close are the conventional mod+t / mod+w
// (they land on the console's own tabs when it runs as an installed PWA window).
const CONSOLE_KEYMAP: Keymap = {
  "console.tab.close": "mod+w", // closes the focused PANE, or the tab when it is the last pane
  "console.tab.next": "mod+alt+ArrowRight",
  "console.tab.prev": "mod+alt+ArrowLeft",
  // Tiling: split the focused pane in the persisted default direction, toggle that default, and
  // vim-style directional pane focus/move. alt+shift+hjkl mirrors alt+hjkl (focus) one modifier over,
  // so "move" reads as "focus, but it drags the pane with it". alt+a jumps back across the nearest
  // divider to the pane the current one was split from (siblingLeafId in tiling.ts).
  "console.pane.split": "mod+\\",
  "console.pane.toggleSplitMode": "mod+shift+\\",
  "console.pane.focusLeft": "alt+h",
  "console.pane.focusDown": "alt+j",
  "console.pane.focusUp": "alt+k",
  "console.pane.focusRight": "alt+l",
  "console.pane.moveLeft": "alt+shift+h",
  "console.pane.moveDown": "alt+shift+j",
  "console.pane.moveUp": "alt+shift+k",
  "console.pane.moveRight": "alt+shift+l",
  "console.pane.focusParent": "alt+a",
  // The action bar: one searchable list of every action (and its chord).
  "console.actionBar.open": "mod+k",
};
const keymapCell = persisted<Keymap>("keymap", {});
// The default direction a plain split takes (mod+\\, and the Panes tray's Horizontal/Vertical picks
// below): "row" (side by side) or "col" (stacked). Global and persisted - a choice made once, by the
// keyboard toggle or an explicit pick, sticks across every tab and reload rather than resetting.
const splitMode = persisted<"row" | "col">("split-mode", "row");

const registry = new Map<string, PageModule<any, any>>();
function register(m: PageModule<any, any>): void { registry.set(m.id, m); }

// The surfaces the home launcher offers (and the console can open).
const SURFACES: Launchable[] = [
  { pageId: "logs", label: "Log Viewer", hint: "Read a run's captured output" },
  { pageId: "graph", label: "Graph Explorer", hint: "Start exploring the knowledge graph" },
  { pageId: "dashboard", label: "Dashboard", hint: "What magus is doing right now" },
  { pageId: "activity", label: "Activity Trail", hint: "A history of recent magus actions" },
  { pageId: "actions", label: "Actions", hint: "Every console action and its shortcut" },
  { pageId: "settings", label: "Settings", hint: "Console settings and keybindings" },
];


interface Mounted { host: HTMLElement; status: HTMLElement; tile: TileView; }

// The status bar shows the connected daemon's build: its version inline, the full fingerprint on
// hover. Read from GET /api/v1/status (build_info) - the running binary reports its own identity, so
// the bar reflects the daemon you are talking to. In the daemon-free demo it shows a demo value; with
// no daemon and no demo the chip stays hidden. Cached once and applied to every tab's status bar.
let buildVersion: string | null = null;
let buildFingerprint = "";

function fillVersionChip(el: HTMLElement): void {
  if (!buildVersion) return;
  el.textContent = buildVersion;
  el.title = buildFingerprint || "magus " + buildVersion;
  el.hidden = false;
}

function setBuild(version: string, fingerprint: string): void {
  if (!version) return;
  buildVersion = version;
  buildFingerprint = fingerprint;
  document.querySelectorAll<HTMLElement>("[data-version-chip]").forEach(fillVersionChip);
}

function loadBuildInfo(): void {
  const params = parseHash();
  if (wantsDemo(params)) { setBuild("v1.4.2", "magus v1.4.2 (a1b2c3d) built 2026-07-16T00:00:00Z"); return; }
  const host = params.live ? validateLiveHost(params.live) : null;
  if (!host) return;
  fetch("http://" + host + "/api/v1/status", { headers: authHeaders(getLiveToken()) })
    .then((r) => (r.ok ? r.json() : null))
    .then((st) => { if (st?.build_info?.version) setBuild(st.build_info.version, st.build_info.fingerprint || ""); })
    .catch(() => {});
}

// keyboardIcon returns the status-bar shortcuts glyph as an inline SVG (keys row over a space bar),
// built via createElementNS to match the console's icon convention (no innerHTML, themes on currentColor).
function keyboardIcon(): SVGElement {
  const NS = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("width", "14");
  svg.setAttribute("height", "14");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.6");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  const rect = document.createElementNS(NS, "rect");
  rect.setAttribute("x", "2.5");
  rect.setAttribute("y", "6");
  rect.setAttribute("width", "19");
  rect.setAttribute("height", "12");
  rect.setAttribute("rx", "2");
  const keys = document.createElementNS(NS, "path");
  keys.setAttribute("d", "M6 10h.01M10 10h.01M14 10h.01M18 10h.01M8 14h8");
  svg.append(rect, keys);
  return svg;
}

// svgIcon returns a blank inline SVG shell with the console's shared icon defaults (14x14, stroke on
// currentColor so it themes for free, aria-hidden since every caller pairs it with a labeled button).
// The panes-tray icons below each add their own shape to it - one place to keep that boilerplate in sync.
function svgIcon(): SVGElement {
  const NS = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("width", "14");
  svg.setAttribute("height", "14");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.7");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  return svg;
}

// panesIcon is the tray Panes button's glyph: a framed rect divided by a line whose ORIENTATION
// mirrors the current split mode - a vertical divider for "row" (a plain split puts panes side by
// side), a horizontal one for "col" (stacked) - so the icon doubles as an at-a-glance readout of the
// default direction, not just a generic tiling glyph.
function panesIcon(mode: "row" | "col"): SVGElement {
  const NS = "http://www.w3.org/2000/svg";
  const svg = svgIcon();
  const rect = document.createElementNS(NS, "rect");
  rect.setAttribute("x", "3"); rect.setAttribute("y", "4");
  rect.setAttribute("width", "18"); rect.setAttribute("height", "16"); rect.setAttribute("rx", "2");
  const line = document.createElementNS(NS, "line");
  if (mode === "row") {
    line.setAttribute("x1", "12"); line.setAttribute("y1", "4");
    line.setAttribute("x2", "12"); line.setAttribute("y2", "20");
  } else {
    line.setAttribute("x1", "3"); line.setAttribute("y1", "12");
    line.setAttribute("x2", "21"); line.setAttribute("y2", "12");
  }
  svg.append(rect, line);
  return svg;
}

// setPanesIcon repaints an already-built tray button's glyph in place - called once per tray button at
// creation, and again on every button whenever the split mode changes (refreshPanesTray).
function setPanesIcon(btn: HTMLElement, mode: "row" | "col"): void {
  const iconSpan = btn.querySelector<HTMLElement>(".pf-v6-c-button__icon");
  if (iconSpan) iconSpan.replaceChildren(panesIcon(mode));
}

// makeStatusBar builds one tab's status bar: the SAME element ids the surfaces write to
// (#console-conn, #console-observing, #console-count) and the .console-shell-statusbar__right slot the
// log viewer injects its zoom control into. It is a real element (not an innerHTML snapshot) so the
// surface's live handles + listeners survive tab switches. Only the ACTIVE tab's status bar is attached
// to the footer, so getElementById resolves to the active surface's status - the bottom bar is per-tab.
//
// The text items (#console-conn with its liveness dot, #console-count, #console-observing) are plain
// spans the surfaces write via textContent + [data-state]/[data-health], styled ID-scoped in overrides.css.
// #console-conn also gets a periodic /readyz enrichment (title + data-health) from startConsole's
// readiness poller below - the surfaces still own textContent/data-state outright, the poller only
// touches those two when the launcher's default bar is docked (zero tabs open).
// Only .console-shell-statusbar__right stays a class - the log viewer queries it to inject its zoom control.
//
// withPanesButton adds the Panes tray toggle (data-panes-toggle) to the right cluster - every TAB's bar
// gets one (a tab always has at least one pane to act on), but the launcher's default bar does not:
// zero tabs means zero panes, so startConsole calls makeStatusBar(false) for that one.
function makeStatusBar(withPanesButton = true): HTMLElement {
  const bar = document.createElement("div");
  const left = document.createElement("div");
  left.dataset.cluster = "";
  const conn = document.createElement("span");
  conn.id = "console-conn"; conn.setAttribute("aria-live", "polite");
  conn.textContent = "not connected";
  // Clickable: a disconnected user's fastest fix is the daemon-address field, so the status pill
  // itself is the shortcut there (openDaemonSettings, wired via the delegated listener below). role +
  // tabindex make it a real keyboard-reachable control since a bare <span> is neither by default; the
  // aria-label is the static accessible name (what the click DOES), while .title carries the dynamic
  // last-probe sentence the readiness poller keeps current (what hovering SEES).
  conn.setAttribute("role", "button");
  conn.tabIndex = 0;
  conn.setAttribute("aria-label", "Configure daemon address");
  conn.title = "Configure daemon address";
  left.append(conn);
  const right = document.createElement("div");
  right.dataset.cluster = ""; right.className = "console-shell-statusbar__right";
  for (const id of ["console-count", "console-observing"] as const) {
    const s = document.createElement("span");
    s.id = id; s.dataset.item = ""; s.hidden = true; s.setAttribute("aria-live", "polite");
    right.append(s);
  }
  // Panes tray toggle: opens the popup that drives split/focus/move/close without a keyboard - the
  // touch-reachable route tiling used to lack entirely on a phone. Its glyph is a live readout of the
  // persisted split mode (panesIcon); refreshPanesTray (startConsole) repaints every tab's copy in
  // place whenever that mode changes. aria-controls points at the one shared popup element built once
  // in startConsole (its id, #console-panespopup, is stable regardless of which tab's button opened it).
  if (withPanesButton) {
    const panes = document.createElement("button");
    panes.type = "button";
    panes.className = "pf-v6-c-button pf-m-plain console-shell-statusbar__panes";
    panes.dataset.panesToggle = "";
    panes.setAttribute("aria-haspopup", "true");
    panes.setAttribute("aria-expanded", "false");
    panes.setAttribute("aria-controls", "console-panespopup");
    panes.setAttribute("aria-label", "Panes");
    panes.title = "Panes";
    const panesIconSpan = document.createElement("span");
    panesIconSpan.className = "pf-v6-c-button__icon";
    panesIconSpan.append(panesIcon(splitMode.get()));
    panes.append(panesIconSpan);
    right.append(panes);
  }
  // Keyboard-shortcuts toggle: a quiet icon button that flips the cheat sheet (the same overlay the
  // hold-"?" gesture reveals). data-cheatsheet-toggle is the hook; startConsole wires ONE delegated
  // click on the footer so every tab's button (built here) drives the single shared cheat sheet.
  const shortcuts = document.createElement("button");
  shortcuts.type = "button";
  shortcuts.className = "pf-v6-c-button pf-m-plain console-shell-statusbar__shortcuts";
  shortcuts.dataset.cheatsheetToggle = "";
  shortcuts.setAttribute("aria-label", "Keyboard shortcuts");
  shortcuts.title = "Keyboard shortcuts";
  const shortcutsIcon = document.createElement("span");
  shortcutsIcon.className = "pf-v6-c-button__icon";
  shortcutsIcon.append(keyboardIcon());
  shortcuts.append(shortcutsIcon);
  right.append(shortcuts);

  // Build fingerprint, far-right and quiet: version + commit inline, full detail on hover. Hidden
  // until version.json loads (fills from the cache if it already has).
  const ver = document.createElement("span");
  ver.className = "console-shell-statusbar__version";
  ver.dataset.versionChip = "";
  ver.hidden = true;
  fillVersionChip(ver);
  right.append(ver);
  bar.append(left, right);
  return bar;
}

// openDaemonSettings jumps a disconnected user straight to the fix: open the Settings surface (as a
// tab, focusing it if already open - open() is single-instance) then focus + scroll the daemon-address
// field once it exists. Settings activates asynchronously (its host pane mounts synchronously, but the
// controller - and the DOM inside it - resolves via mountSurface's awaited activate() a tick or more
// later; see settings/surface.ts), so the field is not guaranteed to exist the instant dispatchCommand
// returns. Poll a few animation frames rather than assume a fixed delay, and give up quietly past the
// deadline - the tab is open either way, so a user who does not get auto-focus can still find the field.
function openDaemonSettings(): void {
  dispatchCommand("console.open.settings");
  const deadline = Date.now() + 800;
  const tryFocus = (): void => {
    const input = document.getElementById("console-settings-host") as HTMLInputElement | null;
    if (input) { input.scrollIntoView({ block: "center" }); input.focus(); return; }
    if (Date.now() < deadline) requestAnimationFrame(tryFocus);
  };
  requestAnimationFrame(tryFocus);
}

// openKeybindings jumps to the Keybindings editor embedded in the Settings surface (not the modal
// overlay - a deep link lands on the surface's own persistent copy, which is what a return visit or
// bookmark would find again). Same async-mount pattern as openDaemonSettings above: Settings activates
// asynchronously, so poll a few animation frames for the target rather than assume a fixed delay, and
// give up quietly past the deadline. With a cmdId, scroll to and briefly highlight that command's row
// and focus its Record button (the row's first button) so a rebind is one click away; without one, just
// scroll the editor into view. Scoped to [data-surface="settings"] so this never matches the modal
// overlay's own [data-kbeditor] copy, which is present in the DOM (just hidden) the whole session.
function openKeybindings(cmdId?: string): void {
  dispatchCommand("console.open.settings");
  const deadline = Date.now() + 800;
  const tryFocus = (): void => {
    if (cmdId) {
      const row = document.querySelector<HTMLElement>('[data-surface="settings"] [data-kbeditor] [data-command="' + cmdId + '"]');
      if (row) {
        row.scrollIntoView({ block: "center" });
        row.dataset.kbHighlight = "";
        setTimeout(() => { delete row.dataset.kbHighlight; }, 1200);
        row.querySelector("button")?.focus();
        return;
      }
    } else {
      const editor = document.querySelector<HTMLElement>('[data-surface="settings"] [data-kbeditor]');
      if (editor) { editor.scrollIntoView({ block: "start" }); return; }
    }
    if (Date.now() < deadline) requestAnimationFrame(tryFocus);
  };
  requestAnimationFrame(tryFocus);
}

// ---- daemon readiness enrichment for #console-conn -------------------------
//
// Beyond the SSE-derived connected/disconnected signal each surface already owns, a periodic GET
// /readyz (daemon.fetchReadiness) gives a component-level health breakdown (workspaces, symbol index,
// services, knowledge graph). This is purely an ENRICHMENT layer: the title (hover detail) and the
// data-health dot color, applied to whichever status bar is currently docked. An old daemon that
// predates /readyz's CORS/JSON support degrades gracefully (fetchReadiness resolves null), so the UI
// falls back to whatever the SSE-derived state already says instead of a broken tooltip.

// readinessHealth derives the dot's ok/warn/fail tier from the report. "down" outranks "degraded" -
// a component that is actually down is worse than one merely degraded - and idle/disabled components
// are not treated as a problem (a disabled feature reporting itself disabled is working as intended).
function readinessHealth(report: ReadinessReport): "ok" | "warn" | "fail" {
  if (!report.ready) return "fail";
  if (report.components.some((c) => c.status === "down")) return "fail";
  if (report.components.some((c) => c.status === "degraded")) return "warn";
  return "ok";
}

// summarizeComponents renders each component as "name status (detail)", joined with "; " - compact
// enough for a tooltip line. Components with no detail (the common "ok" case) omit the parenthetical.
function summarizeComponents(components: ReadinessComponent[]): string {
  return components.map((c) => c.name + " " + c.status + (c.detail ? " (" + c.detail + ")" : "")).join("; ");
}

// formatReadinessTitle builds the #console-conn tooltip sentence for one probe outcome. ageSec is how
// long the check itself took to answer (fetchReadiness resolves right before this is called, so it
// reads as "how stale is this the moment you're seeing it"). A null report - old daemon or genuinely
// unreachable, indistinguishable from the browser's side - gets an actionable sentence rather than a
// guessed cause, always ending in the click hint so the enrichment doubles as a discoverability nudge.
function formatReadinessTitle(report: ReadinessReport | null, ageSec: number): string {
  if (!report) {
    return "Daemon health unavailable (update the daemon to see component status), last tried " + ageSec + "s ago. Click to set the daemon address.";
  }
  const statusLine = report.ready ? "200 (ready)" : "503 (not ready)";
  const summary = summarizeComponents(report.components);
  const base = "Last check: GET /readyz -> " + statusLine + ", " + ageSec + "s ago.";
  return summary ? base + " " + summary + "." : base;
}

export function startConsole(tabBarHost: HTMLElement, outlet: HTMLElement, statusHost: HTMLElement): void {
  loadBuildInfo(); // fetch the build fingerprint once; fills every status bar's version chip
  applyFocusRing(getFocusRing()); // apply the persisted focus-ring preference before anything renders
  const ws = workspaceStore();
  const mounts = new Map<string, Mounted>(); // tabId -> its mounted tile + status bar

  // The launcher is the outlet's EMPTY STATE, not a tab: one element appended straight into the
  // content outlet as a sibling of the tab panes, shown only when no tab is active (show(null)),
  // hidden the moment a tab activates. Clicking a card opens that surface as a real tab. It gets its
  // own default status bar (identical to what the old home tab supplied: a "not connected" dot and a
  // hidden Demo chip) so the footer stays populated at zero tabs.
  // launchDemo opens every surface in the daemon-free demo: it sets the shared #demo fragment each
  // surface reads when it activates, then opens them as tabs (Dashboard last so its live-updating demo
  // is the active tab). The launcher only shows at zero tabs, so all four mount fresh into demo mode.
  const launchDemo = (): void => {
    history.replaceState(null, "", location.pathname + location.search + "#demo");
    for (const id of ["logs", "graph", "activity", "dashboard"]) open(id);
  };
  const launcher = buildLauncher(SURFACES, open, launchDemo);
  launcher.hidden = true;
  outlet.append(launcher);
  const launcherStatus = makeStatusBar(false); // zero tabs, zero panes: no Panes tray button

  // mountSurface is how a tile mounts one surface into a pane host: resolve the registered module and
  // activate it, returning its controller (or null if unknown). A tile calls this per leaf, so all
  // the per-surface lazy-import machinery (standalone/moduleSurface) is reused unchanged.
  async function mountSurface(pageId: string, host: HTMLElement): Promise<PageController<unknown, unknown> | null> {
    const m = registry.get(pageId);
    if (!m) return null;
    return (await m.activate(host)) as PageController<unknown, unknown>;
  }

  // mount builds a tab's runtime once: a host pane in the outlet, a per-tab status bar, and a tile
  // that renders the tab's split-pane tree (a single leaf for an un-split tab). It attaches and shows
  // the tab synchronously BEFORE any surface activates, so a surface that measures its own DOM at init
  // (the log viewer's segmented switches, charts, canvas) sees the real, visible dimensions - a
  // display:none host reports zero. Inactive tabs are never pre-mounted. A second call for the same
  // tab is a no-op.
  function mount(tab: TabState): void {
    if (mounts.has(tab.id)) return;
    const host = document.createElement("div"); // a pane container: #console-outlet-content > div[data-tab-id]
    host.dataset.tabId = tab.id;
    outlet.append(host);
    const seed: Pane = tab.layout ?? { kind: "leaf", id: tab.id, pageId: tab.pageId };
    const tile = createTileView({
      seed,
      surfaces: SURFACES,
      mountSurface,
      onLayoutChange: (tree) => ws.set(setLayout(ws.get(), tab.id, tree)),
    });
    host.append(tile.el);
    mounts.set(tab.id, { host, status: makeStatusBar(), tile });
    show(tab.id); // visible + status attached before the tile's surfaces finish activating
  }

  // show reveals one tab's tile and swaps its status bar into the footer (detaching the others), so
  // the bottom bar always reflects the active tab. It also tells each tile whether it is visible, so
  // a background streamer suppresses its shared-status writes instead of leaking into the active bar.
  function show(id: string | null): void {
    for (const [tid, mt] of mounts) {
      mt.host.hidden = tid !== id;
      mt.tile.setVisible(tid === id);
    }
    const active = id ? mounts.get(id) : null;
    // No active tab means the workspace is empty: reveal the launcher empty state and dock its default
    // status bar. With a tab active, hide the launcher and dock the active tab's per-tab status bar.
    launcher.hidden = active != null;
    // Empty state flag for CSS: with zero tabs there is nothing on the (mobile) tab row, so the phone
    // title bar collapses back from two rows to one (see console.css) instead of reserving an empty
    // second row on the launcher screen.
    document.documentElement.toggleAttribute("data-no-tabs", active == null);
    statusHost.replaceChildren(active ? active.status : launcherStatus);
    // Let a docked Reference panel re-read the now-active surface's help sections.
    document.dispatchEvent(new CustomEvent("console:activetab", { detail: { id } }));
  }

  function unmount(id: string): void {
    const mt = mounts.get(id);
    if (!mt) return;
    mt.tile.deactivate();
    mt.host.remove();
    mt.status.remove();
    mounts.delete(id);
  }

  // reveal shows a tab's pane, mounting it lazily if it is a restored tab not yet mounted.
  function reveal(id: string): void {
    if (mounts.has(id)) { show(id); return; }
    const tab = ws.get().tabs.find((t) => t.id === id);
    if (tab) mount(tab);
  }

  // activeTile returns the tile of the active tab, or null - the pane commands target it.
  function activeTile(): TileView | null {
    const id = ws.get().activeId;
    return (id && mounts.get(id)?.tile) || null;
  }

  // The console owns the workspace mutations (the bar only reports intent, so keybindings drive the
  // same ops). activateTab records the active tab (the bar re-renders via its ws binding) then
  // reveals it; closeTabById removes a tab and reveals whatever the reducer chose next.
  function activateTab(id: string): void {
    ws.set(setActive(ws.get(), id));
    reveal(id);
  }
  function closeTabById(id: string): void {
    const next = closeTab(ws.get(), id);
    ws.set(next);
    unmount(id);
    if (next.activeId) reveal(next.activeId);
    else show(null);
  }
  // cycleTab moves to the next (+1) or previous (-1) tab, wrapping around the bar.
  function cycleTab(dir: 1 | -1): void {
    const cur = ws.get();
    if (cur.tabs.length < 2) return;
    const i = cur.tabs.findIndex((t) => t.id === cur.activeId);
    if (i < 0) return;
    activateTab(cur.tabs[(i + dir + cur.tabs.length) % cur.tabs.length].id);
  }

  const bar = createTabBar(ws, {
    onSelect: (id) => activateTab(id),
    onClose: (id) => closeTabById(id),
    // Split THAT tab's tile (its currently focused pane), not necessarily the active one - the context
    // menu is per-tab, so a right-click on a background tab splits it in place without switching to it.
    onSplit: (id, dir) => mounts.get(id)?.tile.split(dir),
    // Move a tab out into its own OS window: open the app window, then drop the tab. The window boots
    // the surface fresh (app mode mounts one surface and skips the workspace), so a tiled tab's other
    // panes do not travel with it - the same thing closing the tab would have discarded.
    onMoveToWindow: (id) => {
      const t = ws.get().tabs.find((x) => x.id === id);
      if (!t) return;
      openSurfaceWindow(t.pageId);
      closeTabById(id);
    },
  });
  tabBarHost.append(bar.el);

  // Wire the title-bar settings gear to OPEN the Settings surface as a tab (single-instance: open()
  // focuses it if it is already open). The old gear popover was retired; its controls live on the
  // surface now. No-op if the page did not supply the #settings-btn markup.
  const settingsBtn = document.getElementById("settings-btn");
  if (settingsBtn) settingsBtn.addEventListener("click", () => open("settings"));

  // Wire the title-bar Applications menu (links back to the docs site + playground). No-ops without
  // the #console-appmenu markup.
  initAppMenu();

  // Wire the title-bar Reference button + its slide-out panel. No-ops without the #console-refdrawer
  // markup. It reads the active surface's [data-ref-section] help blocks (refreshed on tab change).
  initRefDrawer();

  // Tab keybindings: register the commands and install ONE keydown listener over the merged keymap.
  // The listener skips while typing in a field (see commands.ts), so it never eats filter input.
  // Opening a surface is a command per surface (group "Open"): the launcher's cards cover the empty
  // state, and once a tab is open the command bar is how another surface is launched. Each opens
  // (or focuses, if already open) that single-instance surface as a tab.
  for (const s of SURFACES) {
    registerCommand({ id: "console.open." + s.pageId, label: "Open " + s.label, group: "Open", run: () => open(s.pageId) });
  }
  // mod+w closes the smallest thing: the focused PANE, falling through to the whole tab only when
  // that was the tab's last pane (closeFocused returns true) or the tab is un-tiled (no tile).
  registerCommand({ id: "console.tab.close", label: "Close pane or tab", group: "Tabs", run: () => {
    const t = activeTile();
    if (t && !t.closeFocused()) return;
    const a = ws.get().activeId; if (a) closeTabById(a);
  } });
  registerCommand({ id: "console.tab.next", label: "Next tab", group: "Tabs", run: () => cycleTab(1) });
  registerCommand({ id: "console.tab.prev", label: "Previous tab", group: "Tabs", run: () => cycleTab(-1) });
  // Tiling: split the focused pane (in the persisted default direction, or a forced axis), move focus
  // between panes, move a pane's SURFACE into a neighbor's slot, and jump back across the nearest
  // divider to the pane the current one was split from. Each targets the active tab's tile (tileView.ts
  // owns the tree ops). splitHorizontal/splitVertical double as "set the default": picking one
  // explicitly re-asserts it as splitMode, so the tray icon and the bare mod+\\ chord both follow the
  // last explicit choice, whichever surface (popup, command bar, tab context menu) made it.
  registerCommand({ id: "console.pane.split", label: "Split pane", group: "Panes", run: () => activeTile()?.split(splitMode.get()) });
  registerCommand({ id: "console.pane.splitHorizontal", label: "Split horizontal", group: "Panes", run: () => {
    splitMode.set("row");
    activeTile()?.split("row");
    refreshPanesTray();
  } });
  registerCommand({ id: "console.pane.splitVertical", label: "Split vertical", group: "Panes", run: () => {
    splitMode.set("col");
    activeTile()?.split("col");
    refreshPanesTray();
  } });
  registerCommand({ id: "console.pane.toggleSplitMode", label: "Toggle split mode", group: "Panes", run: () => {
    splitMode.set(splitMode.get() === "row" ? "col" : "row");
    refreshPanesTray();
  } });
  registerCommand({ id: "console.pane.focusLeft", label: "Focus pane left", group: "Panes", run: () => activeTile()?.focus("left") });
  registerCommand({ id: "console.pane.focusDown", label: "Focus pane down", group: "Panes", run: () => activeTile()?.focus("down") });
  registerCommand({ id: "console.pane.focusUp", label: "Focus pane up", group: "Panes", run: () => activeTile()?.focus("up") });
  registerCommand({ id: "console.pane.focusRight", label: "Focus pane right", group: "Panes", run: () => activeTile()?.focus("right") });
  registerCommand({ id: "console.pane.moveLeft", label: "Move pane left", group: "Panes", run: () => activeTile()?.move("left") });
  registerCommand({ id: "console.pane.moveDown", label: "Move pane down", group: "Panes", run: () => activeTile()?.move("down") });
  registerCommand({ id: "console.pane.moveUp", label: "Move pane up", group: "Panes", run: () => activeTile()?.move("up") });
  registerCommand({ id: "console.pane.moveRight", label: "Move pane right", group: "Panes", run: () => activeTile()?.move("right") });
  registerCommand({ id: "console.pane.focusParent", label: "Focus parent", group: "Panes", run: () => activeTile()?.focusParent() });

  // The command bar: a searchable overlay over every registered command. Register it AFTER the
  // other commands so it lists them; it reads the live command list + merged keymap on each open.
  const commandBar = createCommandBar({
    commands: listCommands,
    keymap: () => mergeKeymap(CONSOLE_KEYMAP, keymapCell.get()),
    mac: isMac(),
    onRun: (id) => dispatchCommand(id),
  });
  document.body.append(commandBar.el);
  registerCommand({ id: "console.actionBar.open", label: "Action bar", group: "General", run: () => commandBar.open() });

  // The title-bar trigger (index.html #console-commandbar-btn) opens the same action bar, so it is
  // discoverable without the chord. Stamp the effective chord into the tooltip so it also teaches it.
  const commandBarBtn = document.getElementById("console-commandbar-btn");
  if (commandBarBtn) {
    commandBarBtn.addEventListener("click", () => commandBar.open());
    const chord = formatChord(mergeKeymap(CONSOLE_KEYMAP, keymapCell.get())["console.actionBar.open"] ?? "", isMac());
    if (chord) commandBarBtn.title = "Action bar (" + chord + ")";
  }

  // The Panes tray: a small floating control panel opened from the status-bar Panes button, so tiling
  // is reachable without a keyboard - what the old title-bar Panes dropdown covered, plus move and
  // focus-parent, which a two-item menu never could. ONE shared element (appended to document.body,
  // reused by whichever tab's tray button opened it) rather than one per tab. Built AFTER the pane
  // commands above so every id it dispatches exists. chordFor mirrors the commandBarBtn tooltip's
  // chord lookup, stamped into each control's title so the tray also teaches its keyboard equivalent.
  const chordFor = (id: string): string => formatChord(mergeKeymap(CONSOLE_KEYMAP, keymapCell.get())[id] ?? "", isMac());

  const panesPopup = document.createElement("div");
  panesPopup.id = "console-panespopup";
  panesPopup.className = "console-shell-panespopup";
  panesPopup.setAttribute("role", "dialog");
  panesPopup.setAttribute("aria-label", "Panes");
  panesPopup.hidden = true;
  document.body.append(panesPopup);

  const panesBody = document.createElement("div");
  panesBody.className = "console-shell-panespopup__body";

  // surfaceLabel is a map cell's caption: the surface's launcher label, or "Empty" for an unfilled
  // leaf (a fresh split's launcher pane, before the operator picks a surface for it).
  function surfaceLabel(pageId: string): string {
    if (pageId === "") return "Empty";
    return SURFACES.find((s) => s.pageId === pageId)?.label ?? pageId;
  }

  // wirePaneCellDrag turns a map cell into both a tap target (focus) and a drag source/target (swap) -
  // one pointer stream serves both, matching how a real spatial map is operated: point to focus, drag
  // to move. setPointerCapture pins move/up events to the ORIGIN cell regardless of where the pointer
  // travels, so elementFromPoint (not event.target) is what finds the cell currently under the
  // pointer - the standard technique for a custom drag between sibling elements. A short pointer
  // path (under 4px) is a tap, not a drag, so a plain click still just focuses.
  function wirePaneCellDrag(cell: HTMLElement, id: string, onTap: () => void): void {
    let startX = 0, startY = 0, moved = false;
    let dropTarget: HTMLElement | null = null;
    const clearDrop = (): void => { dropTarget?.removeAttribute("data-drop"); dropTarget = null; };
    cell.addEventListener("pointerdown", (ev) => {
      startX = ev.clientX; startY = ev.clientY; moved = false;
      cell.setPointerCapture(ev.pointerId);
    });
    cell.addEventListener("pointermove", (ev) => {
      if (!cell.hasPointerCapture(ev.pointerId)) return;
      if (!moved && Math.hypot(ev.clientX - startX, ev.clientY - startY) > 4) moved = true;
      if (!moved) return;
      const under = document.elementFromPoint(ev.clientX, ev.clientY)?.closest<HTMLElement>("[data-pane-cell]") ?? null;
      const next = under && under !== cell ? under : null;
      if (next !== dropTarget) { clearDrop(); dropTarget = next; dropTarget?.setAttribute("data-drop", ""); }
    });
    cell.addEventListener("pointerup", (ev) => {
      cell.releasePointerCapture(ev.pointerId);
      const target = dropTarget;
      clearDrop();
      if (moved && target) {
        const dropId = target.dataset.paneCell;
        if (dropId) { activeTile()?.swap(id, dropId); renderPanesMap(); }
      } else if (!moved) {
        onTap();
      }
    });
    cell.addEventListener("pointercancel", clearDrop);
  }

  // buildSplitControls is the focused cell's H/V buttons - split THIS pane, no direction to decode
  // since the operator already pointed at it. Reuses the same commands (and the same
  // splitMode-asserting behavior) the old d-pad's Split row drove, so the tray icon still tracks
  // "the last explicit choice". pointerdown stopPropagation keeps a click on these from also
  // registering as the cell's own tap-to-focus/drag gesture.
  function buildSplitControls(): HTMLElement {
    const wrap = document.createElement("div");
    wrap.className = "console-shell-panesmap__splitctl";
    const mk = (dir: Split["dir"], label: string, glyph: string): HTMLButtonElement => {
      const b = document.createElement("button");
      b.type = "button";
      b.className = "console-shell-panesmap__splitbtn";
      b.dataset.split = dir;
      b.textContent = glyph;
      b.setAttribute("aria-label", label);
      const commandId = dir === "row" ? "console.pane.splitHorizontal" : "console.pane.splitVertical";
      const chord = chordFor(commandId);
      b.title = chord ? label + " (" + chord + ")" : label;
      b.addEventListener("pointerdown", (ev) => ev.stopPropagation());
      b.addEventListener("click", (ev) => {
        ev.stopPropagation();
        splitMode.set(dir);
        activeTile()?.split(dir);
        refreshPanesTray();
        renderPanesMap();
      });
      return b;
    };
    wrap.append(mk("row", "Split horizontal", "H"), mk("col", "Split vertical", "V"));
    return wrap;
  }

  // buildPaneMapCell renders one leaf as a tappable rectangle: its surface label, an accent ring when
  // it is the focused pane (matching tileView's own data-focus convention), and - only on the focused
  // cell - the split controls (so the first split works even on a single, un-tiled pane).
  function buildPaneMapCell(leaf: Leaf, focusId: string): HTMLElement {
    const cell = document.createElement("div");
    cell.className = "console-shell-panesmap__cell";
    cell.dataset.paneCell = leaf.id;
    cell.setAttribute("role", "button");
    cell.tabIndex = 0;
    const label = surfaceLabel(leaf.pageId);
    cell.setAttribute("aria-label", "Focus " + label + " pane");
    if (leaf.id === focusId) cell.dataset.focus = "";
    const text = document.createElement("span");
    text.className = "console-shell-panesmap__celllabel";
    text.textContent = label;
    cell.append(text);
    const focusThis = (): void => { activeTile()?.focusLeaf(leaf.id); renderPanesMap(); };
    cell.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); focusThis(); } });
    wirePaneCellDrag(cell, leaf.id, focusThis);
    if (leaf.id === focusId) cell.append(buildSplitControls());
    return cell;
  }

  // buildPaneMapNode walks the tree into nested flex boxes mirroring tileView's own row/col split
  // semantics (row = side by side, col = stacked), with each side's flex-grow set from the split's
  // ratio so the miniature tracks actual pane proportions, not just its shape.
  function buildPaneMapNode(pane: Pane, focusId: string): HTMLElement {
    if (pane.kind === "leaf") return buildPaneMapCell(pane, focusId);
    const split = document.createElement("div");
    split.className = "console-shell-panesmap__split";
    split.dataset.dir = pane.dir;
    const a = buildPaneMapNode(pane.a, focusId);
    const b = buildPaneMapNode(pane.b, focusId);
    a.style.flexGrow = String(pane.ratio);
    b.style.flexGrow = String(1 - pane.ratio);
    split.append(a, b);
    return split;
  }

  const panesMap = document.createElement("div");
  panesMap.className = "console-shell-panesmap";

  // chordModifiers renders just the MODIFIER prefix of a directional command's chord (drops the final
  // hjkl letter), so the hint legend below can show one generic "<mods> hjkl" line for the four
  // focus/move commands instead of four near-identical rows.
  function chordModifiers(commandId: string): string {
    const chord = mergeKeymap(CONSOLE_KEYMAP, keymapCell.get())[commandId] ?? "";
    if (!chord) return "";
    const mods = chord.split("+").slice(0, -1).join("+");
    return mods ? formatChord(mods, isMac()) : "";
  }

  // appendHintChord renders a formatted chord ("Cmd+\\") as its own kbd chips, reusing the cheat
  // sheet's keycap styling (.console-cheatsheet-kbd) - the same "each token reads as a physical key"
  // treatment, so the map's hint line looks like it belongs to the same product as the cheat sheet.
  function appendHintChord(target: HTMLElement, chord: string): void {
    chord.split("+").forEach((tok, i) => {
      if (i > 0) target.append(document.createTextNode("+"));
      const kbd = document.createElement("kbd");
      kbd.className = "console-cheatsheet-kbd";
      kbd.textContent = tok;
      target.append(kbd);
    });
  }

  // buildPanesHint folds the split/focus/move chords into one teaching line below the map - the old
  // d-pad taught its chords per-button (each control's tooltip); the map has no buttons for focus/move
  // (that is now pointer + drag), so this line is where that teaching lives instead.
  function buildPanesHint(): HTMLElement {
    const hint = document.createElement("div");
    hint.className = "console-shell-panesmap__hint";
    const groups: { label: string; chord: string }[] = [];
    const splitChord = chordFor("console.pane.split");
    if (splitChord) groups.push({ label: "Split", chord: splitChord });
    const focusMods = chordModifiers("console.pane.focusLeft");
    if (focusMods) groups.push({ label: "Focus", chord: focusMods + "+hjkl" });
    const moveMods = chordModifiers("console.pane.moveLeft");
    if (moveMods) groups.push({ label: "Move", chord: moveMods + "+hjkl" });
    groups.forEach((g, i) => {
      if (i > 0) hint.append(document.createTextNode("  "));
      const span = document.createElement("span");
      span.className = "console-shell-panesmap__hintgroup";
      span.append(document.createTextNode(g.label + " "));
      appendHintChord(span, g.chord);
      hint.append(span);
    });
    return hint;
  }
  const panesHintHost = document.createElement("div");
  panesHintHost.className = "console-shell-panesmap__hinthost";

  const closePaneBtn = document.createElement("button");
  closePaneBtn.type = "button";
  closePaneBtn.className = "pf-v6-c-button pf-m-secondary console-shell-panespopup__closebtn";
  closePaneBtn.textContent = "Close pane";
  const closeChord = chordFor("console.tab.close");
  closePaneBtn.title = closeChord ? "Close pane (" + closeChord + ")" : "Close pane";
  // Deliberately does not close the popup - Close, like the map's tap/drag/split gestures, is meant
  // for repeated use (closing several panes in one popup session) without reopening the tray each time.
  closePaneBtn.addEventListener("click", () => { dispatchCommand("console.tab.close"); renderPanesMap(); });

  panesBody.append(panesMap, panesHintHost, closePaneBtn);
  panesPopup.append(panesBody);

  // renderPanesMap (re)paints the live spatial map from the active tab's tile - rebuilt on open and
  // after every map action (tap-focus, split, drag-swap, close) so the highlighted cell and layout
  // never go stale. A null snapshot (no active tab) leaves the map empty; unreachable in practice since
  // the tray button only ever appears on a tab's own status bar.
  function renderPanesMap(): void {
    panesMap.replaceChildren();
    const snap = activeTile()?.snapshot();
    if (snap) panesMap.append(buildPaneMapNode(snap.tree, snap.focusId));
    panesHintHost.replaceChildren(buildPanesHint());
  }

  // refreshPanesTray repaints every MOUNTED tab's tray icon (not just the docked one - a hidden tab's
  // status bar is still a live element, just detached, so its icon would go stale until that tab is
  // shown again) whenever the persisted split mode changes - from the keyboard toggle, an explicit
  // split pick (the map's H/V buttons), or on open (to catch a cross-tab storage sync).
  function refreshPanesTray(): void {
    const mode = splitMode.get();
    for (const mt of mounts.values()) {
      const btn = mt.status.querySelector<HTMLElement>("[data-panes-toggle]");
      if (btn) setPanesIcon(btn, mode);
    }
  }
  refreshPanesTray(); // sync every tray icon with whatever mode was persisted

  // placePanesPopup opens the popup UPWARD from its anchor (the tray button sits on the bottom status
  // bar, so placing it below would run the popup off the bottom of the screen) and clamps it into the
  // viewport horizontally - the same clamped-fixed-position idiom as help-popover.ts, flipped vertically
  // for a bottom-docked trigger. Measured after unhiding so offsetWidth/Height are real.
  let panesAnchor: HTMLElement | null = null;
  function placePanesPopup(anchor: HTMLElement): void {
    const r = anchor.getBoundingClientRect();
    const margin = 8;
    const pw = panesPopup.offsetWidth;
    const ph = panesPopup.offsetHeight;
    let left = r.right - pw;
    if (left < margin) left = margin;
    if (left + pw > window.innerWidth - margin) left = window.innerWidth - margin - pw;
    let top = r.top - ph - 6;
    if (top < margin) top = margin;
    panesPopup.style.left = left + "px";
    panesPopup.style.top = top + "px";
  }
  function openPanesPopup(anchor: HTMLElement): void {
    refreshPanesTray(); // the tray icon must reflect the CURRENT mode the instant it becomes visible
    renderPanesMap(); // rebuild the live map from the active tab's tree fresh on every open
    panesAnchor = anchor;
    panesPopup.hidden = false;
    anchor.setAttribute("aria-expanded", "true");
    placePanesPopup(anchor);
  }
  function closePanesPopup(restoreFocus = false): void {
    if (panesPopup.hidden) return;
    panesPopup.hidden = true;
    panesAnchor?.setAttribute("aria-expanded", "false");
    if (restoreFocus) panesAnchor?.focus();
    panesAnchor = null;
  }
  function togglePanesPopup(anchor: HTMLElement): void {
    if (!panesPopup.hidden && panesAnchor === anchor) { closePanesPopup(); return; }
    closePanesPopup(); // a different tab's tray button while open: close-then-reopen re-anchors it
    openPanesPopup(anchor);
  }
  // Outside click closes it - checked against BOTH the popup and its current anchor (app-menu.ts's
  // idiom), so the same click that opened the popup (which reaches this document listener too, after
  // the statusHost delegation above already toggled it open) does not immediately close it again.
  document.addEventListener("click", (e) => {
    if (panesPopup.hidden) return;
    const t = e.target as Node;
    if (panesPopup.contains(t) || panesAnchor?.contains(t)) return;
    closePanesPopup();
  });
  document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key === "Escape" && !panesPopup.hidden) closePanesPopup(true);
  });

  // The keybinding editor is an integrated modal overlay (a sibling of the command bar), not a tab. It
  // edits the console's own commands (those with a CONSOLE_KEYMAP default) against the shared keymap
  // cell. Built here AFTER the command bar command is registered so it appears among the editable rows.
  // The console's own editable commands (those with a CONSOLE_KEYMAP default). Shared by the modal
  // overlay and the Settings surface's embedded editor - both drive the one shared keymap cell, so
  // the two never fork. Snapshotted here, after every CONSOLE_KEYMAP command is registered.
  const editableCommands = listCommands().filter((c) => Object.prototype.hasOwnProperty.call(CONSOLE_KEYMAP, c.id));
  const keybindings = createKeybindingsOverlay({ commands: editableCommands, defaults: CONSOLE_KEYMAP, keymap: keymapCell });
  document.body.append(keybindings.el);
  registerCommand({ id: "console.settings.keybindings", label: "Edit keybindings", group: "General", run: () => keybindings.open() });

  // A read-only, hold-to-reveal cheat sheet (hold "?"). It teaches the effective bindings and is
  // deliberately separate from the editor above - reading the same live command list + merged keymap.
  const cheatsheet = createCheatsheet({
    commands: listCommands,
    keymap: () => mergeKeymap(CONSOLE_KEYMAP, keymapCell.get()),
    mac: isMac(),
  });
  document.body.append(cheatsheet.el);
  // Make the cheat sheet a first-class command (discoverable in the command bar, bindable) and drive it from the
  // status-bar button. One delegated click on the footer covers every tab's button (present and future),
  // since makeStatusBar rebuilds a button per tab but they all toggle the single shared cheat sheet.
  registerCommand({ id: "console.cheatsheet.toggle", label: "Keyboard shortcuts", group: "General", run: () => cheatsheet.toggle() });
  statusHost.addEventListener("click", (e) => {
    const t = e.target as HTMLElement;
    if (t.closest("[data-cheatsheet-toggle]")) cheatsheet.toggle();
    // Same delegation idiom for the Panes tray button: makeStatusBar rebuilds one per tab, this one
    // listener drives the single shared popup regardless of which tab's copy was clicked.
    const panesBtn = t.closest<HTMLElement>("[data-panes-toggle]");
    if (panesBtn) togglePanesPopup(panesBtn);
    // #console-conn is rebuilt per tab (makeStatusBar) plus once more for the launcher, so this is one
    // delegated listener over the footer rather than a per-instance handler - it covers every incarnation,
    // present and future, the same way the cheat-sheet toggle above does.
    if (t.closest("#console-conn")) openDaemonSettings();
  });
  // Keyboard activation for the same control (role="button" + tabindex="0" on #console-conn makes it
  // focusable, but a <span> has no native Enter/Space activation the way a <button> would).
  statusHost.addEventListener("keydown", (e) => {
    if (e.key !== "Enter" && e.key !== " ") return;
    const t = e.target as HTMLElement;
    if (!t.closest("#console-conn")) return;
    e.preventDefault(); // Space must not also scroll the page
    openDaemonSettings();
  });

  // Readiness polling: enriches whichever #console-conn is currently docked with the daemon's /readyz
  // component report on a fixed interval, independent of tab switches. This is the composition root -
  // there is no console-level teardown to hook into (startConsole runs once for the page's lifetime,
  // like installKeybindings above), so the interval simply runs for as long as the page does.
  const READINESS_POLL_MS = 15000;
  function pollReadiness(): void {
    const params = parseHash();
    const liveHost = params.live ? validateLiveHost(params.live) : null;
    const defaultHost = getDefaultHost();
    const host = liveHost ?? (defaultHost ? validateLiveHost(defaultHost) : null);
    if (!host) return; // no resolvable daemon address to probe; leave the SSE-derived state alone
    const startedAt = Date.now();
    fetchReadiness(host).then((report) => {
      const conn = document.getElementById("console-conn");
      if (!conn) return; // momentarily absent between tab swaps
      const ageSec = Math.max(0, Math.round((Date.now() - startedAt) / 1000));
      // The tooltip is always safe to enrich - no surface writes conn.title, so this never contends.
      conn.title = formatReadinessTitle(report, ageSec);
      // But the dot (data-health) and the text (textContent/data-state) belong to whichever surface owns
      // the docked bar (module header's SURFACE contract) - the dashboard drives data-health from its own
      // cache/pool signal, and stealing it here would make the two fight and flicker. So the poller only
      // owns the dot + text at ZERO tabs, where the launcher's default bar has no surface behind it.
      if (ws.get().activeId == null) {
        conn.dataset.health = report
          ? readinessHealth(report)
          // Old daemon or genuinely unreachable - the browser cannot tell those apart, so do not assert a
          // health we did not measure; a plain fail reads honestly on the empty launcher.
          : "fail";
        conn.textContent = report ? (report.ready ? "daemon ready" : "daemon not ready") : "not connected";
        conn.dataset.state = report?.ready ? "connected" : "disconnected";
      }
    });
  }
  pollReadiness();
  setInterval(pollReadiness, READINESS_POLL_MS);

  installKeybindings(() => mergeKeymap(CONSOLE_KEYMAP, keymapCell.get()));

  // open launches a surface as a tab. Every surface (logs/graph/dashboard/activity) is single-instance
  // - it keeps module-level state, so a second instance would fight the first; if one is already open
  // anywhere - a tab's primary surface OR a pane inside a tiled tab - focus that tab instead of opening
  // a duplicate.
  function open(pageId: string): void {
    const m = registry.get(pageId);
    if (!m) return;
    const hostTab = ws.get().tabs.find((t) => tabHostsSurface(t, pageId));
    if (hostTab) { activateTab(hostTab.id); return; }
    const tab: TabState = { id: pageId + "-" + Date.now().toString(36), pageId, title: m.title };
    ws.set(openTab(ws.get(), tab));
    mount(tab);
  }

  // tabHostsSurface reports whether a tab already shows a surface, checking its tiled panes when it
  // has a layout and its primary pageId otherwise - so a single-instance surface is never opened twice.
  function tabHostsSurface(t: TabState, pageId: string): boolean {
    const ids = t.layout ? leaves(t.layout).map((l) => l.pageId) : [t.pageId];
    return ids.includes(pageId);
  }

  register(standaloneSurface({ id: "logs", title: "Log Viewer", dir: "logs", bundle: "log-viewer.js", css: "logs.css" }));
  register(standaloneSurface({ id: "dashboard", title: "Dashboard", dir: "dashboard", bundle: "dashboard.js", css: "dashboard.css" }));
  register(standaloneSurface({ id: "graph", title: "Graph Explorer", dir: "graph", bundle: "explorer.js", css: "graph.css" }));
  register(moduleSurface({ id: "activity", title: "Activity Trail", bundle: "activity/activity.js", css: "logs/logs.css" }));
  // Actions is registered from the shell bundle (not a lazy surface bundle) - it is a thin, static
  // catalogue over the console's own live command list + keymap, the same deps the keyboard cheat
  // sheet above reads, so a separate bundle would get nothing but import overhead.
  register(createActionsSurface({
    commands: listCommands,
    keymap: () => mergeKeymap(CONSOLE_KEYMAP, keymapCell.get()),
    mac: isMac(),
    run: (id) => dispatchCommand(id),
    editableIds: new Set(editableCommands.map((c) => c.id)),
    onEditKeybindings: openKeybindings,
  }));
  // Settings is registered from the shell bundle (not a lazy surface bundle) so its Keybindings
  // editor drives the SAME live keymap cell installKeybindings reads - a separate bundle would get its
  // own non-syncing persisted("keymap"). The shell injects the editable command list, defaults, and cell.
  register(settingsSurface({ keybindings: { commands: editableCommands, defaults: CONSOLE_KEYMAP, keymap: keymapCell } }));

  // App mode: a dedicated single-surface window, opened by the app drawer as index.html?app=<id>. It
  // shows ONE surface with the tab bar hidden (CSS keys on the [data-appmode] root) so an installed
  // PWA popup reads as a native app window. It mounts the surface DIRECTLY, bypassing the persisted
  // workspace, so a dedicated window never disturbs the main console's saved tabs. Unknown/absent param
  // falls through to the normal restore below.
  const launchApp = new URLSearchParams(location.search).get("app");
  const appSurface = launchApp ? SURFACES.find((s) => s.pageId === launchApp) : undefined;
  if (appSurface && registry.has(appSurface.pageId)) {
    document.documentElement.dataset.appmode = appSurface.pageId;
    document.title = appSurface.label + " - magus";
    mount({ id: "app-" + appSurface.pageId, pageId: appSurface.pageId, title: appSurface.label });
    return;
  }

  // Restore the persisted workspace: the tab bar already renders every saved tab (it binds to ws);
  // mount ONLY the active one so restore is cheap and its surface activates visible. The rest mount
  // lazily on first selection. Show the launcher empty state if the workspace is empty.
  const saved = ws.get();
  if (saved.tabs.length === 0) {
    show(null);
  } else {
    const activeId = saved.activeId ?? saved.tabs[0]?.id ?? null;
    const tab = saved.tabs.find((t) => t.id === activeId) ?? saved.tabs[0];
    if (tab) mount(tab);
  }
}

// Entry: wire the console page's DOM. Guarded so the module no-ops when the scaffold is absent. The
// footer (#console-statusbar) is an empty slot the console fills with the active tab's status bar.
const tabBarHost = document.getElementById("console-tabs");
// The outlet is the PF Drawer's __content (panes mount here); the Reference panel is the
// Drawer's __panel sibling. Falls back to #console-outlet if the drawer markup is absent.
const outlet = document.getElementById("console-outlet-content") ?? document.getElementById("console-outlet");
const statusHost = document.getElementById("console-statusbar");
if (tabBarHost && outlet && statusHost) startConsole(tabBarHost, outlet, statusHost);
