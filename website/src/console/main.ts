// main.ts - the console composition root and SPA-island entry. The page supplies three slots - the
// tab strip host (#console-tabs), the content outlet (#console-outlet), and the status-bar footer
// (#console-statusbar) - and this wires them: it mounts one surface per open tab (each kept in the
// DOM and hidden when inactive, so switching is instant and closing tears down), and swaps the active
// tab's status bar into the footer so the bottom bar is PER-TAB. The active set is the persisted
// Workspace (tabs.ts), so the console reopens exactly as you left it. Surfaces are PageModules
// (page.ts); a heavy one activates lazily (its bundle a dynamic import) so a tab stays cheap until
// opened. All four core lenses are real surfaces: home launcher + logs/graph/dashboard/activity.

import { openTab, closeTab, setActive, workspaceStore, type TabState } from "./tabs";
import { createTabStrip } from "./tabStrip";
import { homePage, type Launchable } from "./home";
import { standaloneSurface, moduleSurface } from "./standalone";
import { registerCommand, installKeybindings, mergeKeymap, type Keymap } from "./commands";
import { initConsoleSettings } from "../ui/console-settings";
import { persisted } from "../lib/persist";
import type { PageController, PageModule } from "./page";

// The console's default tab keybindings. Flat commandId -> chord, layered over the user's persisted
// "keymap" overrides (the same cell the surfaces read). mod = Cmd on macOS, Ctrl elsewhere. Cmd+Opt
// arrows match a browser/editor's next/prev-tab feel; new/close are the conventional mod+t / mod+w
// (they land on the console's own tabs when it runs as an installed PWA window).
const CONSOLE_KEYMAP: Keymap = {
  "console.tab.new": "mod+t",
  "console.tab.close": "mod+w",
  "console.tab.next": "mod+alt+ArrowRight",
  "console.tab.prev": "mod+alt+ArrowLeft",
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


interface Mounted { host: HTMLElement; status: HTMLElement; controller: PageController<any, any> | null; }

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
  const mounts = new Map<string, Mounted>(); // tabId -> its mounted host + controller

  // mount activates a surface into its own host once; a second call for the same tab is a no-op
  // (the surface stays mounted and hidden while another tab is active). mount is only ever called
  // for a tab we are switching to, so it shows the pane BEFORE activating: a surface that measures
  // its own DOM at init (the log viewer's segmented switches, and later charts/canvas) needs real
  // dimensions, and a display:none host reports zero. Inactive tabs are never pre-mounted - they
  // stay cheap until first selected.
  async function mount(tab: TabState): Promise<void> {
    if (mounts.has(tab.id)) return;
    const m = registry.get(tab.pageId);
    if (!m) return;
    const host = document.createElement("div"); // a pane: #console-outlet > div[data-tab-id], no class
    host.dataset.tabId = tab.id;
    outlet.append(host);
    const entry: Mounted = { host, status: makeStatusBar(), controller: null };
    mounts.set(tab.id, entry);
    show(tab.id); // visible + status attached before activate, so init-time measurement (and the log
    // viewer's zoom-control injection into .statusbar-right) sees the real, attached DOM.
    entry.controller = await m.activate(host);
  }

  // show reveals one tab's pane and swaps its status bar into the footer (detaching the others), so
  // the bottom bar always reflects the active tab. It also tells each surface whether it is visible,
  // so a background streamer suppresses its shared-status writes instead of leaking into the active
  // tab's bar.
  function show(id: string | null): void {
    for (const [tid, mt] of mounts) {
      mt.host.hidden = tid !== id;
      mt.controller?.setVisible?.(tid === id);
    }
    const active = id ? mounts.get(id) : null;
    if (active) statusHost.replaceChildren(active.status);
    else statusHost.replaceChildren();
  }

  function unmount(id: string): void {
    const mt = mounts.get(id);
    if (!mt) return;
    mt.controller?.deactivate();
    mt.host.remove();
    mt.status.remove();
    mounts.delete(id);
  }

  // reveal shows a tab's pane, mounting it lazily if it is a restored tab not yet mounted.
  function reveal(id: string): void {
    if (mounts.has(id)) { show(id); return; }
    const tab = ws.get().tabs.find((t) => t.id === id);
    if (tab) void mount(tab);
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
  registerCommand({ id: "console.tab.close", label: "Close tab", group: "Tabs", run: () => { const a = ws.get().activeId; if (a) closeTabById(a); } });
  registerCommand({ id: "console.tab.next", label: "Next tab", group: "Tabs", run: () => cycleTab(1) });
  registerCommand({ id: "console.tab.prev", label: "Previous tab", group: "Tabs", run: () => cycleTab(-1) });
  installKeybindings(() => mergeKeymap(CONSOLE_KEYMAP, keymapCell.get()));

  // open adds a fresh tab for a surface and mounts it. Every open is a new instance (its own id),
  // so the same surface can sit in two tabs.
  function open(pageId: string): void {
    const m = registry.get(pageId);
    if (!m) return;
    const tab: TabState = { id: pageId + "-" + Date.now().toString(36), pageId, title: m.title };
    ws.set(openTab(ws.get(), tab));
    void mount(tab);
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
    if (tab) void mount(tab);
  }
}

// Entry: wire the console page's DOM. Guarded so the module no-ops when the scaffold is absent. The
// footer (#console-statusbar) is an empty slot the console fills with the active tab's status bar.
const stripHost = document.getElementById("console-tabs");
const outlet = document.getElementById("console-outlet");
const statusHost = document.getElementById("console-statusbar");
if (stripHost && outlet && statusHost) startConsole(stripHost, outlet, statusHost);
