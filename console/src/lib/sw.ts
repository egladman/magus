// sw.ts - registering the console's service worker (../sw.js), shared by the surfaces that need one.
//
// sw.js's PRECACHE is the SHELL's asset list (index.html, console.js, console.css, every surface
// bundle), so the SHELL is what has to register it. Leave it to one surface and a console whose
// operator never opens that surface runs with no worker at all - no offline shell, and no install
// offer either, since Chromium's prompt algorithm still requires a fetch handler even though the
// browser-menu install no longer does.
//
// The script URL is a PARAMETER, not resolved here: each surface is its own esbuild bundle, so
// import.meta.url differs per output file and only the caller knows where ../sw.js sits relative to it.
// Every caller resolves the same gen/sw.js, so the registrations coincide and the second is a no-op.

// registerServiceWorker registers `url` and resolves with the registration, or null when the browser has
// no service workers, the origin is insecure, or registration failed. It never throws: a missing worker
// degrades the console (no offline shell) but must never break its boot.
export function registerServiceWorker(
  url: URL | string,
): Promise<ServiceWorkerRegistration | null> {
  if (typeof navigator === "undefined" || !("serviceWorker" in navigator))
    return Promise.resolve(null);
  // Service workers are secure-context only. The daemon's loopback URL qualifies; a plain http:// LAN
  // share link does not, which is also why the console is not installable from one.
  const secure = location.protocol === "https:" || location.hostname === "localhost";
  if (!secure) return Promise.resolve(null);

  const go = (): Promise<ServiceWorkerRegistration | null> =>
    navigator.serviceWorker.register(url).catch((e: unknown) => {
      // not-a-failure: no worker only costs the offline shell and the install offer; the console runs
      console.debug("service worker registration", e);
      return null;
    });

  // Waiting on `load` unconditionally never fires when a surface reaches this through a dynamic import:
  // by then the page has long finished loading, and the listener is registered for an event that has
  // already been and gone - so the surface silently gets no service worker at all, while the same
  // code on a standalone page (a deferred module script, which does run before `load`) works.
  if (document.readyState === "complete") return go();
  return new Promise((resolve) => {
    window.addEventListener("load", () => resolve(go()), { once: true });
  });
}

// ---- the served build ------------------------------------------------------
//
// The daemon serves the console's files from disk, so a restarted daemon (or a rebuilt console) can
// serve a newer build than the one this page is running, while the service worker keeps handing the
// page its cached copy. The old bundle then talks to a daemon whose messages it does not fully know,
// and a view it cannot fill reads as empty rather than out of date. So the page compares the build it
// runs with the build the daemon serves, and asks for a reload when they differ.
//
// A build is sw.js's stamped BUILD_ID (scripts/stamp-build.mjs), a digest of every precached file.

const CACHE_PREFIX = "magus-console-";

// buildIdOf reads the BUILD_ID a sw.js source was stamped with, or null for an unstamped one.
export function buildIdOf(workerSource: string): string | null {
  const m = workerSource.match(/const BUILD_ID = "([^"]+)";/);
  return m && m[1] !== "unstamped" ? m[1] : null;
}

// servedBuildId fetches sw.js past every cache (the worker never serves it) and reads its build.
export async function servedBuildId(workerUrl: URL | string): Promise<string | null> {
  const res = await fetch(workerUrl, { cache: "no-store" });
  return res.ok ? buildIdOf(await res.text()) : null;
}

// cachedBuildId is the build the service worker served this page from, or null when no worker
// controls the page. Read at boot: a worker that updates later replaces the cache but not the code
// already running.
async function cachedBuildId(): Promise<string | null> {
  if (typeof navigator === "undefined" || !navigator.serviceWorker?.controller) return null;
  if (typeof caches === "undefined") return null;
  const keys = (await caches.keys()).filter((k) => k.startsWith(CACHE_PREFIX));
  return keys.length === 1 ? keys[0].slice(CACHE_PREFIX.length) : null;
}

// watchServedBuild calls onStale once when the daemon serves a build other than the one this page
// runs: at boot, then every intervalMs. Returns a stop function.
export function watchServedBuild(
  workerUrl: URL | string,
  onStale: (running: string, served: string) => void,
  intervalMs = 5 * 60 * 1000,
): () => void {
  let running: string | null = null;
  let told = false;
  const check = async (): Promise<void> => {
    if (told) return;
    let served: string | null;
    try {
      served = await servedBuildId(workerUrl);
    } catch (e) {
      // reported: a daemon that cannot serve sw.js is unreachable, which the daemon transport and the
      // status stream report; this check only answers "which build", and retries next interval.
      console.debug("served build check", e);
      return;
    }
    if (!served) return;
    running ??= (await cachedBuildId()) ?? served;
    if (served === running) return;
    told = true;
    onStale(running, served);
  };
  void check();
  const timer = setInterval(() => void check(), intervalMs);
  return () => clearInterval(timer);
}
