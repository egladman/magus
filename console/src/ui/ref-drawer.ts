import { persisted } from "../lib/persist";

// ref-drawer.ts - a right-side slide-out reference panel shared by the console apps (graph
// explorer, log viewer, ...). A page marks its reference blocks with [data-legacy-ref] and supplies a
// trigger (.ref-trigger), the drawer (#ref-drawer) and a backdrop (#ref-backdrop). This relocates
// the sections into the drawer (so the same blocks serve as inline no-JS content AND as the drawer
// body - no duplicate markup) and wires open/close: the trigger, the close button, a backdrop
// click, and Escape. No-ops where there is no drawer, like every other main.js module.
export function initRefDrawer(): void {
  const drawer = document.getElementById("ref-drawer");
  const backdrop = document.getElementById("ref-backdrop");
  if (!drawer || !backdrop) return;

  // Pull the injected docs search bar (.page-tools, built by search.js) up into the drawer, so on the
  // CONSOLE apps "quick search" lives in the reference panel. Gated on data-relocate-search: the DOCS
  // site keeps its prominent top search bar in place (its drawer holds only reference links), so its
  // drawer omits the flag. search.js is imported before this module (main.ts) so the element exists.
  if (drawer.dataset.relocateSearch === "true") {
    const search = document.querySelector(".page-tools");
    if (search) drawer.appendChild(search);
  }

  // Then relocate the page's reference sections, in document order, and expand them by default so
  // they read as content, not folded-away toggles (still collapsible). CSS hides them inline
  // (.js [data-legacy-ref]) and reveals them once inside (#ref-drawer [data-legacy-ref]).
  document.querySelectorAll("[data-legacy-ref]").forEach((s) => {
    drawer.appendChild(s);
    if (s instanceof HTMLDetailsElement) s.open = true;
  });

  // --- inline docs browsing ------------------------------------------------
  // A docs link clicked inside the drawer (a search result, or a link within an already-open doc)
  // loads that page's <article> INTO the panel instead of navigating away, with a back trail. Only
  // same-origin HTML pages are taken over; external / asset / in-page-hash links navigate normally.
  const docview = document.createElement("div");
  docview.className = "ref-docview";
  const backBtn = document.createElement("button");
  backBtn.type = "button";
  backBtn.className = "ref-doc-back";
  backBtn.textContent = "← Back";
  const docBody = document.createElement("div");
  docBody.className = "ref-doc";
  const docBar = document.createElement("div");
  docBar.className = "ref-docview-bar";
  docBar.append(backBtn);
  docview.append(docBar, docBody);
  drawer.appendChild(docview);

  const trail: string[] = [];

  const showReference = (): void => {
    drawer.classList.remove("browsing");
    trail.length = 0;
    docBody.replaceChildren();
    drawer.scrollTop = 0;
  };

  const isDocLink = (a: HTMLAnchorElement): boolean => {
    if (a.origin !== location.origin) return false;              // external
    if (a.hasAttribute("download") || (a.target && a.target !== "_self")) return false;
    if (/\.(png|jpe?g|webp|gif|svg|css|js|json|xml|txt|pdf|wasm|zip)(\?|$)/i.test(a.pathname)) return false;
    if (a.pathname === location.pathname && a.hash) return false; // in-page anchor: let it scroll
    return true;
  };

  const openDoc = async (url: string): Promise<void> => {
    try {
      const res = await fetch(url);
      if (!res.ok) { location.href = url; return; }
      const parsed = new DOMParser().parseFromString(await res.text(), "text/html");
      const article = parsed.querySelector("main article") ?? parsed.querySelector("main");
      if (!article) { location.href = url; return; }
      article.querySelectorAll("script").forEach((s) => s.remove());
      // Resolve relative href/src against the fetched page so its links + images work from the panel.
      article.querySelectorAll("[href], [src]").forEach((el) => {
        (["href", "src"] as const).forEach((attr) => {
          const v = el.getAttribute(attr);
          if (v && !/^(https?:|data:|mailto:|#)/i.test(v)) el.setAttribute(attr, new URL(v, url).href);
        });
      });
      docBody.replaceChildren(article);
      drawer.classList.add("browsing");
      // If the link carried a #anchor (a glossary term, a section), scroll that heading into view in
      // the panel so the reader lands on the exact definition; otherwise start at the top.
      const hash = url.includes("#") ? url.slice(url.indexOf("#") + 1) : "";
      const target = hash ? docBody.querySelector('[id="' + CSS.escape(hash) + '"]') : null;
      if (target) target.scrollIntoView(); else drawer.scrollTop = 0;
    } catch {
      location.href = url; // network/parse failure: just navigate there
    }
  };

  backBtn.addEventListener("click", () => {
    trail.pop();                          // drop the current doc
    const prev = trail[trail.length - 1]; // the one before it, if any
    if (prev) void openDoc(prev); else showReference();
  });

  drawer.addEventListener("click", (e) => {
    const t = e.target;
    if (!(t instanceof Element)) return;
    const a = t.closest("a");
    if (!(a instanceof HTMLAnchorElement) || !isDocLink(a)) return;
    e.preventDefault();
    trail.push(a.href);
    void openDoc(a.href);
  });

  const triggers = document.querySelectorAll(".ref-trigger");
  const pinBtn = drawer.querySelector(".ref-pin");

  // Pinned state persists across pages: pin it once and the panel stays docked as you navigate
  // between the console apps. A pinned panel docks beside the content (no dim); an unpinned one is
  // a temporary overlay with a dimming backdrop. `pinned` is a local mirror of the durable cell.
  const pinnedCell = persisted("ref-pinned", false);
  let pinned = pinnedCell.get();
  const savePinned = (v: boolean): void => pinnedCell.set(v);

  let isOpen = pinned; // a pinned panel is open on load

  const render = (): void => {
    drawer.classList.toggle("open", isOpen);
    drawer.classList.toggle("pinned", isOpen && pinned);
    // The reflow (content shrinks to make room) and the un-dimmed view are the pinned mode.
    document.body.classList.toggle("ref-pinned", isOpen && pinned);
    // The backdrop only dims in overlay mode; a pinned panel sits beside the content, undimmed.
    backdrop.classList.toggle("open", isOpen && !pinned);
    drawer.setAttribute("aria-hidden", isOpen ? "false" : "true");
    triggers.forEach((t) => t.setAttribute("aria-expanded", isOpen ? "true" : "false"));
    if (pinBtn) pinBtn.setAttribute("aria-pressed", pinned ? "true" : "false");
  };

  const setOpen = (open: boolean): void => {
    isOpen = open;
    // Closing returns to the reference view (leave any doc you were reading); closing a pinned panel
    // also unpins it so it does not spring back on the next page.
    if (!open) { showReference(); if (pinned) { pinned = false; savePinned(false); } }
    render();
  };
  const togglePin = (): void => {
    pinned = !pinned;
    savePinned(pinned);
    if (pinned) isOpen = true; // pinning docks it open
    render();
  };

  // A glossary/reference link clicked ANYWHERE on the page (not just inside the drawer) opens its
  // target INLINE in the reference panel instead of navigating away - so looking up a term keeps you
  // on the surface. Opt-in by class (.gloss-link) or data-ref-open, and only for same-origin doc
  // links; a fetch failure (e.g. a daemon that does not serve the page) falls back to navigation.
  document.addEventListener("click", (e) => {
    const t = e.target;
    if (!(t instanceof Element)) return;
    const a = t.closest("a.gloss-link, a[data-ref-open]");
    if (!(a instanceof HTMLAnchorElement) || !isDocLink(a)) return;
    e.preventDefault();
    if (!isOpen) setOpen(true);
    trail.length = 0;
    trail.push(a.href);
    void openDoc(a.href);
  });

  triggers.forEach((t) => t.addEventListener("click", () => setOpen(!isOpen)));
  const closeBtn = drawer.querySelector(".ref-drawer-close");
  if (closeBtn) closeBtn.addEventListener("click", () => setOpen(false));
  backdrop.addEventListener("click", () => setOpen(false));
  if (pinBtn) pinBtn.addEventListener("click", togglePin);
  document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key !== "Escape" || !isOpen) return;
    // While reading a doc, Escape steps back to the reference view first; then it closes an overlay
    // panel (a pinned panel stays put).
    if (drawer.classList.contains("browsing")) { showReference(); return; }
    if (!pinned) setOpen(false);
  });

  // Apply the persisted state on load without animating the slide/reflow on every navigation.
  document.documentElement.classList.add("ref-instant");
  render();
  requestAnimationFrame(() => document.documentElement.classList.remove("ref-instant"));
}
