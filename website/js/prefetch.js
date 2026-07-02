// prefetch.js - warm the cache for internal links the reader is about to click.
//
// On mouseover / touchstart of any same-origin link (skipping anchors, alias
// stubs, and the playground WASM route), inject a <link rel="prefetch"> once.
// The browser fetches the target with idle priority and reuses it when the click
// actually navigates; the perceived latency drops without paying the cost of
// SPA-style navigation. Enhancement only: no JS, no prefetch.

(function () {
  if (typeof window === "undefined") return;
  var origin = window.location.origin;
  var seen = new Set();

  function isPrefetchable(a) {
    if (!a || !a.href) return false;
    if (a.target && a.target !== "_self") return false; // opens in a new tab
    if (a.hasAttribute("download")) return false;
    // Fragment-only or same-page link: nothing to fetch.
    if (a.href === window.location.href) return false;
    if (a.pathname === window.location.pathname && a.href.indexOf("#") >= 0) return false;
    // Cross-origin: skip.
    if (a.origin !== origin) return false;
    // Non-HTML asset: the browser caches it fine on its own.
    if (/\.(jpg|jpeg|png|webp|gif|svg|css|js|woff2?|wasm|xml|json|txt|pdf)($|\?)/i.test(a.pathname)) return false;
    if (seen.has(a.href)) return false;
    return true;
  }

  function prefetch(href) {
    seen.add(href);
    var link = document.createElement("link");
    link.rel = "prefetch";
    link.href = href;
    document.head.appendChild(link);
  }

  function onHover(e) {
    var a = e.target.closest && e.target.closest("a");
    if (!a || !isPrefetchable(a)) return;
    prefetch(a.href);
  }

  document.addEventListener("mouseover", onHover, { passive: true });
  document.addEventListener("touchstart", onHover, { passive: true });
})();
