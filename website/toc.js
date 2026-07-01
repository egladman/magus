// Collapsible TOC. A labeled toggle button above the content hides the "On this
// page" sidebar and lets the article reflow to the full container width; the
// choice persists across pages. With no stored preference the sidebar starts
// open on the wide (two-column) layout and collapsed on narrow screens. No-ops
// where there is no .with-toc grid.
(function () {
  var grid = document.querySelector(".with-toc");
  if (!grid) return;

  var KEY = "toc-collapsed";
  var stored = null;
  try { stored = localStorage.getItem(KEY); } catch (e) {}
  var collapsed = stored === null ? window.innerWidth < 1024 : stored === "1";

  var LIST_ICON =
    '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<line x1="8" y1="6" x2="21" y2="6"></line><line x1="8" y1="12" x2="21" y2="12"></line><line x1="8" y1="18" x2="21" y2="18"></line>' +
    '<line x1="3" y1="6" x2="3.01" y2="6"></line><line x1="3" y1="12" x2="3.01" y2="12"></line><line x1="3" y1="18" x2="3.01" y2="18"></line></svg>';

  var btn = document.createElement("button");
  btn.type = "button";
  btn.className = "toc-toggle outline";
  btn.innerHTML = LIST_ICON; // one stable icon; state is shown by the active style, not an icon swap

  // The toggle lives in a .page-tools toolbar (a shared row above the content)
  // so search.js can drop its field in beside it. Resolve the toolbar within the
  // enclosing <main> - the same scope search.js searches - so whichever script
  // runs first wins and the other reuses it, rather than each making its own.
  var main = grid.closest("main") || grid.parentNode;
  var tools = main.querySelector(".page-tools");
  if (!tools) {
    tools = document.createElement("div");
    tools.className = "page-tools";
    // Same insertion point search.js uses (main's first child) so that whichever
    // script wins the create race, the toolbar lands in one consistent place.
    main.insertBefore(tools, main.firstChild);
  }
  tools.insertBefore(btn, tools.firstChild);

  // The icon stays put; aria-expanded conveys state and drives the active
  // (filled) vs inactive (outline) styling in CSS - a clearer on/off signal than
  // swapping the icon. aria-label/title still spell out the action for hover and
  // screen readers.
  function apply() {
    grid.classList.toggle("toc-collapsed", collapsed);
    var label = collapsed ? "Show table of contents" : "Hide table of contents";
    btn.setAttribute("aria-label", label);
    btn.setAttribute("title", label);
    btn.setAttribute("aria-expanded", collapsed ? "false" : "true");
  }
  apply();

  btn.addEventListener("click", function () {
    collapsed = !collapsed;
    try { localStorage.setItem(KEY, collapsed ? "1" : "0"); } catch (e) {}
    apply();
  });
})();

// Table-of-contents scroll-spy. Highlights the TOC link for the section
// currently in view by setting aria-current="page" on it (Pico styles that with
// an underline + primary color, matching the top-nav "you are here" indicator).
// No-ops on pages that have no .toc sidebar.
(function () {
  var toc = document.querySelector(".toc nav");
  if (!toc) return;

  // Map each heading id -> its TOC link, and collect the headings to observe.
  var links = {};
  toc.querySelectorAll('a[href^="#"]').forEach(function (a) {
    // A malformed fragment (e.g. a lone "%") makes decodeURIComponent throw;
    // fall back to the raw slug so one bad link can't abort scroll-spy setup.
    var raw = a.getAttribute("href").slice(1), id;
    try { id = decodeURIComponent(raw); } catch (e) { id = raw; }
    if (id) links[id] = a;
  });

  var headings = [];
  Object.keys(links).forEach(function (id) {
    var el = document.getElementById(id);
    if (el) headings.push(el);
  });
  if (!headings.length) return;

  var current = null;
  function setCurrent(id) {
    if (id === current) return;
    if (current && links[current]) links[current].removeAttribute("aria-current");
    current = id;
    if (current && links[current]) links[current].setAttribute("aria-current", "page");
  }

  // Track which headings are above the top of the viewport (offset to clear the
  // sticky header). The lowest such heading is the section being read; if none
  // are above yet, highlight the first.
  var visible = new Set();
  var observer = new IntersectionObserver(
    function (entries) {
      entries.forEach(function (e) {
        if (e.isIntersecting) visible.add(e.target.id);
        else visible.delete(e.target.id);
      });
      pick();
    },
    // rootMargin takes only px/% (no rem): -72px ~= the 4.5rem sticky header.
    { rootMargin: "-72px 0px -70% 0px", threshold: 0 }
  );
  headings.forEach(function (h) { observer.observe(h); });

  function pick() {
    if (visible.size) {
      // Choose the topmost heading still intersecting the active band.
      var best = null, bestTop = Infinity;
      headings.forEach(function (h) {
        if (!visible.has(h.id)) return;
        var top = h.getBoundingClientRect().top;
        if (top < bestTop) { bestTop = top; best = h.id; }
      });
      if (best) setCurrent(best);
      return;
    }
    // Nothing in the band: pick the last heading scrolled past, else the first.
    var passed = null;
    headings.forEach(function (h) {
      if (h.getBoundingClientRect().top < 80) passed = h.id;
    });
    setCurrent(passed || headings[0].id);
  }

  pick();
})();
