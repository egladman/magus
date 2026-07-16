import { persisted } from "../lib/persist";
import { loadDocIndex, runSearch, positiveTerms, snippet, type DocSearchEntry } from "../lib/docsearch";

// ref-drawer.ts - the console's right-side Reference panel, built on a PatternFly Drawer
// (index.html: #console-refdrawer is the pf-v6-c-drawer, #console-outlet-content its __content,
// #console-refpanel its __panel). Opening toggles pf-m-expanded; because the drawer carries
// pf-m-inline, an expanded panel INSETS the tab content (docked) rather than overlaying it - so
// there is no backdrop, which is the PF-conventional docked behavior.
//
// The panel shows two things: a documentation search field (searches the docs site's prebuilt
// index via the ported Datadog grammar in lib/docsearch.ts, results open in a new tab) and the
// ACTIVE surface's reference sections. Each surface scaffold carries help blocks marked
// [data-legacy-ref] (the graph explorer's query/search-syntax help, the log viewer's filter
// help). Because surfaces mount dynamically, this CLONES the active surface's blocks each time
// it opens (and refreshes on tab change while docked).
//
// Pin/unpin maps onto the Drawer honestly: expanded = docked/inset (pf-m-expanded). "Pinned"
// persists across tab switches and ignores Escape / outside-click; "unpinned" closes on Escape
// or a click outside the panel. Cloned example buttons are inert (cloneNode drops listeners), so
// a click inside the panel on an example carrying a distinguishing data-* (the graph's data-q /
// data-view / data-lens) is forwarded to the matching live control in the active surface pane.
export function initRefDrawer(): void {
  const drawer = document.getElementById("console-refdrawer");
  const panel = document.getElementById("console-refpanel");
  const bodyEl = document.getElementById("console-refdrawer-body");
  const trigger = document.getElementById("console-refbtn");
  if (!drawer || !panel || !bodyEl || !trigger) return;

  const pinBtn = document.getElementById("console-refpin");
  const closeBtn = document.getElementById("console-refclose");

  // The active surface is the one visible pane in the outlet (main.ts hides the others).
  const activePane = (): HTMLElement | null =>
    document.querySelector<HTMLElement>("#console-outlet-content div[data-tab-id]:not([hidden])");

  const collect = (pane: HTMLElement | null): HTMLElement[] =>
    pane ? [...pane.querySelectorAll<HTMLElement>("[data-legacy-ref]")].filter((b) => b.id !== "ask-panel") : [];

  // Paint the given reference blocks into the panel body. Cloning (not moving) keeps the source
  // intact so a surface can unmount/remount freely. Nested <details> open so the reference reads
  // as content; ids are stripped from clones to avoid duplicates.
  const paint = (blocks: HTMLElement[]): void => {
    bodyEl.replaceChildren();
    if (blocks.length === 0) {
      const empty = document.createElement("p");
      empty.className = "console-shell-refdrawer__empty";
      empty.textContent = "No reference for this view.";
      bodyEl.append(empty);
      return;
    }
    for (const b of blocks) {
      const clone = b.cloneNode(true) as HTMLElement;
      clone.removeAttribute("data-legacy-ref"); // clones are shown; the [data-legacy-ref]{display:none} rule hides only sources
      clone.removeAttribute("id");
      clone.querySelectorAll("[id]").forEach((el) => el.removeAttribute("id"));
      if (clone instanceof HTMLDetailsElement) clone.open = true;
      clone.querySelectorAll("details").forEach((d) => { (d as HTMLDetailsElement).open = true; });
      bodyEl.append(clone);
    }
  };

  // Show the active surface's reference sections. #ask-panel is skipped (the graph explorer
  // surfaces those "Ask a question" views in its sidebar already). A freshly opened surface mounts
  // its scaffold asynchronously (its bundle is a dynamic import), so if the pane has no blocks yet,
  // watch it briefly and repaint once its content lands - otherwise a just-opened tab would read
  // "No reference".
  let watcher: MutationObserver | null = null;
  const refresh = (): void => {
    watcher?.disconnect();
    watcher = null;
    const pane = activePane();
    const blocks = collect(pane);
    paint(blocks);
    if (pane && blocks.length === 0) {
      const obs = new MutationObserver(() => {
        const found = collect(pane);
        if (found.length > 0) { obs.disconnect(); watcher = null; paint(found); }
      });
      watcher = obs;
      obs.observe(pane, { childList: true, subtree: true });
      // Stop watching a genuinely reference-less surface after it has had time to mount.
      setTimeout(() => { if (watcher === obs) { obs.disconnect(); watcher = null; } }, 3000);
    }
  };

  // Forward a click on a cloned example to the live control in the active surface. Match by the
  // first distinguishing attribute present; the source control (same attr+value) carries the real
  // listener.
  const FORWARD_ATTRS = ["data-q", "data-view", "data-lens"] as const;
  bodyEl.addEventListener("click", (e) => {
    const t = e.target;
    if (!(t instanceof Element)) return;
    const src = t.closest<HTMLElement>("[data-q],[data-view],[data-lens]");
    if (!src) return;
    const pane = activePane();
    if (!pane) return;
    for (const attr of FORWARD_ATTRS) {
      const val = src.getAttribute(attr);
      if (val === null) continue;
      const live = pane.querySelector<HTMLElement>(`[${attr}="${CSS.escape(val)}"]`);
      if (live) { e.preventDefault(); live.click(); return; }
    }
  });

  // --- Documentation search -----------------------------------------------------------------
  // A debounced search over the docs site's prebuilt index. The grammar/ranking is the ported
  // Datadog-style search from the docs site (lib/docsearch.ts). Results open in a NEW tab so the
  // console stays put. The index is fetched lazily on first focus/type; if it is unreachable (a
  // standalone dev console with no docs sibling), we degrade to a subtle note and never throw.
  const searchInput = document.getElementById("console-refsearch-input") as HTMLInputElement | null;
  const resultsEl = document.getElementById("console-refsearch-results");
  const noteEl = document.getElementById("console-refsearch-note");
  if (searchInput && resultsEl && noteEl) {
    let index: DocSearchEntry[] | null = null;
    let loaded = false; // the fetch has completed (with or without an index)
    let sel = -1;

    const showNote = (text: string): void => { noteEl.textContent = text; noteEl.hidden = false; };
    const clearNote = (): void => { noteEl.hidden = true; noteEl.textContent = ""; };

    const optionEls = (): HTMLAnchorElement[] =>
      [...resultsEl.querySelectorAll<HTMLAnchorElement>('a[role="option"]')];
    const highlight = (): void => {
      const opts = optionEls();
      opts.forEach((a, i) => {
        if (i === sel) { a.setAttribute("aria-selected", "true"); a.scrollIntoView({ block: "nearest" }); }
        else a.removeAttribute("aria-selected");
      });
      if (sel >= 0 && opts[sel]) searchInput.setAttribute("aria-activedescendant", opts[sel].id);
      else searchInput.removeAttribute("aria-activedescendant");
    };

    const closeResults = (): void => {
      resultsEl.replaceChildren();
      resultsEl.hidden = true;
      sel = -1;
      searchInput.setAttribute("aria-expanded", "false");
      searchInput.removeAttribute("aria-activedescendant");
    };

    // Base URL of the docs site (one level up from the console) for result links; falls back to a
    // console-relative path when the site-root sibling is absent (standalone dev).
    const docBase = (url: string): string => {
      const rel = url.replace(/^\//, "");
      try { return new URL("../" + rel, window.location.href).href; } catch { return "../" + rel; }
    };

    const render = (): void => {
      const raw = searchInput.value.trim();
      if (!raw) { closeResults(); clearNote(); return; }
      if (!index) {
        // Not yet loaded, or the load failed. loaded=false: a fetch is pending, the input handler
        // re-renders when it lands. loaded=true with no index: the docs index is unreachable.
        if (loaded) { closeResults(); showNote("Search needs the docs site."); }
        return;
      }
      clearNote();
      const res = runSearch(index, raw);
      const terms = positiveTerms(raw);
      resultsEl.replaceChildren();
      sel = -1;
      searchInput.setAttribute("aria-expanded", "true");
      if (res.length === 0) {
        const li = document.createElement("li");
        li.className = "console-shell-refsearch__empty";
        li.setAttribute("role", "presentation");
        li.textContent = "No matches.";
        resultsEl.append(li);
        resultsEl.hidden = false;
        return;
      }
      res.slice(0, 10).forEach((r, i) => {
        const li = document.createElement("li");
        li.setAttribute("role", "presentation"); // the <a> is the listbox option
        const a = document.createElement("a");
        a.className = "console-shell-refsearch__result";
        a.id = "console-refsearch-opt-" + i;
        a.setAttribute("role", "option");
        a.href = docBase(r.entry.url);
        a.target = "_blank";
        a.rel = "noopener";
        const title = document.createElement("span");
        title.className = "console-shell-refsearch__title";
        title.textContent = r.entry.title;
        a.append(title);
        const snip = snippet(r.entry.text || r.entry.description || "", terms);
        if (snip) {
          const sn = document.createElement("span");
          sn.className = "console-shell-refsearch__snippet";
          sn.textContent = snip;
          a.append(sn);
        }
        li.append(a);
        resultsEl.append(li);
      });
      resultsEl.hidden = false;
    };

    // Kick the lazy index load once; re-render when it resolves so a query typed before the fetch
    // finished still gets results (or the unreachable-note).
    const ensureIndex = (): void => {
      if (loaded) return;
      loadDocIndex().then((data) => { loaded = true; index = data; render(); }).catch(() => { loaded = true; render(); });
    };

    let debounce: ReturnType<typeof setTimeout> | undefined;
    searchInput.addEventListener("input", () => {
      ensureIndex();
      clearTimeout(debounce);
      debounce = setTimeout(render, 90);
    });
    searchInput.addEventListener("focus", () => { ensureIndex(); if (searchInput.value.trim()) render(); });
    searchInput.addEventListener("keydown", (e: KeyboardEvent) => {
      const opts = optionEls();
      if (e.key === "ArrowDown" && opts.length) { e.preventDefault(); sel = (sel + 1) % opts.length; highlight(); }
      else if (e.key === "ArrowUp" && opts.length) { e.preventDefault(); sel = (sel <= 0 ? opts.length : sel) - 1; highlight(); }
      else if (e.key === "Enter") {
        if (sel >= 0 && opts[sel]) { e.preventDefault(); window.open(opts[sel].href, "_blank", "noopener"); }
      } else if (e.key === "Escape" && !resultsEl.hidden) {
        // Swallow Escape while a result list is open so it closes the list, not the whole panel.
        e.stopPropagation();
        closeResults();
      }
    });
  }

  // --- Pin / open state ---------------------------------------------------------------------
  // Pinned persists: a pinned panel is docked open on load and stays docked as you switch tabs; an
  // unpinned panel closes on Escape / outside-click.
  const pinnedCell = persisted("ref-pinned", false);
  let pinned = pinnedCell.get();
  let isOpen = pinned;

  const render = (): void => {
    drawer.classList.toggle("pf-m-expanded", isOpen);
    panel.setAttribute("aria-hidden", isOpen ? "false" : "true");
    trigger.setAttribute("aria-expanded", isOpen ? "true" : "false");
    pinBtn?.setAttribute("aria-pressed", pinned && isOpen ? "true" : "false");
  };

  const setOpen = (open: boolean): void => {
    isOpen = open;
    if (open) refresh();
    // Closing a pinned panel also unpins it, so it does not spring back on the next open.
    if (!open && pinned) { pinned = false; pinnedCell.set(false); }
    render();
    // Move focus into the panel on open (the close button), back to the trigger on close.
    if (open) requestAnimationFrame(() => closeBtn?.focus());
    else if (document.activeElement instanceof HTMLElement && panel.contains(document.activeElement)) trigger.focus();
  };

  const togglePin = (): void => {
    pinned = !pinned;
    pinnedCell.set(pinned);
    if (pinned) isOpen = true;
    render();
  };

  trigger.addEventListener("click", () => setOpen(!isOpen));
  closeBtn?.addEventListener("click", () => setOpen(false));
  pinBtn?.addEventListener("click", togglePin);

  // Outside-click closes an unpinned panel. A click on the trigger is handled by its own listener,
  // so ignore it here to avoid a double-toggle.
  document.addEventListener("pointerdown", (e) => {
    if (!isOpen || pinned) return;
    const t = e.target;
    if (!(t instanceof Node)) return;
    if (panel.contains(t) || trigger.contains(t)) return;
    setOpen(false);
  });

  document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key === "Escape" && isOpen && !pinned) setOpen(false);
  });

  // main.ts dispatches this when the active tab changes; a docked/open panel re-reads the new
  // surface's help sections.
  document.addEventListener("console:activetab", () => { if (isOpen) refresh(); });

  // Apply the persisted (pinned) state without animating the slide on load.
  drawer.classList.add("console-shell-refdrawer--instant");
  render();
  if (isOpen) refresh();
  requestAnimationFrame(() => drawer.classList.remove("console-shell-refdrawer--instant"));
}
