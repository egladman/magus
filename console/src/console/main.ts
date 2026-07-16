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
import { createCheatsheet } from "./cheatsheet";
import { createCommandsCheatsheet } from "./commandsCheatsheet";
import { createTileView, type TileView } from "./tileView";
import { leaves, type Pane } from "./tiling";
import { initConsoleSettings } from "../ui/console-settings";
import { initRefDrawer } from "../ui/ref-drawer";
import { initAppMenu } from "../ui/app-menu";
import { persisted } from "../lib/persist";
import { parseHash, wantsDemo, validateLiveHost, getLiveToken, authHeaders } from "../lib/daemon";
import type { PageController, PageModule } from "./page";

// The console's default tab keybindings. Flat commandId -> chord, layered over the user's persisted
// "keymap" overrides (the same cell the surfaces read). mod = Cmd on macOS, Ctrl elsewhere. Cmd+Opt
// arrows match a browser/editor's next/prev-tab feel; new/close are the conventional mod+t / mod+w
// (they land on the console's own tabs when it runs as an installed PWA window).
const CONSOLE_KEYMAP: Keymap = {
  "console.tab.close": "mod+w", // closes the focused PANE, or the tab when it is the last pane
  "console.tab.next": "mod+alt+ArrowRight",
  "console.tab.prev": "mod+alt+ArrowLeft",
  // Tiling: split the focused pane (auto axis / forced down), and vim-style directional pane focus.
  "console.pane.split": "mod+\\",
  "console.pane.splitDown": "mod+shift+\\",
  "console.pane.focusLeft": "alt+h",
  "console.pane.focusDown": "alt+j",
  "console.pane.focusUp": "alt+k",
  "console.pane.focusRight": "alt+l",
  // The command bar: one searchable list of every command (and its chord).
  "console.commandBar.open": "mod+k",
};
const keymapCell = persisted<Keymap>("keymap", {});

const registry = new Map<string, PageModule<any, any>>();
function register(m: PageModule<any, any>): void { registry.set(m.id, m); }

// The surfaces the home launcher offers (and the console can open).
const SURFACES: Launchable[] = [
  { pageId: "logs", label: "Log Viewer", hint: "Read a run's captured output" },
  { pageId: "graph", label: "Graph Explorer", hint: "Explore the knowledge graph" },
  { pageId: "dashboard", label: "Dashboard", hint: "What magus is doing right now" },
  { pageId: "activity", label: "Activity Trail", hint: "A history of recent magus actions" },
];


interface Mounted { host: HTMLElement; status: HTMLElement; tile: TileView; }

// pfLabel builds a PatternFly Label chip carrying the given id, color modifier, and text. The id
// sits on the OUTER .pf-v6-c-label span so a surface toggling `.hidden` hides the whole chip; the
// text lives in the nested __content/__text (a surface never rewrites these chips' text, only shows/
// hides them). Compact + outline reads as a quiet status pill, not a loud filled badge.
function pfLabel(id: string, colorMod: string, text: string): HTMLElement {
  const label = document.createElement("span");
  label.id = id;
  label.className = ("pf-v6-c-label pf-m-compact pf-m-outline " + colorMod).trim();
  label.hidden = true;
  label.setAttribute("aria-live", "polite");
  const content = document.createElement("span");
  content.className = "pf-v6-c-label__content";
  const txt = document.createElement("span");
  txt.className = "pf-v6-c-label__text";
  txt.textContent = text;
  content.append(txt);
  label.append(content);
  return label;
}

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

// commandsIcon returns the status-bar "all commands" glyph as an inline SVG: a command-prompt ">_"
// (a chevron over an underscore caret), distinct from the keyboard glyph beside it so the two footer
// toggles read apart. Built via createElementNS to match the console's icon convention (no innerHTML,
// themes on currentColor).
function commandsIcon(): SVGElement {
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
  const prompt = document.createElementNS(NS, "path");
  prompt.setAttribute("d", "M5 8l4 4-4 4M12 16h7");
  svg.append(prompt);
  return svg;
}

// makeStatusBar builds one tab's status bar: the SAME element ids the surfaces write to
// (#console-conn, #console-demo, #console-observing, #console-count) and the
// .console-shell-statusbar__right slot the log viewer injects its zoom control into. It is a real element (not an
// innerHTML snapshot) so the surface's live handles + listeners survive tab switches. Only the ACTIVE
// tab's status bar is attached to the footer, so getElementById resolves to the active surface's
// status - the bottom bar is per-tab. (A surface streaming while its tab is hidden would still write
// through getElementById to the active bar; no surface does that today except a live dashboard/log,
// a known edge.)
//
// PatternFly (W2 shell rebuild): the #console-demo chip is a PF Label; the text items (#console-conn
// with its liveness dot, #console-count, #console-observing) are plain
// spans the surfaces write via textContent + [data-state]/[data-health], styled ID-scoped in
// overrides.css (PF has no status-bar component). The wrapper + clusters are class-free (data hooks);
// only .console-shell-statusbar__right stays a class because the log viewer queries it to inject its zoom control.
function makeStatusBar(): HTMLElement {
  const bar = document.createElement("div");
  const left = document.createElement("div");
  left.dataset.cluster = "";
  const conn = document.createElement("span");
  conn.id = "console-conn"; conn.setAttribute("aria-live", "polite");
  conn.textContent = "not connected";
  left.append(conn, pfLabel("console-demo", "", "demo"));
  const right = document.createElement("div");
  right.dataset.cluster = ""; right.className = "console-shell-statusbar__right";
  for (const id of ["console-count", "console-observing"] as const) {
    const s = document.createElement("span");
    s.id = id; s.dataset.item = ""; s.hidden = true; s.setAttribute("aria-live", "polite");
    right.append(s);
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

  // All-commands toggle: a sibling quiet icon button, immediately next to the keyboard one, that flips
  // the commands cheat sheet (the full command catalogue, distinct from the chorded keyboard sheet).
  // data-commands-toggle is the hook; startConsole wires ONE delegated footer click so every tab's
  // button drives the single shared overlay. Reuses the shortcuts button's muted footer styling.
  const commands = document.createElement("button");
  commands.type = "button";
  commands.className = "pf-v6-c-button pf-m-plain console-shell-statusbar__shortcuts";
  commands.dataset.commandsToggle = "";
  commands.setAttribute("aria-label", "All commands");
  commands.title = "All commands";
  const commandsIconWrap = document.createElement("span");
  commandsIconWrap.className = "pf-v6-c-button__icon";
  commandsIconWrap.append(commandsIcon());
  commands.append(commandsIconWrap);
  right.append(commands);

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

export function startConsole(tabBarHost: HTMLElement, outlet: HTMLElement, statusHost: HTMLElement): void {
  loadBuildInfo(); // fetch the build fingerprint once; fills every status bar's version chip
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
  const launcherStatus = makeStatusBar();

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
  });
  tabBarHost.append(bar.el);

  // Wire the title-bar settings gear (theme is wired separately by theme.js). No-ops if the page
  // did not supply the #settings-btn / #settings-panel markup.
  initConsoleSettings();

  // Wire the title-bar Applications menu (links back to the docs site + playground). No-ops without
  // the #console-appmenu markup.
  initAppMenu();

  // Wire the title-bar Reference button + its slide-out panel. No-ops without the #console-refdrawer
  // markup. It reads the active surface's [data-legacy-ref] help blocks (refreshed on tab change).
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
  // Tiling: split the focused pane and move focus between panes. Each targets the active tab's tile.
  registerCommand({ id: "console.pane.split", label: "Split pane", group: "Panes", run: () => activeTile()?.split() });
  registerCommand({ id: "console.pane.splitDown", label: "Split pane down", group: "Panes", run: () => activeTile()?.split("col") });
  registerCommand({ id: "console.pane.focusLeft", label: "Focus pane left", group: "Panes", run: () => activeTile()?.focus("left") });
  registerCommand({ id: "console.pane.focusDown", label: "Focus pane down", group: "Panes", run: () => activeTile()?.focus("down") });
  registerCommand({ id: "console.pane.focusUp", label: "Focus pane up", group: "Panes", run: () => activeTile()?.focus("up") });
  registerCommand({ id: "console.pane.focusRight", label: "Focus pane right", group: "Panes", run: () => activeTile()?.focus("right") });

  // The command bar: a searchable overlay over every registered command. Register it AFTER the
  // other commands so it lists them; it reads the live command list + merged keymap on each open.
  const commandBar = createCommandBar({
    commands: listCommands,
    keymap: () => mergeKeymap(CONSOLE_KEYMAP, keymapCell.get()),
    mac: isMac(),
    onRun: (id) => dispatchCommand(id),
  });
  document.body.append(commandBar.el);
  registerCommand({ id: "console.commandBar.open", label: "Command bar", group: "General", run: () => commandBar.open() });

  // The title-bar trigger (index.html #console-commandbar-btn) opens the same command bar, so it is
  // discoverable without the chord. Stamp the effective chord into the tooltip so it also teaches it.
  const commandBarBtn = document.getElementById("console-commandbar-btn");
  if (commandBarBtn) {
    commandBarBtn.addEventListener("click", () => commandBar.open());
    const chord = formatChord(mergeKeymap(CONSOLE_KEYMAP, keymapCell.get())["console.commandBar.open"] ?? "", isMac());
    if (chord) commandBarBtn.title = "Command bar (" + chord + ")";
  }

  // The keybinding editor is an integrated modal overlay (a sibling of the command bar), not a tab. It
  // edits the console's own commands (those with a CONSOLE_KEYMAP default) against the shared keymap
  // cell. Built here AFTER the command bar command is registered so it appears among the editable rows.
  const keybindings = createKeybindingsOverlay({
    commands: listCommands().filter((c) => Object.prototype.hasOwnProperty.call(CONSOLE_KEYMAP, c.id)),
    defaults: CONSOLE_KEYMAP,
    keymap: keymapCell,
  });
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

  // A read-only catalogue of EVERY registered command (the chorded keyboard sheet above shows only the
  // bound ones), driven from the sibling status-bar button - same delegated-click pattern, reading the
  // same live command list + merged keymap.
  const commandsSheet = createCommandsCheatsheet({
    commands: listCommands,
    keymap: () => mergeKeymap(CONSOLE_KEYMAP, keymapCell.get()),
    mac: isMac(),
  });
  document.body.append(commandsSheet.el);
  registerCommand({ id: "console.commands.toggle", label: "All commands", group: "General", run: () => commandsSheet.toggle() });
  statusHost.addEventListener("click", (e) => {
    const t = e.target as HTMLElement;
    if (t.closest("[data-cheatsheet-toggle]")) cheatsheet.toggle();
    else if (t.closest("[data-commands-toggle]")) commandsSheet.toggle();
  });

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
