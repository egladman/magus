// surface.ts - the console's `logs` PageModule: it mounts the standalone Log Viewer as a console
// surface without duplicating its scaffold or pulling its bundle into the console frame. activate()
// lifts the viewer's <main> from the already-built /console/logs/ page (the SAME scaffold the
// standalone tool serves, so logs.html stays the one source of truth), ensures the viewer stylesheet
// is loaded, then dynamically imports the prebuilt log-viewer bundle by URL and boots it via its
// exported activate(). Loading both by URL - not a static import - keeps the surface lazy: the
// console bundle stays tiny and the protobuf/viewer code arrives only when a logs tab is first
// opened. This file imports nothing heavy (only the erased page types), so it is safe for the
// console composition root to import eagerly.

import type { PageController, PageModule, SearchProvider } from "../page";

// The console owns one shared search box, but the log viewer carries its own filter input in the
// scaffold, so the surface opts out of the shared box for now (wiring the two is a later refinement).
const noSearch: SearchProvider<null> = { placeholder: "", parse: () => null, apply: () => ({ matches: 0 }) };

// siblingUrl resolves an artifact under gen/console/logs/ relative to THIS module's URL at runtime,
// so the same code works wherever the console is served (a dev port, or the site's /magus/ base
// path). Computed (not a string literal) so esbuild leaves it a runtime load instead of trying to
// bundle the built artifact at compile time.
function siblingUrl(rel: string): string {
  return new URL(rel, import.meta.url).href;
}

// ensureViewerCss adds the log viewer's stylesheet once (idempotent by id): the surface markup is
// styled by logs.css, which the console page itself does not load.
function ensureViewerCss(): void {
  const id = "logs-surface-css";
  if (document.getElementById(id)) return;
  const link = document.createElement("link");
  link.id = id;
  link.rel = "stylesheet";
  link.href = siblingUrl("./logs/logs.css");
  document.head.append(link);
}

// liftScaffold fetches the built log viewer page and returns a copy of its <main> - the exact
// scaffold the standalone tool boots against, so the console never drifts from it. The console frame
// provides the outer chrome (app bar, status bar), so only <main> is lifted and the page's own shell
// is dropped.
async function liftScaffold(): Promise<HTMLElement> {
  const res = await fetch(siblingUrl("./logs/index.html"));
  const html = await res.text();
  const doc = new DOMParser().parseFromString(html, "text/html");
  const main = doc.querySelector("main");
  if (!main) throw new Error("log viewer scaffold not found in /console/logs/");
  return document.importNode(main, true);
}

// The shape the surface calls on the dynamically imported viewer bundle.
interface ViewerModule { activate(): void; }

export function logsPage(): PageModule<null, null> {
  return {
    id: "logs",
    title: "Log viewer",
    async activate(host: HTMLElement): Promise<PageController<null, null>> {
      ensureViewerCss();
      // Import the bundle BEFORE the scaffold exists so the viewer's standalone auto-boot (guarded
      // on #log-body) no-ops; we drive activate() ourselves once the scaffold is mounted. On reopen
      // the module is cached (no re-eval), so activate() simply re-binds to the fresh scaffold.
      const mod = (await import(siblingUrl("./logs/log-viewer.js"))) as ViewerModule;
      host.append(await liftScaffold());
      mod.activate();
      return {
        search: noSearch,
        // The viewer keeps module-singleton state and does not yet expose a live-stream teardown
        // handle (a known follow-up), so clearing the host detaches its DOM but a live SSE would keep
        // running until the page unloads. Acceptable for the static-log C1 slice.
        deactivate() { host.replaceChildren(); },
      };
    },
  };
}
