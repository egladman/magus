// service-worker-register.js - register gen/sw.js and offer a refresh on update.
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
  var ROOT = import.meta.url.replace(/main\.js(\?.*)?$/, "");
  var hadController = !!navigator.serviceWorker.controller;

  function showUpdateToast() {
    if (document.querySelector(".sw-toast")) return;
    var toast = document.createElement("div");
    toast.className = "sw-toast";
    toast.setAttribute("role", "status");

    var msg = document.createElement("span");
    msg.textContent = "A newer version of the docs is available.";
    toast.appendChild(msg);

    var refresh = document.createElement("button");
    refresh.type = "button";
    refresh.textContent = "Refresh";
    refresh.addEventListener("click", function () { location.reload(); });
    toast.appendChild(refresh);

    document.body.appendChild(toast);
  }

  // A new service worker taking control (the current one skipWaiting()s) fires this.
  // Only treat it as an update worth surfacing if we were already controlled.
  navigator.serviceWorker.addEventListener("controllerchange", function () {
    if (hadController) showUpdateToast();
  });

  window.addEventListener("load", function () {
    navigator.serviceWorker.register(ROOT + "sw.js", { scope: ROOT }).catch(function () {
      // Registration failures are non-fatal - the plain static site works fine.
    });
  });
})();
