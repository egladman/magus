// sw.js - the console app's own service worker. A conservative offline shell: it precaches the app
// entry + its bundles + styles so a cold reload works offline, and serves everything else same-origin
// cache-first-then-network. The console talks to the server over loopback at runtime (never cached).
// BUILD_ID names the cache, and the build REWRITES the line below with a digest of the bytes in
// PRECACHE (scripts/stamp-build.mjs, run by copy-static). It is derived, never edited: a new worker
// only installs when sw.js differs from the copy the browser holds, so a hand-maintained id that
// nobody remembers to bump leaves every client pinned to the shell it first cached. The value here
// is only what an unstamped source tree carries.
const BUILD_ID = "unstamped";
const CACHE = "magus-console-" + BUILD_ID;
const BASE = new URL("./", self.location).pathname;

const PRECACHE = [
  BASE,
  BASE + "index.html",
  BASE + "console.js",
  BASE + "console.css",
  // PatternFly Core is the console's only design system now. The Pico-era sheets (pico.min.css,
  // site.css, ui-panels.css, theme.css) were removed at the W4 cutover; BUILD_ID is bumped so
  // clients drop the old cache and refetch the PF-only shell.
  BASE + "patternfly.css",
  BASE + "tokens.css",
  BASE + "overrides.css",
  BASE + "theme.js",
  BASE + "logs/log-viewer.js",
  BASE + "logs/logs.css",
  BASE + "logs/scaffold.html",
  BASE + "graph/explorer.js",
  BASE + "graph/graph.css",
  BASE + "graph/scaffold.html",
  // Not the demo graph JSON: a server refuses to serve it (it is a workspace's data), and one
  // failed entry fails addAll, so the worker would never install on a server origin. The hosted
  // demo still caches it on first view through the network-first branch below.
  BASE + "dashboard/dashboard.js",
  BASE + "dashboard/dashboard.css",
  BASE + "dashboard/scaffold.html",
  BASE + "activity/activity.js",
];

self.addEventListener("install", (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(PRECACHE)).then(() => self.skipWaiting()));
});

self.addEventListener("activate", (e) => {
  e.waitUntil(
    caches.keys().then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k)))).then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (e) => {
  const req = e.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return; // never touch server/loopback or cross-origin
  // On the server's own origin the API is same-origin too, and cache-first would replay a stale
  // /readyz or buffer the endless /api/v1/events stream. Only the console's own files are the shell.
  // sw.js stays uncached so the page can read the build the server serves (lib/sw.ts).
  if (!url.pathname.startsWith(BASE) || url.pathname === BASE + "sw.js") return;
  if (url.pathname.endsWith("/graph/knowledge-graph.json") || url.pathname.endsWith("/graph/target-graph.json")) {
    e.respondWith(
      fetch(req).then((res) => {
        if (res.ok) caches.open(CACHE).then((c) => c.put(req, res.clone()));
        return res;
      }).catch(() => caches.match(req)),
    );
    return;
  }
  e.respondWith(
    caches.match(req).then((hit) => hit || fetch(req).then((res) => {
      if (res.ok) { const copy = res.clone(); caches.open(CACHE).then((c) => c.put(req, copy)); }
      return res;
    }).catch(() => caches.match(BASE + "index.html"))),
  );
});
