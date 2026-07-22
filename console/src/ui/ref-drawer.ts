import { persisted } from "../lib/persist";
import { createTextSearch, type TextSearchEntry } from "@magus/textsearch";
import type { PageModule, PageController, SearchProvider } from "../console/page";

// One docs-index record. It carries a url (for the result link) on top of the fields the
// shared ranker reads; the engine ignores url and hands it back on each result. This is the
// console's OWN record type - the shared lib is app-agnostic and never names it.
interface RefEntry extends TextSearchEntry {
  url: string;
}

// loadRefIndex is the console's OWN data source for the shared search engine (injected via
// createTextSearch below): it fetches the docs site's prebuilt search-index.json. The docs
// site ships it at the SITE ROOT; the console lives at the site's /console/ sibling, so the
// index is one level up. A standalone dev console has no docs sibling, so a console-relative
// copy (a gitignored dev artifact) is tried as a fallback. A miss resolves to null (never
// throws) so the panel degrades to a note. All of this docs-layout knowledge lives HERE, in
// the app - the shared lib knows none of it. The AbortSignal (threaded from the searcher's
// load() call) cancels an in-flight fetch when the load is torn down, so a superseded request
// does not settle against a gone consumer.
async function loadRefIndex(signal?: AbortSignal): Promise<RefEntry[] | null> {
  let base = "";
  try {
    base = import.meta.url.replace(/[^/]*$/, "");
  } catch {
    base = "";
  }
  const candidates = base
    ? [base + "../search-index.json", base + "search-index.json"]
    : ["../search-index.json", "search-index.json"];
  for (const url of candidates) {
    try {
      const r = await fetch(url, { signal });
      if (!r.ok) continue;
      const data = (await r.json()) as RefEntry[];
      if (Array.isArray(data)) return data;
    } catch {
      /* try the next candidate */
    }
  }
  return null;
}

// DrawerToggle is a minimal open/close controller with the Reference panel's dismissal + focus
// behaviour. wireDrawerToggle factors that "pop-out" idiom out of initRefDrawer so a second pop-out (the
// notification center) reuses the SAME mechanics rather than reimplementing them: a trigger toggles a
// panel; Escape and an outside pointerdown dismiss it (unless canDismiss() vetoes, as the pinned
// Reference panel does); focus moves into the panel on open and returns to the trigger on close; and
// aria-expanded (trigger) / aria-hidden (panel) track the state. The panel is shown/hidden with its
// `hidden` attribute, so the caller owns all visual styling. This is intentionally NARROWER than
// initRefDrawer's own state machine (which also owns pinning, inline docking, and resize) - it is the
// shared core, not a replacement for that surface's extra behaviour.
export interface DrawerToggle {
  open(): void;
  close(): void;
  toggle(): void;
  isOpen(): boolean;
}

export function wireDrawerToggle(opts: {
  trigger: HTMLElement;
  panel: HTMLElement;
  onOpen?: () => void;
  onClose?: () => void;
  focusTarget?: () => HTMLElement | null;
  canDismiss?: () => boolean;
}): DrawerToggle {
  const { trigger, panel } = opts;
  let open = false;

  const render = (): void => {
    panel.hidden = !open;
    panel.setAttribute("aria-hidden", open ? "false" : "true");
    trigger.setAttribute("aria-expanded", open ? "true" : "false");
  };

  const setOpen = (v: boolean): void => {
    if (v === open) return;
    open = v;
    render();
    if (v) {
      opts.onOpen?.();
      requestAnimationFrame(() => (opts.focusTarget?.() ?? panel).focus());
    } else {
      opts.onClose?.();
      // Only pull focus back to the trigger when it currently sits inside the panel, so closing via an
      // outside click on some other control does not yank focus away from where the user just went.
      if (document.activeElement instanceof HTMLElement && panel.contains(document.activeElement))
        trigger.focus();
    }
  };

  trigger.addEventListener("click", (e) => {
    e.stopPropagation();
    setOpen(!open);
  });

  // Outside pointerdown dismisses. A click on the trigger is handled by its own listener, so ignore it
  // here to avoid a double-toggle.
  document.addEventListener("pointerdown", (e) => {
    if (!open) return;
    if (opts.canDismiss && !opts.canDismiss()) return;
    const t = e.target;
    if (!(t instanceof Node)) return;
    if (panel.contains(t) || trigger.contains(t)) return;
    setOpen(false);
  });

  document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key === "Escape" && open && (!opts.canDismiss || opts.canDismiss())) setOpen(false);
  });

  render();
  return {
    open: () => setOpen(true),
    close: () => setOpen(false),
    toggle: () => setOpen(!open),
    isOpen: () => open,
  };
}

// ref-drawer.ts - the console's right-side Reference panel, built on a PatternFly Drawer
// (index.html: #console-refdrawer is the pf-v6-c-drawer, #console-outlet-content its __content,
// #console-refpanel its __panel). The drawer is pf-m-inline (PF v6's supported mode: the panel is a
// flex sibling that insets the content); opening toggles pf-m-expanded. On a narrow/mobile screen the
// panel's flex-basis is 100%, so the content shrinks to zero and the panel fills the screen (a
// full-screen reference) - which requires #console-outlet-content to be shrinkable (min-width:0 in
// console.css). "Pinned" is purely PERSISTENCE: it stays open across tab switches and ignores Escape /
// outside-click; "unpinned" closes on Escape or an outside click. Both look the same (inset/docked).
//
// The panel shows two things: a documentation search field (searches the docs site's prebuilt
// index via the shared @magus/textsearch grammar, results open in a new tab) and the
// ACTIVE surface's reference sections. Each surface scaffold carries help blocks marked
// [data-ref-section] (the graph explorer's query/search-syntax help, the log viewer's filter
// help). Because surfaces mount dynamically, this CLONES the active surface's blocks each time
// it opens (and refreshes on tab change while docked).
//
// Pin/unpin maps onto the Drawer honestly: expanded = docked/inset (pf-m-expanded). "Pinned"
// persists across tab switches and ignores Escape / outside-click; "unpinned" closes on Escape
// or a click outside the panel. Cloned example buttons are inert (cloneNode drops listeners), so
// a click inside the panel on an example carrying a distinguishing data-* (the graph's data-q /
// data-view / data-lens) is forwarded to the matching live control in the active surface pane.
export function initRefDrawer(opts: { onBreakOut?: () => void } = {}): void {
  const drawer = document.getElementById("console-refdrawer");
  const panel = document.getElementById("console-refpanel");
  const bodyEl = document.getElementById("console-refdrawer-body");
  const trigger = document.getElementById("console-refbtn");
  if (!drawer || !panel || !bodyEl || !trigger) return;

  const pinBtn = document.getElementById("console-refpin");
  const closeBtn = document.getElementById("console-refclose");
  const breakoutBtn = document.getElementById("console-refbreakout");

  // The active surface is the one visible pane in the outlet (main.ts hides the others).
  const activePane = (): HTMLElement | null =>
    document.querySelector<HTMLElement>("#console-outlet-content div[data-tab-id]:not([hidden])");

  const collect = (pane: HTMLElement | null): HTMLElement[] =>
    pane
      ? [...pane.querySelectorAll<HTMLElement>("[data-ref-section]")].filter(
          (b) => b.id !== "ask-panel",
        )
      : [];

  // The shell's own reference (chords, tabs and panes) is not surface-specific, so it trails EVERY
  // surface's sections rather than belonging to one - and it is the whole of what the launcher shows,
  // which has no surface and so previously read "No reference for this view".
  const shellBlocks = (): HTMLElement[] => {
    const shell = document.getElementById("console-ref-shell");
    return shell ? [...shell.querySelectorAll<HTMLElement>("[data-ref-section]")] : [];
  };

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
      clone.removeAttribute("data-ref-section"); // clones are shown; the [data-ref-section]{display:none} rule hides only sources
      clone.removeAttribute("id");
      clone.querySelectorAll("[id]").forEach((el) => el.removeAttribute("id"));
      if (clone instanceof HTMLDetailsElement) clone.open = true;
      clone.querySelectorAll("details").forEach((d) => {
        (d as HTMLDetailsElement).open = true;
      });
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
    paint([...blocks, ...shellBlocks()]);
    if (pane && blocks.length === 0) {
      const obs = new MutationObserver(() => {
        const found = collect(pane);
        if (found.length > 0) {
          obs.disconnect();
          watcher = null;
          paint([...found, ...shellBlocks()]);
        }
      });
      watcher = obs;
      obs.observe(pane, { childList: true, subtree: true });
      // Stop watching a genuinely reference-less surface after it has had time to mount.
      setTimeout(() => {
        if (watcher === obs) {
          obs.disconnect();
          watcher = null;
        }
      }, 3000);
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
      if (live) {
        e.preventDefault();
        live.click();
        return;
      }
    }
  });

  // --- Documentation search -----------------------------------------------------------------
  // A debounced search over the docs site's prebuilt index. The grammar/ranking come from the
  // shared @magus/textsearch engine; the console supplies its OWN data source (loadRefIndex,
  // above) via createTextSearch, so the engine stays app-agnostic. Results open in a NEW tab so
  // the console stays put. The index is fetched lazily on first focus/type; if it is unreachable
  // (a standalone dev console with no docs sibling), we degrade to a subtle note and never throw.
  const searchInput = document.getElementById("console-refsearch-input") as HTMLInputElement | null;
  const resultsEl = document.getElementById("console-refsearch-results");
  const noteEl = document.getElementById("console-refsearch-note");
  if (searchInput && resultsEl && noteEl) {
    const searcher = createTextSearch<RefEntry>(loadRefIndex);
    let loaded = false; // the fetch has completed (with or without an index)
    let sel = -1;

    // A "how this parsed" preview above the results (teach the grammar as you type), and a PF Spinner
    // that fills the results area while the ~486KB index loads. Both are created here and inserted
    // around the results list so no extra scaffold markup is needed.
    const previewEl = document.createElement("div");
    previewEl.className = "console-shell-refsearch__preview";
    previewEl.hidden = true;
    resultsEl.before(previewEl);

    const spinnerEl = document.createElement("div");
    spinnerEl.className = "console-shell-refsearch__loading";
    spinnerEl.hidden = true;
    spinnerEl.innerHTML =
      '<svg class="pf-v6-c-spinner pf-m-md" role="status" viewBox="0 0 100 100" aria-label="Loading the docs search index">' +
      '<circle class="pf-v6-c-spinner__path" cx="50" cy="50" r="45" fill="none"></circle></svg>';
    resultsEl.before(spinnerEl);

    const showNote = (text: string): void => {
      noteEl.textContent = text;
      noteEl.hidden = false;
    };
    const clearNote = (): void => {
      noteEl.hidden = true;
      noteEl.textContent = "";
    };

    // markMatches builds a fragment with the query's positive terms wrapped in <mark>, so the matched
    // text stands out in a result's title/snippet. Text-node based (never innerHTML from the index), so
    // it cannot inject markup. Case-insensitive; longer terms first so they win an overlap.
    const markMatches = (text: string, terms: string[]): DocumentFragment => {
      const frag = document.createDocumentFragment();
      const uniq = [...new Set(terms.filter((t) => t.length >= 2))].sort(
        (a, b) => b.length - a.length,
      );
      if (uniq.length === 0) {
        frag.append(text);
        return frag;
      }
      const esc = uniq.map((t) => t.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"));
      const re = new RegExp("(" + esc.join("|") + ")", "ig");
      let last = 0;
      for (let m = re.exec(text); m; m = re.exec(text)) {
        if (m.index > last) frag.append(text.slice(last, m.index));
        const mark = document.createElement("mark");
        mark.className = "console-shell-refsearch__hl";
        mark.textContent = m[0];
        frag.append(mark);
        last = m.index + m[0].length;
        if (re.lastIndex === m.index) re.lastIndex++; // guard a zero-width match
      }
      if (last < text.length) frag.append(text.slice(last));
      return frag;
    };

    // renderPreview shows the parsed query as compact read-only chips (field scoping, exclusions,
    // phrases, wildcards), so a user sees how their query was understood.
    const renderPreview = (raw: string): void => {
      const parts = searcher.describeQuery(raw);
      previewEl.replaceChildren();
      if (parts.length === 0) {
        previewEl.hidden = true;
        return;
      }
      for (const p of parts) {
        const chip = document.createElement("span");
        chip.className = "console-shell-refsearch__chip";
        if (p.neg) chip.dataset.neg = "";
        const scope = document.createElement("span");
        scope.className = "console-shell-refsearch__chip-scope";
        scope.textContent =
          (p.neg ? "exclude " : "") + (p.field ? p.field : p.phrase ? "phrase" : "text");
        const val = document.createElement("span");
        val.className = "console-shell-refsearch__chip-value";
        val.textContent = p.value + (p.wildcard ? " *" : "");
        chip.append(scope, val);
        previewEl.append(chip);
      }
      previewEl.hidden = false;
    };

    const optionEls = (): HTMLAnchorElement[] => [
      ...resultsEl.querySelectorAll<HTMLAnchorElement>('a[role="option"]'),
    ];
    const highlight = (): void => {
      const opts = optionEls();
      opts.forEach((a, i) => {
        if (i === sel) {
          a.setAttribute("aria-selected", "true");
          a.scrollIntoView({ block: "nearest" });
        } else a.removeAttribute("aria-selected");
      });
      if (sel >= 0 && opts[sel]) searchInput.setAttribute("aria-activedescendant", opts[sel].id);
      else searchInput.removeAttribute("aria-activedescendant");
    };

    const closeResults = (): void => {
      resultsEl.replaceChildren();
      resultsEl.hidden = true;
      previewEl.hidden = true;
      spinnerEl.hidden = true;
      sel = -1;
      searchInput.setAttribute("aria-expanded", "false");
      searchInput.removeAttribute("aria-activedescendant");
    };

    // Base URL of the docs site (one level up from the console) for result links; falls back to a
    // console-relative path when the site-root sibling is absent (standalone dev).
    const docBase = (url: string): string => {
      const rel = url.replace(/^\//, "");
      try {
        return new URL("../" + rel, window.location.href).href;
      } catch {
        return "../" + rel;
      }
    };

    const render = (): void => {
      const raw = searchInput.value.trim();
      if (!raw) {
        closeResults();
        clearNote();
        return;
      }
      renderPreview(raw); // show how the query parsed as soon as there is a query, even while loading
      const res = searcher.runSearch(raw);
      if (res === null) {
        // The index is not loaded. loaded=false: a fetch is pending -> show the spinner; the input
        // handler re-renders when it lands. loaded=true with no index: the docs index is unreachable.
        if (loaded) {
          spinnerEl.hidden = true;
          closeResults();
          renderPreview(raw);
          showNote("Search needs the docs site.");
        } else {
          spinnerEl.hidden = false;
          resultsEl.hidden = true;
        }
        return;
      }
      spinnerEl.hidden = true;
      clearNote();
      const terms = searcher.getPositiveTerms(raw);
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
        title.append(markMatches(r.entry.title, terms));
        a.append(title);
        const snip = searcher.buildSnippet(r.entry.text || r.entry.description || "", terms);
        if (snip) {
          const sn = document.createElement("span");
          sn.className = "console-shell-refsearch__snippet";
          sn.append(markMatches(snip, terms));
          a.append(sn);
        }
        li.append(a);
        resultsEl.append(li);
      });
      resultsEl.hidden = false;
    };

    // Kick the lazy index load once; re-render when it resolves so a query typed before the fetch
    // finished still gets results (or the unreachable-note). The load carries an AbortSignal so a
    // page teardown cancels the in-flight ~486KB fetch instead of letting it settle in the void.
    const loadAbort = new AbortController();
    window.addEventListener("pagehide", () => loadAbort.abort(), { once: true });
    const ensureIndex = (): void => {
      if (loaded) return;
      searcher
        .load(loadAbort.signal)
        .then(() => {
          loaded = true;
          render();
        })
        .catch((err) => {
          loaded = true;
          // Degrade to the unreachable-note (render() shows it), but surface the real reason so a
          // genuine index-pipeline failure (bad JSON, a moved path) is not silently invisible.
          if (!loadAbort.signal.aborted) console.error("docs search index failed to load", err);
          render();
        });
    };

    let debounce: ReturnType<typeof setTimeout> | undefined;
    searchInput.addEventListener("input", () => {
      ensureIndex();
      clearTimeout(debounce);
      debounce = setTimeout(render, 90);
    });
    searchInput.addEventListener("focus", () => {
      ensureIndex();
      if (searchInput.value.trim()) render();
    });
    searchInput.addEventListener("keydown", (e: KeyboardEvent) => {
      const opts = optionEls();
      if (e.key === "ArrowDown" && opts.length) {
        e.preventDefault();
        sel = (sel + 1) % opts.length;
        highlight();
      } else if (e.key === "ArrowUp" && opts.length) {
        e.preventDefault();
        sel = (sel <= 0 ? opts.length : sel) - 1;
        highlight();
      } else if (e.key === "Enter") {
        if (sel >= 0 && opts[sel]) {
          e.preventDefault();
          window.open(opts[sel].href, "_blank", "noopener");
        }
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
    // Pin is what SNAPS THE LAYOUT TO FIT: pinned docks the panel INLINE (pf-m-inline), so PF's own
    // drawer mechanics inset the __content and the surfaces reflow to the narrower box. Unpinned drops
    // pf-m-inline, so the panel floats over the content as an overlay (content keeps full width) - a
    // quick peek that Escape / an outside click dismisses. (PF's CSS-only non-inline drawer parks the
    // panel off the right edge, so the unpinned overlay position is authored in console.css.)
    drawer.classList.toggle("pf-m-inline", pinned && isOpen);
    panel.setAttribute("aria-hidden", isOpen ? "false" : "true");
    trigger.setAttribute("aria-expanded", isOpen ? "true" : "false");
    pinBtn?.setAttribute("aria-pressed", pinned && isOpen ? "true" : "false");
  };

  const setOpen = (open: boolean): void => {
    isOpen = open;
    if (open) refresh();
    // Closing a pinned panel also unpins it, so it does not spring back on the next open.
    if (!open && pinned) {
      pinned = false;
      pinnedCell.set(false);
    }
    render();
    // Move focus into the panel on open (the close button), back to the trigger on close.
    if (open) requestAnimationFrame(() => closeBtn?.focus());
    else if (
      document.activeElement instanceof HTMLElement &&
      panel.contains(document.activeElement)
    )
      trigger.focus();
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
  // Break out to a tab: promote the console-wide reference into its own persistent, tileable tab, then
  // close the panel (its content now lives in the tab). main.ts supplies onBreakOut (it opens the
  // reference surface below).
  breakoutBtn?.addEventListener("click", () => {
    opts.onBreakOut?.();
    setOpen(false);
  });

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

  // --- Resize (PF resizable Drawer) ---------------------------------------------------------
  // The panel width is user-draggable via PF's splitter handle. PF Core ships the splitter markup
  // and styling; the drag/keyboard interaction is ours (PF Core is CSS-only). We drive the panel's
  // flex-basis vars in px so the width holds across breakpoints, clamp to a sane range, and persist
  // it. On a phone the panel is a full-screen overlay (CSS), so the splitter is hidden there.
  const splitter = document.getElementById("console-refsplitter");
  if (splitter) {
    const MIN_W = 280;
    const widthCell = persisted("ref-width", 0); // 0 = "use the PF default until first drag"
    const maxW = (): number =>
      Math.min(760, Math.max(MIN_W, Math.round(drawer.getBoundingClientRect().width * 0.85)));
    const applyWidth = (w: number): void => {
      const clamped = Math.max(MIN_W, Math.min(maxW(), Math.round(w)));
      panel.style.setProperty("--pf-v6-c-drawer__panel--md--FlexBasis", clamped + "px");
      panel.style.setProperty("--pf-v6-c-drawer__panel--xl--FlexBasis", clamped + "px");
      const dw = drawer.getBoundingClientRect().width || 1;
      splitter.setAttribute("aria-valuenow", String(Math.round((clamped / dw) * 100)));
      widthCell.set(clamped);
    };
    const savedW = widthCell.get();
    if (savedW >= MIN_W) applyWidth(savedW);

    let dragging = false;
    const onMove = (e: PointerEvent): void => {
      if (!dragging) return;
      // Right-docked panel: width grows as the pointer moves toward the left edge.
      applyWidth(drawer.getBoundingClientRect().right - e.clientX);
    };
    const stop = (): void => {
      if (!dragging) return;
      dragging = false;
      splitter.classList.remove("console-shell-refsplitter--dragging");
      document.body.style.removeProperty("user-select");
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", stop);
    };
    splitter.addEventListener("pointerdown", (e: PointerEvent) => {
      if (e.button !== 0) return;
      dragging = true;
      splitter.classList.add("console-shell-refsplitter--dragging");
      document.body.style.userSelect = "none"; // no text selection while dragging
      window.addEventListener("pointermove", onMove);
      window.addEventListener("pointerup", stop);
      e.preventDefault();
    });
    // Keyboard resize: the splitter is focusable (role=separator); arrows nudge, Home/End jump.
    splitter.addEventListener("keydown", (e: KeyboardEvent) => {
      const cur = panel.getBoundingClientRect().width;
      const step = e.shiftKey ? 48 : 16;
      if (e.key === "ArrowLeft") {
        applyWidth(cur + step);
        e.preventDefault();
      } else if (e.key === "ArrowRight") {
        applyWidth(cur - step);
        e.preventDefault();
      } else if (e.key === "Home") {
        applyWidth(maxW());
        e.preventDefault();
      } else if (e.key === "End") {
        applyWidth(MIN_W);
        e.preventDefault();
      }
    });
  }

  // main.ts dispatches this when the active tab changes; a docked/open panel re-reads the new
  // surface's help sections.
  document.addEventListener("console:activetab", () => {
    if (isOpen) refresh();
  });

  // Apply the persisted (pinned) state without animating the slide on load.
  drawer.classList.add("console-shell-refdrawer--instant");
  render();
  if (isOpen) refresh();
  requestAnimationFrame(() => drawer.classList.remove("console-shell-refdrawer--instant"));
}

// referenceSurface is the "break out to tab" target: a lightweight, single-instance surface that renders
// the CONSOLE-WIDE reference (the #console-ref-shell sections - chords, tabs/panes, where-your-data-goes),
// the same always-true help the drawer trails after every surface and the whole of what the launcher
// shows. It is deliberately NOT a live mirror of the drawer's surface-specific sections: a tab persists by
// pageId alone (no payload), so it must re-derive stable content on every mount/reload - and the shell
// reference is exactly that. main.ts registers it (kept out of the launcher SURFACES list, so it has no
// card and is reachable only via the drawer's break-out button) and opens it single-instance.
const noRefSearch: SearchProvider<null> = {
  placeholder: "",
  parse: () => null,
  apply: () => ({ matches: 0 }),
};

export function referenceSurface(): PageModule<null, null> {
  return {
    id: "reference",
    title: "Reference",
    async activate(host: HTMLElement): Promise<PageController<null, null>> {
      const root = document.createElement("div");
      root.dataset.surface = "reference";
      const body = document.createElement("div");
      body.className = "console-shell-refdrawer__body";
      const shell = document.getElementById("console-ref-shell");
      const blocks = shell ? [...shell.querySelectorAll<HTMLElement>("[data-ref-section]")] : [];
      for (const b of blocks) {
        const clone = b.cloneNode(true) as HTMLElement;
        clone.removeAttribute("data-ref-section"); // clones are shown; [data-ref-section]{display:none} hides only sources
        clone.removeAttribute("id");
        clone.querySelectorAll("[id]").forEach((el) => el.removeAttribute("id"));
        if (clone instanceof HTMLDetailsElement) clone.open = true;
        clone.querySelectorAll("details").forEach((d) => {
          (d as HTMLDetailsElement).open = true;
        });
        body.append(clone);
      }
      root.append(body);
      host.append(root);
      return { search: noRefSearch, deactivate: () => {} };
    },
  };
}
