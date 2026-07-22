// standalone.ts - wrap a standalone console app (the log viewer, dashboard, graph explorer) as a
// console PageModule without duplicating its scaffold or pulling its bundle into the console frame.
// Each of these apps already ships as its own page + esm bundle under gen/console/<dir>/; this factory
// mounts one as a surface: it lifts the app's <main> from its built page (the app's own .html stays
// the ONE scaffold source, so the console never drifts from it), ensures the app's page-scoped
// stylesheet, dynamically imports the prebuilt bundle BY URL, and boots it via the app's exported
// activate(). Loading both by URL - not a static import - keeps the surface lazy: the console bundle
// stays tiny and an app's heavy code (protobuf, d3) arrives only when its tab is first opened. This
// file imports nothing heavy (only the erased page types), so the composition root can import it
// eagerly.
//
// Precondition: the wrapped app must be boot-DEFERRABLE - export activate() and guard its standalone
// auto-boot on a scaffold id, so importing the bundle before the scaffold exists is a no-op and the
// console drives activate() itself. The log viewer and dashboard satisfy this; the graph explorer
// does not yet.

import type { PageController, PageModule, SearchProvider } from "./page";

export interface StandaloneSurface {
  id: string; // registry id / pageId, e.g. "logs"
  title: string; // tab title, e.g. "Log Viewer"
  dir: string; // gen/console/<dir>/ holding index.html + the bundle + the css
  bundle: string; // bundle filename that exports activate(), e.g. "log-viewer.js"
  css: string; // page-scoped stylesheet filename, e.g. "logs.css"
}

// The console owns one shared search box, but these apps carry their own filter controls, so a
// wrapped surface opts out of the shared box for now (wiring the two is a later refinement).
const noSearch: SearchProvider<null> = {
  placeholder: "",
  parse: () => null,
  apply: () => ({ matches: 0 }),
};

// The shape the factory calls on a dynamically imported app bundle. A streaming surface (the
// dashboard) also exports setVisible so it can suppress its shared-status-bar writes while its tab is
// hidden. A surface that opens something with a lifetime (a live SSE stream, the graph's force
// simulation) exports deactivate() to tear it down when its tab/pane closes; a purely static surface
// omits it.
interface BootModule {
  activate(): void;
  setVisible?(visible: boolean): void;
  deactivate?(): void;
}

// A surface that has NO standalone page to lift - its bundle builds its own DOM into the host. Used
// for the Activity view (there is no /console/activity/ tool page). Paths are relative to gen/console/.
export interface ModuleSurface {
  id: string; // registry id / pageId, e.g. "activity"
  title: string; // tab title, e.g. "Activity Trail"
  bundle: string; // bundle path under gen/console/ whose activate(host) builds the DOM, e.g. "activity/activity.js"
  css: string; // page-scoped stylesheet path under gen/console/, e.g. "logs/logs.css" (the trail reuses it)
}

// The shape moduleSurface calls on a host-building bundle: activate(host) builds into host and returns
// an optional teardown run on close.
interface HostModule {
  activate(host: HTMLElement): (() => void) | void;
}

// moduleSurface wraps a page-less surface: the console dynamically imports its bundle by URL (kept
// lazy, like the others) and calls its exported activate(host). Symmetric with standaloneSurface but
// without the fetch-and-lift, since there is no built page - the bundle owns its scaffold.
export function moduleSurface(s: ModuleSurface): PageModule<null, null> {
  const url = (p: string): string => new URL("./" + p, import.meta.url).href;
  const cssId = "surface-css-" + s.id;
  return {
    id: s.id,
    title: s.title,
    async activate(host: HTMLElement): Promise<PageController<null, null>> {
      if (!document.getElementById(cssId)) {
        const link = document.createElement("link");
        link.id = cssId;
        link.rel = "stylesheet";
        link.href = url(s.css);
        document.head.append(link);
      }
      const mod = (await import(url(s.bundle))) as HostModule;
      const teardown = mod.activate(host);
      return {
        search: noSearch,
        deactivate() {
          if (typeof teardown === "function") teardown();
          host.replaceChildren();
        },
      };
    },
  };
}

export function standaloneSurface(s: StandaloneSurface): PageModule<null, null> {
  // artUrl resolves an artifact under gen/console/<dir>/ relative to THIS module's URL at runtime, so
  // the same code works wherever the console is served (a dev port, or the site's /magus/ base path).
  // Computed (not a string literal) so esbuild leaves it a runtime load instead of bundling the built
  // artifact at compile time.
  const artUrl = (file: string): string => new URL("./" + s.dir + "/" + file, import.meta.url).href;
  const cssId = "surface-css-" + s.id;

  return {
    id: s.id,
    title: s.title,
    async activate(host: HTMLElement): Promise<PageController<null, null>> {
      // The app's page-scoped stylesheet, added once (idempotent by id); the console page itself does
      // not load it.
      if (!document.getElementById(cssId)) {
        const link = document.createElement("link");
        link.id = cssId;
        link.rel = "stylesheet";
        link.href = artUrl(s.css);
        document.head.append(link);
      }
      // Import the bundle BEFORE the scaffold exists so the app's standalone auto-boot (guarded on a
      // scaffold id) no-ops; we drive activate() ourselves once the scaffold is mounted. On reopen the
      // module is cached (no re-eval), so activate() simply re-binds to the fresh scaffold.
      const mod = (await import(artUrl(s.bundle))) as BootModule;
      // Fetch the surface's co-located scaffold - a `<main>` fragment (gen/<dir>/scaffold.html) that
      // this console project owns, no longer a full standalone page (the decoupled console has none).
      // The console frame provides the outer chrome (title bar, status bar); the fragment is the
      // surface's own body, appended into the pane host.
      const res = await fetch(artUrl("scaffold.html"));
      const doc = new DOMParser().parseFromString(await res.text(), "text/html");
      const main = doc.querySelector("main");
      if (!main)
        throw new Error(s.id + " scaffold.html missing its <main> (built to gen/" + s.dir + "/)");
      host.append(document.importNode(main, true));
      mod.activate();
      return {
        search: noSearch,
        // A surface that writes the shared status bar (the dashboard) exports setVisible so it can go
        // quiet while backgrounded; static surfaces (logs/graph) do not, and this stays undefined.
        setVisible: mod.setVisible,
        // Tear down the surface's own lifetimes first (a live SSE stream, the graph's force simulation)
        // via its exported deactivate(), THEN detach its DOM - so closing a tab/pane leaves nothing
        // streaming or ticking in the background. A static surface exports no deactivate; the DOM detach
        // still happens.
        deactivate() {
          mod.deactivate?.();
          host.replaceChildren();
        },
      };
    },
  };
}
