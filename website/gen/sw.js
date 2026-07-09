// Auto-generated at build time (gen/sw.js). Template lives at website/sw.js.tmpl;
// the render substitutes magus-ac432de82f77 and /magus/ before copying into gen/.
const VERSION = "magus-ac432de82f77";
const BASE = "/magus/";

const PRECACHE = [
  BASE + "search-index.json",
  BASE + "playground/wasm_exec.js",
  BASE + "playground/buzz.wasm",
  BASE + "graph/explorer.js",
  BASE + "graph/graph.css",
  BASE + "main.js",
  BASE + "theme.js",
  BASE + "site.css",
  BASE + "theme.css",
  // pico.min.css is same-origin (no CDN); precache so every page is styled on a
  // cold offline load. The large mermaid bundle (gen/assets/mermaid.js) is NOT
  // precached - it caches on first use via the cache-first same-origin asset path.
  BASE + "assets/pico.min.css",
  // offline/ is the SW fallback for failed navigations - it must be in the cache
  // before any navigation fails, so it is precached unconditionally here.
  BASE + "offline/",
];

self.addEventListener("install", (e) => {
  e.waitUntil(caches.open(VERSION).then((c) => c.addAll(PRECACHE)).catch(() => {}));
  self.skipWaiting();
});

self.addEventListener("activate", (e) => {
  e.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== VERSION).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener("fetch", (e) => {
  const req = e.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;

  const accept = req.headers.get("accept") || "";
  const isDoc = accept.indexOf("text/html") >= 0;

  if (isDoc) {
    // Network-first for HTML so clients never get stuck on stale docs.
    e.respondWith(
      fetch(req).then((r) => {
        const copy = r.clone();
        caches.open(VERSION).then((c) => c.put(req, copy));
        return r;
      }).catch(() => caches.match(req).then((c) => c || caches.match(BASE + "offline/")))
    );
    return;
  }

  // Cache-first for everything else (assets/wasm/json).
  e.respondWith(
    caches.match(req).then((cached) => cached || fetch(req).then((r) => {
      const copy = r.clone();
      caches.open(VERSION).then((c) => c.put(req, copy));
      return r;
    }))
  );
});
