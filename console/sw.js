// sw.js - the console app's own service worker. A conservative offline shell: it precaches the app
// entry + its bundles + styles so a cold reload works offline, and serves everything else same-origin
// cache-first-then-network. The console talks to the daemon over loopback at runtime (never cached).
// BUILD_ID is a PLACEHOLDER in this source file. scripts/stamp-sw.mjs rewrites the line below in
// gen/sw.js as the last step of every build, replacing it with a hash of the precached bytes. That
// stamp is what makes the whole update path work: a browser only re-installs a service worker whose
// own BYTES changed, so while BUILD_ID was a hand-written constant that nothing bumped, every deploy
// shipped a byte-identical sw.js, `install` never fired, `activate` never dropped the old CACHE, and
// the old bundles were served cache-first forever - a fix could not reach a client that had already
// cached the app. Seeing the literal "unstamped" reach a browser means the stamp step did not run.
const BUILD_ID = "unstamped";
const CACHE = "magus-console-" + BUILD_ID;
const BASE = new URL("./", self.location).pathname;

const PRECACHE = [
  BASE,
  BASE + "index.html",
  BASE + "console.js",
  BASE + "console.css",
  // PatternFly Core is the console's only design system now. The Pico-era sheets (pico.min.css,
  // site.css, ui-panels.css, theme.css) were removed at the W4 cutover.
  //
  // scripts/stamp-sw.mjs READS this array out of the built file to decide what to hash, so keep every
  // entry a plain `BASE + "<literal>"` (no computed paths, no double quotes in the comments here) and
  // keep every listed path something the build actually produces - a precache entry with no file on
  // disk makes cache.addAll reject, which fails install and leaves the worker permanently stuck.
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
  BASE + "graph/knowledge-graph.json",
  BASE + "graph/target-graph.json",
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

// SHELL_EXT is the file-type half of the cacheable-path allowlist: the extensions the built shell
// actually ships. Same-origin is NOT a sufficient test on the daemon's loopback mount, where the
// console and the daemon API share one origin - without an allowlist the SSE stream (fetchSSE issues
// a plain GET to /api/v1/events) and probes like /readyz are same-origin GETs whose ok response would
// be buffered into the cache and then replayed to the app cache-first, forever.
const SHELL_EXT = [".html", ".js", ".css", ".json", ".webmanifest", ".png", ".svg", ".ico", ".woff2"];

// isShellPath reports whether pathname belongs to the built app shell: under this worker's own scope
// AND either a directory route (the clean /console/<surface>/ paths, which serve index.html) or a file
// with a shell extension. Anything else is passed straight through to the network - not cached, and
// not answered from cache.
function isShellPath(pathname) {
  if (!pathname.startsWith(BASE)) return false;
  if (pathname.endsWith("/")) return true;
  return SHELL_EXT.some((ext) => pathname.endsWith(ext));
}

// cacheable adds the response half of the rule: an ok response that does not forbid storage. A
// no-store response is one the origin has explicitly said must not be held, and the shell has no
// business overriding that.
function cacheable(res) {
  return res.ok && (res.headers.get("Cache-Control") || "").includes("no-store") === false;
}

self.addEventListener("fetch", (e) => {
  const req = e.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return; // never touch daemon/loopback or cross-origin
  if (isShellPath(url.pathname) === false) return; // same-origin API and streams are not shell files
  if (url.pathname.endsWith("/graph/knowledge-graph.json") || url.pathname.endsWith("/graph/target-graph.json")) {
    e.respondWith(
      fetch(req).then((res) => {
        if (cacheable(res)) caches.open(CACHE).then((c) => c.put(req, res.clone()));
        return res;
      }).catch(() => caches.match(req)),
    );
    return;
  }
  e.respondWith(
    caches.match(req).then((hit) => hit || fetch(req).then((res) => {
      if (cacheable(res)) { const copy = res.clone(); caches.open(CACHE).then((c) => c.put(req, copy)); }
      return res;
    }).catch((err) => {
      // Offline. Only a NAVIGATION may fall back to the app shell. Answering a failed module, sheet,
      // or JSON fetch with index.html hands the caller HTML it cannot parse, so a plain network
      // failure surfaces as a bogus syntax error and hides what actually went wrong.
      if (req.mode === "navigate") return caches.match(BASE + "index.html");
      throw err;
    })),
  );
});
