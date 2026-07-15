// main.ts - the console composition root and SPA-island entry. It owns the ONE chrome (the
// app bar via the shared main.js, the tab strip, the content outlet) and mounts one surface per
// open tab - each kept in the DOM and hidden when inactive, so switching is instant and closing
// tears down. The active set is the persisted Workspace (tabs.ts), so the console reopens exactly
// as you left it. Surfaces are PageModules (page.ts); a heavy one activates lazily so a tab stays
// cheap until opened.
//
// This is the first console slice: the home launcher plus stub surfaces. The real log viewer / graph
// / dashboard PageModules replace the stubs slice by slice, as each app is refactored to mount into
// a host via activate() rather than boot against its own static document.

import { openTab, workspaceStore, type TabState } from "./tabs";
import { createTabStrip } from "./tabStrip";
import { homePage, type Launchable } from "./home";
import type { PageController, PageModule, SearchProvider } from "./page";

const registry = new Map<string, PageModule<any, any>>();
function register(m: PageModule<any, any>): void { registry.set(m.id, m); }

// The surfaces the home launcher offers (and the console can open).
const SURFACES: Launchable[] = [
  { pageId: "logs", label: "Log viewer", hint: "Read a run's captured output" },
  { pageId: "graph", label: "Graph explorer", hint: "Explore the knowledge graph" },
  { pageId: "dashboard", label: "Dashboard", hint: "Live daemon state" },
  { pageId: "activity", label: "Activity", hint: "The daemon's audit trail" },
];

const noSearch: SearchProvider<null> = { placeholder: "", parse: () => null, apply: () => ({ matches: 0 }) };

// stubPage is a placeholder surface for an app not yet refactored to mount in the console - it shows
// what will live there. The real PageModule replaces it one app at a time.
function stubPage(id: string, title: string): PageModule<null, null> {
  return {
    id,
    title,
    async activate(host: HTMLElement): Promise<PageController<null, null>> {
      host.classList.add("console-stub"); // add, don't clobber the console-pane class the outlet set
      const note = document.createElement("p");
      note.className = "console-stub-note";
      note.textContent = title + " mounts here once it is wired into the console.";
      host.append(note);
      return { search: noSearch, deactivate() { host.replaceChildren(); } };
    },
  };
}

interface Mounted { host: HTMLElement; controller: PageController<any, any> | null; }

export function startConsole(stripHost: HTMLElement, outlet: HTMLElement): void {
  const ws = workspaceStore();
  const mounts = new Map<string, Mounted>(); // tabId -> its mounted host + controller

  // mount activates a surface into its own host once; a second call for the same tab is a no-op
  // (the surface stays mounted and hidden while another tab is active).
  async function mount(tab: TabState): Promise<void> {
    if (mounts.has(tab.id)) return;
    const m = registry.get(tab.pageId);
    if (!m) return;
    const host = document.createElement("div");
    host.className = "console-pane";
    host.dataset.tabId = tab.id;
    host.hidden = true;
    outlet.append(host);
    const entry: Mounted = { host, controller: null };
    mounts.set(tab.id, entry);
    entry.controller = await m.activate(host);
  }

  function show(id: string | null): void {
    for (const [tid, mt] of mounts) mt.host.hidden = tid !== id;
  }

  function unmount(id: string): void {
    const mt = mounts.get(id);
    if (!mt) return;
    mt.controller?.deactivate();
    mt.host.remove();
    mounts.delete(id);
  }

  const strip = createTabStrip(ws, {
    onSelect: (id) => {
      // Already mounted -> show synchronously (instant switch). Otherwise mount, then show.
      if (mounts.has(id)) { show(id); return; }
      const tab = ws.get().tabs.find((t) => t.id === id);
      if (tab) void mount(tab).then(() => show(id));
    },
    onClose: (id) => unmount(id),
    onNew: () => open("home"),
  });
  stripHost.append(strip.el);

  // open adds a fresh tab for a surface and mounts it. Every open is a new instance (its own id),
  // so the same surface can sit in two tabs.
  function open(pageId: string): void {
    const m = registry.get(pageId);
    if (!m) return;
    const tab: TabState = { id: pageId + "-" + Date.now().toString(36), pageId, title: m.title };
    ws.set(openTab(ws.get(), tab));
    void mount(tab).then(() => show(tab.id));
  }

  register(homePage(SURFACES, open));
  register(stubPage("logs", "Log viewer"));
  register(stubPage("graph", "Graph explorer"));
  register(stubPage("dashboard", "Dashboard"));
  register(stubPage("activity", "Activity"));

  // Restore the persisted workspace - mount every tab, show the active one; land on home if empty.
  const saved = ws.get();
  void (async () => {
    for (const t of saved.tabs) await mount(t);
    if (saved.tabs.length === 0) open("home");
    else show(saved.activeId ?? saved.tabs[0]?.id ?? null);
  })();
}

// Entry: wire the console page's DOM. Guarded so the module no-ops when the scaffold is absent.
const stripHost = document.getElementById("console-tabs");
const outlet = document.getElementById("console-outlet");
if (stripHost && outlet) startConsole(stripHost, outlet);
