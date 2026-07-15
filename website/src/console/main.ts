// main.ts - the console composition root and SPA-island entry. The page supplies three slots - the
// tab strip host (#console-tabs), the content outlet (#console-outlet), and the status-bar footer
// (#console-statusbar) - and this wires them: it mounts one surface per open tab (each kept in the
// DOM and hidden when inactive, so switching is instant and closing tears down), and swaps the active
// tab's status bar into the footer so the bottom bar is PER-TAB. The active set is the persisted
// Workspace (tabs.ts), so the console reopens exactly as you left it. Surfaces are PageModules
// (page.ts); a heavy one activates lazily (its bundle a dynamic import) so a tab stays cheap until
// opened. All four core lenses are real surfaces: home launcher + logs/graph/dashboard/activity.

import { openTab, closeTab, setActive, setLayout, workspaceStore, type TabState } from "./tabs";
import { createTabStrip } from "./tabStrip";
import { homePage, type Launchable } from "./home";
import { standaloneSurface, moduleSurface } from "./standalone";
import { registerCommand, installKeybindings, mergeKeymap, type Keymap } from "./commands";
import { createTileView, type TileView } from "./tileView";
import { leaves, type Pane } from "./tiling";
import { initConsoleSettings } from "../ui/console-settings";
import { persisted } from "../lib/persist";
import type { PageController, PageModule } from "./page";

// The console's default tab keybindings. Flat commandId -> chord, layered over the user's persisted
// "keymap" overrides (the same cell the surfaces read). mod = Cmd on macOS, Ctrl elsewhere. Cmd+Opt
// arrows match a browser/editor's next/prev-tab feel; new/close are the conventional mod+t / mod+w
// (they land on the console's own tabs when it runs as an installed PWA window).
const CONSOLE_KEYMAP: Keymap = {
  "console.tab.new": "mod+t",
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
};
const keymapCell = persisted<Keymap>("keymap", {});

const registry = new Map<string, PageModule<any, any>>();
function register(m: PageModule<any, any>): void { registry.set(m.id, m); }

// The surfaces the home launcher offers (and the console can open).
const SURFACES: Launchable[] = [
  { pageId: "logs", label: "Log viewer", hint: "Read a run's captured output" },
  { pageId: "graph", label: "Graph explorer", hint: "Explore the knowledge graph" },
  { pageId: "dashboard", label: "Dashboard", hint: "Live daemon state" },
  { pageId: "activity", label: "Activity", hint: "The daemon's audit trail" },
];


interface Mounted { host: HTMLElement; status: HTMLElement; tile: TileView; }

// makeStatusBar builds one tab's status bar: the SAME element ids the surfaces write to
// (#console-conn, #console-demo, #console-observing, #console-count, #offline-badge) and the
// .statusbar-right slot the log viewer injects its zoom control into. It is a real element (not an
// innerHTML snapshot) so the surface's live handles + listeners survive tab switches. Only the ACTIVE
// tab's status bar is attached to the footer, so getElementById resolves to the active surface's
// status - the bottom bar is per-tab. (A surface streaming while its tab is hidden would still write
// through getElementById to the active bar; no surface does that today except a live dashboard/log,
// a known edge.)
function makeStatusBar(): HTMLElement {
  const bar = document.createElement("div");
  bar.className = "console-tab-status";
  const left = document.createElement("div");
  left.className = "statusbar-cluster";
  const conn = document.createElement("span");
  conn.id = "console-conn"; conn.className = "status-item conn"; conn.setAttribute("aria-live", "polite");
  conn.textContent = "not connected";
  const demo = document.createElement("span");
  demo.id = "console-demo"; demo.className = "status-tag console-demo-tag"; demo.hidden = true;
  demo.setAttribute("aria-live", "polite"); demo.textContent = "Demo data";
  left.append(conn, demo);
  const right = document.createElement("div");
  right.className = "statusbar-cluster statusbar-right";
  for (const [id, cls] of [["console-count", "status-item status-observing"], ["console-observing", "status-item status-observing"], ["offline-badge", "status-item status-tag status-offline"]] as const) {
    const s = document.createElement("span");
    s.id = id; s.className = cls; s.hidden = true; s.setAttribute("aria-live", "polite");
    if (id === "offline-badge") s.textContent = "offline";
    right.append(s);
  }
  bar.append(left, right);
  return bar;
}

export function startConsole(stripHost: HTMLElement, outlet: HTMLElement, statusHost: HTMLElement): void {
  const ws = workspaceStore();
  const mounts = new Map<string, Mounted>(); // tabId -> its mounted tile + status bar

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
    const host = document.createElement("div"); // a pane container: #console-outlet > div[data-tab-id]
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
    if (active) statusHost.replaceChildren(active.status);
    else statusHost.replaceChildren();
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

  // The console owns the workspace mutations (the strip only reports intent, so keybindings drive the
  // same ops). activateTab records the active tab (the strip re-renders via its ws binding) then
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
  // cycleTab moves to the next (+1) or previous (-1) tab, wrapping around the strip.
  function cycleTab(dir: 1 | -1): void {
    const cur = ws.get();
    if (cur.tabs.length < 2) return;
    const i = cur.tabs.findIndex((t) => t.id === cur.activeId);
    if (i < 0) return;
    activateTab(cur.tabs[(i + dir + cur.tabs.length) % cur.tabs.length].id);
  }

  const strip = createTabStrip(ws, {
    onSelect: (id) => activateTab(id),
    onClose: (id) => closeTabById(id),
    onNew: () => open("home"),
  });
  stripHost.append(strip.el);

  // Wire the title-bar settings gear (theme is wired separately by theme.js). No-ops if the page
  // did not supply the #settings-btn / #settings-panel markup.
  initConsoleSettings();

  // Tab keybindings: register the commands and install ONE keydown listener over the merged keymap.
  // The listener skips while typing in a field (see commands.ts), so it never eats filter input.
  registerCommand({ id: "console.tab.new", label: "New tab", group: "Tabs", run: () => open("home") });
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
  installKeybindings(() => mergeKeymap(CONSOLE_KEYMAP, keymapCell.get()));

  // open launches a surface as a tab. A surface (logs/graph/dashboard/activity) is single-instance -
  // it keeps module-level state, so a second instance would fight the first; if one is already open
  // anywhere - a tab's primary surface OR a pane inside a tiled tab - focus that tab instead. Home is
  // stateless, so "+" can always spawn a fresh launcher tab.
  function open(pageId: string): void {
    const m = registry.get(pageId);
    if (!m) return;
    if (pageId !== "home") {
      const hostTab = ws.get().tabs.find((t) => tabHostsSurface(t, pageId));
      if (hostTab) { activateTab(hostTab.id); return; }
    }
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

  register(homePage(SURFACES, open));
  register(standaloneSurface({ id: "logs", title: "Log viewer", dir: "logs", bundle: "log-viewer.js", css: "logs.css" }));
  register(standaloneSurface({ id: "dashboard", title: "Dashboard", dir: "dashboard", bundle: "dashboard.js", css: "dashboard.css" }));
  register(standaloneSurface({ id: "graph", title: "Graph explorer", dir: "graph", bundle: "explorer.js", css: "graph.css" }));
  register(moduleSurface({ id: "activity", title: "Activity", bundle: "activity/activity.js", css: "logs/logs.css" }));

  // Restore the persisted workspace: the tab strip already renders every saved tab (it binds to ws);
  // mount ONLY the active one so restore is cheap and its surface activates visible. The rest mount
  // lazily on first selection. Land on home if the workspace is empty.
  const saved = ws.get();
  if (saved.tabs.length === 0) {
    open("home");
  } else {
    const activeId = saved.activeId ?? saved.tabs[0]?.id ?? null;
    const tab = saved.tabs.find((t) => t.id === activeId) ?? saved.tabs[0];
    if (tab) mount(tab);
  }
}

// Entry: wire the console page's DOM. Guarded so the module no-ops when the scaffold is absent. The
// footer (#console-statusbar) is an empty slot the console fills with the active tab's status bar.
const stripHost = document.getElementById("console-tabs");
const outlet = document.getElementById("console-outlet");
const statusHost = document.getElementById("console-statusbar");
if (stripHost && outlet && statusHost) startConsole(stripHost, outlet, statusHost);
