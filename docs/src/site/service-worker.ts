/// <reference lib="webworker" />
// The docs service worker: network-first for HTML (never stale), cache-first for hashed
// assets, with an offline fallback. Version and precache list are injected at build time
// (esbuild --define); the base path is the worker's own scope, so this file is deploy-agnostic.
export {};

// self is the worker global, not Window - shadow the DOM lib's type under this module scope.
declare const self: ServiceWorkerGlobalScope;
declare const SERVICE_WORKER_VERSION: string;
declare const SERVICE_WORKER_PRECACHE: readonly string[];

const base = new URL(self.registration.scope).pathname;
const precacheUrls = SERVICE_WORKER_PRECACHE.map((asset) => base + asset);

self.addEventListener("install", (event) => {
  // Cache each asset on its own: addAll is atomic, so one bad URL would sink the whole precache.
  event.waitUntil(
    caches.open(SERVICE_WORKER_VERSION).then((cache) =>
      Promise.allSettled(precacheUrls.map((url) => cache.add(url)))
    )
  );
  void self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== SERVICE_WORKER_VERSION).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

// Cache a successful response under the current version, keeping the worker alive until the
// write lands. Skips errors and redirects so a 404/500 is never served for the version's life.
function store(event: FetchEvent, request: Request, response: Response): void {
  if (!response.ok || response.redirected) return;
  const copy = response.clone();
  event.waitUntil(caches.open(SERVICE_WORKER_VERSION).then((cache) => cache.put(request, copy)));
}

self.addEventListener("fetch", (event) => {
  const request = event.request;
  if (request.method !== "GET") return;
  if (new URL(request.url).origin !== self.location.origin) return;

  const wantsHtml = (request.headers.get("accept") || "").includes("text/html");
  if (wantsHtml) {
    // Network-first, so readers never stick on stale docs; fall back to cache, then offline.
    event.respondWith(
      fetch(request)
        .then((response) => {
          store(event, request, response);
          return response;
        })
        .catch(() =>
          caches.match(request)
            .then((cached) => cached ?? caches.match(base + "offline/"))
            .then((cached) => cached ?? new Response("Offline", { status: 503 }))
        )
    );
    return;
  }

  // Cache-first for everything else (assets, WASM, JSON).
  event.respondWith(
    caches.match(request).then((cached) => {
      if (cached) return cached;
      return fetch(request).then((response) => {
        store(event, request, response);
        return response;
      });
    })
  );
});
