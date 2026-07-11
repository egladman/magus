// service-worker-register.ts - register gen/sw.js and offer a refresh on update.
//
// Progressive enhancement: no navigator.serviceWorker (feature-detected) ->
// no registration -> the plain static site keeps working. Only registers on
// http(s), not file://, so a locally-opened page doesn't try. The register
// call happens after 'load' so it never contends with the LCP request.
//
// The service worker skipWaiting()s, so a new version takes control immediately and
// fires controllerchange. If the page was ALREADY controlled, that's an update (not
// a first install): rather than let the swap happen silently, we show a small toast
// offering a refresh so the reader gets the new assets when they choose to.

(function () {
  if (typeof navigator === "undefined") return;
  if (!("serviceWorker" in navigator)) return;
  if (location.protocol !== "https:" && location.hostname !== "localhost") return;

  // Resolve sw.js from the bundle's own URL so it registers with the right
  // scope under /magus/ (or wherever the site is deployed).
  const ROOT = import.meta.url.replace(/main\.js(\?.*)?$/, "");
  const hadController = !!navigator.serviceWorker.controller;

  // Name the surface the reader is actually on, so the update prompt reads
  // naturally on the app-like pages ("the playground", "the graph explorer")
  // instead of the generic "the docs". The service worker still updates the whole
  // site at once - this is friendlier copy for where the reader stands, not a
  // claim that only that page changed.
  function pageSubject(): string {
    const p = location.pathname.replace(/\/+$/, "");
    if (/\/playground$/.test(p)) return "the playground";
    if (/\/graph$/.test(p)) return "the graph explorer";
    return "the docs";
  }

  function showUpdateToast(): void {
    if (document.querySelector(".sw-toast")) return;
    const toast = document.createElement("div");
    toast.className = "sw-toast";
    toast.setAttribute("role", "status");

    const msg = document.createElement("span");
    msg.textContent = "A newer version of " + pageSubject() + " is available.";
    toast.appendChild(msg);

    const refresh = document.createElement("button");
    refresh.type = "button";
    refresh.textContent = "Refresh";
    refresh.addEventListener("click", () => { location.reload(); });
    toast.appendChild(refresh);

    document.body.appendChild(toast);
  }

  // A new service worker taking control (the current one skipWaiting()s) fires this.
  // Only treat it as an update worth surfacing if we were already controlled.
  navigator.serviceWorker.addEventListener("controllerchange", () => {
    if (hadController) showUpdateToast();
  });

  window.addEventListener("load", () => {
    navigator.serviceWorker.register(ROOT + "sw.js", { scope: ROOT }).catch(() => {
      // Registration failures are non-fatal - the plain static site works fine.
    });
  });
})();
