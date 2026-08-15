// sw.ts - registering the console's service worker (../sw.js), shared by the surfaces that need one.
//
// It was the DASHBOARD's private helper until the Install section landed, which exposed the gap: sw.js's
// PRECACHE is the SHELL's asset list (index.html, console.js, console.css, every surface bundle), yet
// only the dashboard ever registered it. A console whose operator never opened the dashboard therefore
// ran with no worker at all - no offline shell, and no install offer either, since Chromium's prompt
// algorithm still requires a fetch handler even though the browser-menu install no longer does.
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
    navigator.serviceWorker.register(url).catch(() => null);

  // Waiting on `load` unconditionally never fires when a surface reaches this through a dynamic import:
  // by then the page has long finished loading, and the listener is registered for an event that has
  // already been and gone - so the dashboard silently ran with no service worker at all inside the
  // shell, while the standalone page (a deferred module script, which does run before `load`) worked.
  if (document.readyState === "complete") return go();
  return new Promise((resolve) => {
    window.addEventListener("load", () => resolve(go()), { once: true });
  });
}
