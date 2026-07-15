// sw.js - the console app's own service worker. A conservative offline shell: it precaches the app
// entry + its bundles + styles so a cold reload works offline, and serves everything else same-origin
// cache-first-then-network. The console talks to the daemon over loopback at runtime (never cached).
// The cache version is bumped by the build (see BUILD_ID) so a new deploy replaces the old shell.
const BUILD_ID = "dev";
const CACHE = "magus-console-" + BUILD_ID;
const BASE = new URL("./", self.location).pathname;

const PRECACHE = [
  BASE,
  BASE + "index.html",
  BASE + "console.js",
  BASE + "console.css",
  // PatternFly (W0 spike): the PF Core bundle + the console's token layer. Additive for now; W4 will
  // drop the Pico-era sheets below (pico.min.css, site.css, ui-panels.css, theme.css) from this list
  // and bump BUILD_ID so clients refetch the new shell.
  BASE + "patternfly.css",
  BASE + "tokens.css",
  BASE + "overrides.css",
  BASE + "theme.css",
  BASE + "site.css",
  BASE + "ui-panels.css",
  BASE + "theme.js",
  BASE + "assets/pico.min.css",
  BASE + "logs/log-viewer.js",
  BASE + "logs/logs.css",
  BASE + "logs/scaffold.html",
  BASE + "graph/explorer.js",
  BASE + "graph/graph.css",
  BASE + "graph/scaffold.html",
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
  if (url.origin !== self.location.origin) return; // never touch daemon/loopback or cross-origin
  e.respondWith(
    caches.match(req).then((hit) => hit || fetch(req).then((res) => {
      if (res.ok) { const copy = res.clone(); caches.open(CACHE).then((c) => c.put(req, copy)); }
      return res;
    }).catch(() => caches.match(BASE + "index.html"))),
  );
});
