import { persisted } from "../lib/persist";
import { registerPopup, notifyPopupOpen } from "./popups.js";

// Collapsible TOC. A labeled toggle button above the content hides the "On this
// page" sidebar and lets the article reflow to the full container width; the
// choice persists across pages. With no stored preference the sidebar starts
// open on the wide (two-column) layout and collapsed on narrow screens. No-ops
// where there is no .with-toc grid.
export function initTocToggle(): void {
  const grid = document.querySelector(".with-toc");
  if (!grid) return;

  // Durable, cross-tab collapse state. With nothing stored the fallback picks the
  // default from the viewport (open on wide layouts, collapsed on narrow ones).
  const collapsed = persisted("toc-collapsed", window.innerWidth < 1024);

  // A document-outline glyph (a page with heading lines), deliberately NOT a plain
  // three-line "list": the navbar's hamburger menu button is three lines, so a page
  // shape keeps this "table of contents" control from being mistaken for site nav.
  const TOC_ICON =
    '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path>' +
    '<polyline points="14 2 14 8 20 8"></polyline>' +
    '<line x1="8" y1="13" x2="14" y2="13"></line>' +
    '<line x1="8" y1="17" x2="16" y2="17"></line></svg>';

  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "toc-toggle outline";
  btn.innerHTML = TOC_ICON; // one stable icon; state is shown by the active style, not an icon swap

  // The toggle lives in a .page-tools toolbar (a shared row above the content)
  // so search.js can drop its field in beside it. Resolve the toolbar within the
  // enclosing <main> - the same scope search.js searches - so whichever script
  // runs first wins and the other reuses it, rather than each making its own.
  const main = grid.closest("main") || grid.parentNode;
  if (!main) return;
  let tools = main.querySelector(".page-tools");
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
  // Two modes, chosen by viewport. Desktop: the toggle collapses the sidebar and
  // the article reflows to full width (persisted). Mobile: the toggle drops a
  // top-sheet tray down over a dimming backdrop.
  const mq = window.matchMedia("(max-width: 1023px)");
  const tocAside = grid.querySelector(".toc");
  let backdrop: HTMLDivElement | null = null;

  function setLabel(open: boolean): void {
    const label = open ? "Hide table of contents" : "Show table of contents";
    btn.setAttribute("aria-label", label);
    btn.setAttribute("title", label);
    btn.setAttribute("aria-expanded", open ? "true" : "false");
  }
  // A const arrow (not a hoisted function declaration) so the null-guard above
  // narrows grid to non-null inside it.
  const applyDesktop = (): void => {
    grid.classList.toggle("toc-collapsed", collapsed.get());
    setLabel(!collapsed.get());
  };
  // Registered with the popup coordinator so opening the nav menu / gear panel closes
  // an open TOC sheet, and opening the sheet closes them. Only meaningful on mobile
  // (the sheet); on desktop closeSheet just clears classes that are not set.
  const dismissable = { close: (): void => closeSheet() };
  registerPopup(dismissable);

  function openSheet(): void {
    if (!tocAside) return;
    tocAside.classList.add("toc-sheet-open");
    if (!backdrop) {
      backdrop = document.createElement("div");
      backdrop.className = "toc-backdrop";
      backdrop.addEventListener("click", closeSheet);
      document.body.appendChild(backdrop);
    }
    setLabel(true);
    notifyPopupOpen(dismissable);
  }
  function closeSheet(): void {
    if (tocAside) tocAside.classList.remove("toc-sheet-open");
    if (backdrop) {
      backdrop.remove();
      backdrop = null;
    }
    setLabel(false);
  }
  // Reconcile state for the current breakpoint: on mobile the TOC is a closed
  // bottom sheet (never desktop-collapsed, which would display:none it); on
  // desktop the sheet is closed and the persisted collapse state applies.
  const syncMode = (): void => {
    closeSheet();
    if (mq.matches) {
      grid.classList.remove("toc-collapsed");
      setLabel(false);
    } else {
      applyDesktop();
    }
  };
  syncMode();

  btn.addEventListener("click", function () {
    if (mq.matches) {
      if (tocAside && tocAside.classList.contains("toc-sheet-open")) closeSheet();
      else openSheet();
    } else {
      collapsed.set(!collapsed.get());
      applyDesktop();
    }
  });

  // Close the sheet on Escape and when a TOC link is tapped (the page navigates).
  document.addEventListener("keydown", function (e: KeyboardEvent) {
    if (e.key === "Escape") closeSheet();
  });
  if (tocAside) {
    tocAside.addEventListener("click", function (e: Event) {
      const t = e.target;
      if (mq.matches && t instanceof Element && t.closest("a")) closeSheet();
    });
  }
  // Reset cleanly when crossing the breakpoint.
  mq.addEventListener("change", syncMode);
}

// Table-of-contents scroll-spy. Highlights the TOC link for the section
// currently in view by setting aria-current="page" on it (Pico styles that with
// an underline + primary color, matching the top-nav "you are here" indicator).
// No-ops on pages that have no .toc sidebar.
export function initScrollSpy(): void {
  const toc = document.querySelector(".toc nav");
  if (!toc) return;

  // Map each heading id -> its TOC link, and collect the headings to observe.
  const links: Record<string, Element> = {};
  toc.querySelectorAll('a[href^="#"]').forEach((a) => {
    // A malformed fragment (e.g. a lone "%") makes decodeURIComponent throw;
    // fall back to the raw slug so one bad link can't abort scroll-spy setup.
    const href = a.getAttribute("href");
    if (!href) return;
    const raw = href.slice(1);
    let id: string;
    try {
      id = decodeURIComponent(raw);
    } catch {
      id = raw;
    }
    if (id) links[id] = a;
  });

  const headings: HTMLElement[] = [];
  Object.keys(links).forEach((id) => {
    const el = document.getElementById(id);
    if (el) headings.push(el);
  });
  if (!headings.length) return;

  let current: string | null = null;
  function setCurrent(id: string): void {
    if (id === current) return;
    if (current && links[current]) links[current].removeAttribute("aria-current");
    current = id;
    if (current && links[current]) links[current].setAttribute("aria-current", "page");
  }

  // Track which headings are above the top of the viewport (offset to clear the
  // sticky header). The lowest such heading is the section being read; if none
  // are above yet, highlight the first.
  const visible = new Set<string>();
  const observer = new IntersectionObserver(
    (entries) => {
      entries.forEach((e) => {
        if (e.isIntersecting) visible.add(e.target.id);
        else visible.delete(e.target.id);
      });
      pick();
    },
    // rootMargin takes only px/% (no rem): -72px ~= the 4.5rem sticky header.
    { rootMargin: "-72px 0px -70% 0px", threshold: 0 },
  );
  headings.forEach((h) => {
    observer.observe(h);
  });

  function pick(): void {
    if (visible.size) {
      // Choose the topmost heading still intersecting the active band.
      let best: string | null = null,
        bestTop = Infinity;
      headings.forEach((h) => {
        if (!visible.has(h.id)) return;
        const top = h.getBoundingClientRect().top;
        if (top < bestTop) {
          bestTop = top;
          best = h.id;
        }
      });
      if (best) setCurrent(best);
      return;
    }
    // Nothing in the band: pick the last heading scrolled past, else the first.
    let passed: string | null = null;
    headings.forEach((h) => {
      if (h.getBoundingClientRect().top < 80) passed = h.id;
    });
    setCurrent(passed || headings[0].id);
  }

  pick();
}
