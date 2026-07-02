// service-worker-register.js - register gen/sw.js on eligible loads.
//
// Progressive enhancement: no navigator.serviceWorker (feature-detected) ->
// no registration -> the plain static site keeps working. Only registers on
// http(s), not file://, so a locally-opened page doesn't try. The register
// call happens after 'load' so it never contends with the LCP request.

(function () {
  if (typeof navigator === "undefined") return;
  if (!("serviceWorker" in navigator)) return;
  if (location.protocol !== "https:" && location.hostname !== "localhost") return;

  // Resolve sw.js from the bundle's own URL so it registers with the right
  // scope under /magus/ (or wherever the site is deployed).
  var ROOT = import.meta.url.replace(/main\.js(\?.*)?$/, "");

  window.addEventListener("load", function () {
    navigator.serviceWorker.register(ROOT + "sw.js", { scope: ROOT }).catch(function () {
      // Registration failures are non-fatal - the plain static site works fine.
    });
  });
})();
