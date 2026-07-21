// Site search. An always-visible search field in the .page-tools toolbar (beside
// the TOC toggle); it carries its own magnifier, so there's no separate button.
// Matches gen/search-index.json (a flat {url, title, text, tags, description}
// record per page, built at render time, fetched lazily on first focus) and shows
// ranked results in a dropdown under the toolbar. No-ops if there's no <main>.
//
// Query syntax: space-separated terms are AND-ed; "quoted phrases" must appear
// contiguously; a -term excludes pages containing it. Datadog-style field scoping
// narrows a term to one field: tag:foo (exact match on a frontmatter tag),
// title:foo / description:foo / text:foo (substring on that field), and their
// negations (-tag:foo). Values with spaces are quoted: tag:"remote cache". Matching
// is substring with relevance ranking: title hits weigh highest, then curated
// frontmatter tags, then the page description, then body text (word-start hits add
// a bonus at each level). If nothing matches exactly, a typo-tolerant subsequence
// pass on titles rescues near-misses.

// The search grammar - the lexer, parser, and ranker - lives in the shared, DOM-free
// @magus/textsearch engine, the one source of truth this docs site and the console both
// consume (no more copy of the parser per app). This module is the docs site's DOM glue
// around it: the toolbar dropdown, the /search/ page, the token chips, and the tag facets.
// The docs site injects its OWN index into runSearch (fetched below); buildQuery/Token
// drive the chip preview (the raw token stream, which describeQuery's AST walk would
// collapse away); runSearch/getPositiveTerms do the matching and highlighting.
import {
  buildQuery,
  getPositiveTerms,
  runSearch,
  type TextSearchEntry,
  type Token,
} from "@magus/textsearch";

// One record in the site's search index (gen/search-index.json). It carries a url (for the
// result link) on top of the fields the shared ranker reads; the engine ignores url and
// hands it back on each result. This is the docs site's OWN record type - @magus/textsearch
// is app-agnostic and never names it.
interface SearchEntry extends TextSearchEntry {
  url: string;
}

export function initSearch(): void {
  const main = document.querySelector("main.container") || document.querySelector("main");
  if (!main) return;

  // Resolve gen/'s root from the bundle's own URL so the index fetch and result
  // links resolve at any page depth. import.meta.url is the module's own URL under
  // esbuild's --format=esm output (main.js sits at gen/'s root); pre-bundle this
  // used document.currentScript, which is null in ES modules.
  const ROOT = import.meta.url.replace(/main\.js(\?.*)?$/, "");

  // A dedicated /search/ page carries a #search-page container; when present, run
  // the full-page results mode (its own input + uncapped, URL-synced results) and
  // skip building the header dropdown so there's only one search box on that page.
  if (document.getElementById("search-page")) {
    initSearchPage();
    return;
  }

  // page() renders the toolbar (with the edit link); reuse it, else create one so
  // search works on any page.
  let toolsEl = main.querySelector(".page-tools");
  if (!toolsEl) {
    const t = document.createElement("div");
    t.className = "page-tools";
    main.insertBefore(t, main.firstChild);
    toolsEl = t;
  }
  const tools = toolsEl; // const with a non-null type, so closures below see it non-null
  const edit = tools.querySelector(".page-tools-edit");

  // Detect the platform once: it drives both the visual hint badge and the
  // input's tooltip/aria label, so all three name the same modifier key.
  const platform =
    (navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform ||
    navigator.platform ||
    "";
  const isMac = /mac/i.test(platform);
  const modKey = isMac ? "⌘K" : "Ctrl+K"; // the compact hint badge
  const modName = isMac ? "Cmd-K" : "Ctrl-K"; // spoken/tooltip form

  const input = document.createElement("input");
  input.type = "text"; // not type=search: avoids Pico's pill shape; the magnifier is a CSS bg
  input.className = "search-input";
  input.placeholder = "Search docs...";
  input.autocomplete = "off";
  input.setAttribute("aria-label", "Search the documentation (press / or " + modName + ")");
  input.title = "Press / or " + modName + " to search";
  // Combobox semantics so a screen reader announces the results list and the
  // arrow-key-highlighted option (aria-activedescendant, set in highlight()).
  input.setAttribute("role", "combobox");
  input.setAttribute("aria-autocomplete", "list");
  input.setAttribute("aria-expanded", "false");
  input.setAttribute("aria-controls", "search-results");

  const results = document.createElement("ul");
  results.id = "search-results";
  results.className = "search-results";
  results.setAttribute("role", "listbox");
  results.hidden = true;

  // Polite live region announcing the result count as the query changes. The
  // combobox's aria-activedescendant announces the focused option while arrowing,
  // but a screen-reader user still needs the "N results" summary after typing.
  const live = document.createElement("div");
  live.className = "sr-only";
  live.setAttribute("role", "status");
  live.setAttribute("aria-live", "polite");

  // Wrap input + keybinding hint together so the hint can be positioned inside
  // the input's visual boundary. Screen readers skip the hint (aria-hidden).
  const wrap = document.createElement("div");
  wrap.className = "search-wrap";
  wrap.appendChild(input);

  const hint = document.createElement("span");
  hint.className = "search-hint";
  hint.setAttribute("aria-hidden", "true");
  hint.innerHTML = "<kbd>/</kbd> <kbd>" + modKey + "</kbd>";
  wrap.appendChild(hint);

  if (edit) tools.insertBefore(wrap, edit);
  else tools.appendChild(wrap);
  tools.appendChild(results);
  tools.appendChild(live);

  // One lazy fetch of the index, shared by the header dropdown and the /search/
  // page. ready() fires once the index is available (immediately if already
  // loaded); fail() fires on a fetch/parse error. Index stays null (not []) on
  // failure so a later focus/input retries rather than wedging until a page reload.
  let index: SearchEntry[] | null = null,
    loading = false;
  function fetchIndex(ready: () => void, fail?: () => void): void {
    if (index) {
      ready();
      return;
    }
    if (loading) return;
    loading = true;
    fetch(ROOT + "search-index.json")
      .then((r) => {
        if (!r.ok) throw new Error("search index HTTP " + r.status);
        return r.json();
      })
      .then((data: unknown) => {
        if (!Array.isArray(data)) throw new Error("search index is not an array");
        index = data;
        loading = false;
        ready();
      })
      .catch(() => {
        loading = false;
        if (fail) fail();
      });
  }
  function loadIndex(): void {
    fetchIndex(
      () => {
        render(input.value);
      },
      () => {
        if (input.value.trim() && document.activeElement === input) {
          showMessage("Search index unavailable");
        }
      },
    );
  }

  const HTML_ESCAPES: Record<string, string> = {
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
  };
  function escapeHtml(s: string): string {
    return String(s).replace(/[&<>"]/g, (c) => HTML_ESCAPES[c]);
  }

  // sel indexes the arrow-key-highlighted option within the rendered results
  // (-1 = none). highlight() reflects it into the DOM/ARIA; render() resets it.
  let sel = -1;
  function options(): NodeListOf<HTMLAnchorElement> {
    return results.querySelectorAll<HTMLAnchorElement>('a[role="option"]');
  }
  function highlight(opts: NodeListOf<HTMLAnchorElement>): void {
    for (let i = 0; i < opts.length; i++) {
      if (i === sel) {
        opts[i].setAttribute("aria-selected", "true");
        input.setAttribute("aria-activedescendant", opts[i].id);
        opts[i].scrollIntoView({ block: "nearest" });
      } else {
        opts[i].removeAttribute("aria-selected");
      }
    }
    if (sel < 0) input.removeAttribute("aria-activedescendant");
  }

  function setExpanded(open: boolean): void {
    input.setAttribute("aria-expanded", open ? "true" : "false");
    if (!open) input.removeAttribute("aria-activedescendant");
  }

  // showMessage replaces the dropdown with a single status line and opens it, so
  // the user always gets feedback (no matches, or an index that failed to load).
  function showMessage(text: string): void {
    results.innerHTML = "";
    sel = -1;
    const li = document.createElement("li");
    li.className = "search-empty";
    li.textContent = text;
    results.appendChild(li);
    results.hidden = false;
    setExpanded(true);
    live.textContent = text;
  }

  // Persistent dropdown footer that makes the preview-vs-page relationship explicit:
  // a compact syntax hint (the same operators the /search/ page documents in full)
  // and a clear door to the dedicated page for the current query. Not a listbox
  // option, so arrow keys skip it; it is mouse/Tab reachable.
  function footerLi(query: string, count: number): HTMLLIElement {
    const li = document.createElement("li");
    li.className = "search-footer";
    li.setAttribute("role", "presentation");
    const label =
      count > 0 ? "See all " + count + " result" + (count === 1 ? "" : "s") : "Open full search";
    li.innerHTML =
      '<a class="search-seeall" href="' +
      ROOT +
      "search/?q=" +
      encodeURIComponent(query) +
      '">' +
      label +
      ' <span class="xref-arrow" aria-hidden="true">&rarr;</span></a>' +
      '<span class="search-synhint">Syntax: <code>tag:name</code> <code>-term</code> <code>&quot;phrase&quot;</code></span>';
    return li;
  }

  function render(raw: string): void {
    results.innerHTML = "";
    sel = -1;
    const query = (raw || "").trim();
    if (!query) {
      results.hidden = true;
      setExpanded(false);
      live.textContent = "";
      return;
    }
    if (!index) {
      loadIndex();
      results.hidden = true;
      setExpanded(false);
      return;
    } // re-renders once loaded
    // Capture the now-non-null index before any call that could invalidate the narrowing;
    // runSearch takes the corpus as its first argument now that it lives in the shared lib.
    const res = runSearch(index, query);
    results.hidden = false;
    setExpanded(true);

    if (!res.length) {
      const empty = document.createElement("li");
      empty.className = "search-empty";
      empty.textContent = "No matches";
      results.appendChild(empty);
      results.appendChild(footerLi(query, 0));
      live.textContent = "No matches";
      return;
    }
    live.textContent = res.length + (res.length === 1 ? " result" : " results");
    res.slice(0, 8).forEach((r, i) => {
      const li = document.createElement("li");
      li.setAttribute("role", "presentation"); // the <a> is the listbox option, not the <li>
      const a = document.createElement("a");
      a.href = ROOT + r.entry.url;
      a.id = "search-opt-" + i;
      a.setAttribute("role", "option");
      a.innerHTML =
        '<span class="search-title">' +
        escapeHtml(r.entry.title) +
        "</span>" +
        '<span class="search-url">' +
        escapeHtml(r.entry.url) +
        "</span>";
      li.appendChild(a);
      results.appendChild(li);
    });
    // Preview shows the top 8; the footer names the true total and links to the
    // dedicated page, so "dropdown = quick preview, page = everything" reads clearly.
    results.appendChild(footerLi(query, res.length));
  }

  input.addEventListener("focus", () => {
    loadIndex();
    if (input.value.trim()) render(input.value);
  });
  // Debounce typing so a burst of keystrokes coalesces into one index scan +
  // DOM rebuild instead of one per character. Focus/arrow paths stay immediate.
  let debounce: number | undefined;
  input.addEventListener("input", () => {
    clearTimeout(debounce);
    debounce = setTimeout(() => {
      render(input.value);
    }, 80);
  });
  input.addEventListener("keydown", (e: KeyboardEvent) => {
    const opts = options();
    if (e.key === "ArrowDown" && opts.length) {
      e.preventDefault(); // don't move the caret
      sel = (sel + 1) % opts.length;
      highlight(opts);
    } else if (e.key === "ArrowUp" && opts.length) {
      e.preventDefault();
      // From no selection (-1) or the first option, wrap to the last.
      sel = (sel <= 0 ? opts.length : sel) - 1;
      highlight(opts);
    } else if (e.key === "Enter") {
      // A highlighted option navigates straight to it. With no selection, Enter
      // opens the full /search/ page for the current query - the "see all results"
      // affordance that bridges the dropdown and the dedicated results page.
      if (sel >= 0 && opts[sel]) {
        window.location.href = opts[sel].href;
        return;
      }
      const q = input.value.trim();
      if (q) window.location.href = ROOT + "search/?q=" + encodeURIComponent(q);
    } else if (e.key === "Escape") {
      results.hidden = true;
      setExpanded(false);
      input.blur();
    }
  });
  document.addEventListener("click", (e) => {
    if (!results.hidden && !tools.contains(e.target as Node | null)) {
      results.hidden = true;
      setExpanded(false);
    }
  });

  // Global shortcut: "/" or Cmd/Ctrl-K focuses the search field, unless the user
  // is already typing in a field (so "/" stays typable in text inputs). Escape,
  // handled on the input above, blurs it.
  document.addEventListener("keydown", (e: KeyboardEvent) => {
    const cmdK = (e.key === "k" || e.key === "K") && (e.metaKey || e.ctrlKey);
    const slash = e.key === "/" && !e.metaKey && !e.ctrlKey && !e.altKey;
    if (!cmdK && !slash) return;
    const el = document.activeElement as HTMLElement | null;
    if (el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.isContentEditable)) return;
    e.preventDefault();
    input.focus();
    input.select();
  });

  function escapeRegExp(s: string): string {
    return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  }

  // Escape raw text for HTML, wrapping any term occurrence in <mark>. Escaping
  // happens per-segment so a term containing HTML-special chars can't break out.
  function markup(raw: string, terms: string[]): string {
    if (!terms.length) return escapeHtml(raw);
    const re = new RegExp("(" + terms.map(escapeRegExp).join("|") + ")", "gi");
    let out = "",
      last = 0,
      m: RegExpExecArray | null;
    while ((m = re.exec(raw)) !== null) {
      if (m.index === re.lastIndex) {
        re.lastIndex++;
        continue;
      }
      out += escapeHtml(raw.slice(last, m.index)) + "<mark>" + escapeHtml(m[0]) + "</mark>";
      last = m.index + m[0].length;
    }
    return out + escapeHtml(raw.slice(last));
  }

  // A one-line body excerpt centered on the first matched term (or the opening when
  // no term appears in the body, e.g. a pure tag: filter), with matches marked. The
  // text field is already cleaned at build time (fenced code stripped, whitespace
  // collapsed), so no client-side cleanup is needed here.
  function snippet(text: string, terms: string[]): string {
    if (!text) return "";
    const low = text.toLowerCase();
    let pos = -1;
    for (let i = 0; i < terms.length; i++) {
      const p = low.indexOf(terms[i]);
      if (p !== -1 && (pos === -1 || p < pos)) pos = p;
    }
    const WIN = 170;
    let start = pos === -1 ? 0 : Math.max(0, pos - 55);
    if (start > 0) {
      const sp = text.indexOf(" ", start);
      if (sp !== -1 && sp - start < 20) start = sp + 1;
    }
    return (
      (start > 0 ? "..." : "") +
      markup(text.slice(start, start + WIN), terms) +
      (start + WIN < text.length ? "..." : "")
    );
  }

  // Coarse page kind from the URL's leading segment, shown as a scannable badge.
  function typeLabel(url: string): string {
    if (url.indexOf("modules/") === 0) return "module";
    if (url.indexOf("spells/") === 0) return "spell";
    if (url.indexOf("manpage/") === 0) return "man page";
    if (url.indexOf("blog/") === 0) return "blog";
    if (url.indexOf("codes/") === 0) return "error code";
    return "doc";
  }

  // Full-page results mode for the dedicated /search/ page. Reads the query from
  // ?q=, renders every match (not the dropdown's top 8) into #search-page-results
  // as dense rows (title + type badge, url, description, a marked body snippet, and
  // clickable tag chips), and keeps the URL in sync as you type so a result set is
  // shareable and the back button steps between queries. Reuses parseQuery/runSearch.
  function initSearchPage(): void {
    const inputEl = document.getElementById("search-page-input");
    const listEl = document.getElementById("search-page-results");
    const statusEl = document.getElementById("search-page-status");
    const chips = document.getElementById("search-chips");
    const facets = document.getElementById("search-facets");
    if (!inputEl || !listEl || !statusEl) return;
    const input = inputEl as HTMLInputElement;
    const list = listEl;
    const status = statusEl;

    function syncUrl(query: string): void {
      const target = location.pathname + (query ? "?q=" + encodeURIComponent(query) : "");
      if (location.pathname + location.search !== target) history.replaceState(null, "", target);
    }

    // Datadog-style "your logic, chunked": render the token stream as chips (field
    // facets, phrases, free terms) with AND/OR/NOT connectives and bracket markers, so
    // the reader sees exactly how the query parsed. Cleared when the box is empty.
    function renderChips(tokens: Token[]): void {
      if (!chips) return;
      if (!tokens.length) {
        chips.innerHTML = "";
        chips.hidden = true;
        return;
      }
      chips.hidden = false;
      chips.innerHTML = tokens
        .map((tk) => {
          if (tk.t === "(") return '<span class="qbracket">(</span>';
          if (tk.t === ")") return '<span class="qbracket">)</span>';
          // Anything not a term is an operator (and/or/not); a positive "term" test below is
          // what narrows tk to the term variant so its field/phrase/display are readable.
          if (tk.t !== "term") return '<span class="qop">' + tk.t.toUpperCase() + "</span>";
          if (tk.field)
            return (
              '<span class="qchip qchip-field">' +
              escapeHtml(tk.field) +
              ":<b>" +
              escapeHtml(tk.display) +
              "</b></span>"
            );
          if (tk.phrase)
            return (
              '<span class="qchip qchip-phrase">&quot;' +
              escapeHtml(tk.display) +
              "&quot;</span>"
            );
          return '<span class="qchip">' + escapeHtml(tk.display) + "</span>";
        })
        .join("");
    }

    function renderPage(): void {
      const query = input.value.trim();
      syncUrl(query);
      renderChips(buildQuery(query));
      list.innerHTML = "";
      if (!query) {
        status.textContent = "Type a query, or click a tag on any page.";
        return;
      }
      if (!index) {
        status.textContent = "Loading...";
        fetchIndex(renderPage, () => {
          status.textContent = "Search index unavailable.";
        });
        return;
      }
      const res = runSearch(index, query);
      if (!res.length) {
        status.textContent = "No matches for " + query;
        return;
      }
      status.textContent = res.length + (res.length === 1 ? " result" : " results");
      const terms = getPositiveTerms(query);
      res.forEach((r) => {
        const e = r.entry;
        const li = document.createElement("li");
        let html =
          '<a class="result-main" href="' +
          ROOT +
          escapeHtml(e.url) +
          '">' +
          '<span class="result-head"><span class="search-title">' +
          markup(e.title, terms) +
          "</span>" +
          '<span class="result-type">' +
          escapeHtml(typeLabel(e.url)) +
          "</span></span>" +
          '<span class="search-url">' +
          escapeHtml(e.url) +
          "</span>";
        if (e.description)
          html += '<span class="result-desc">' + markup(e.description, terms) + "</span>";
        const snip = snippet(e.text || "", terms);
        if (snip) html += '<span class="result-snippet">' + snip + "</span>";
        html += "</a>";
        if (e.tags && e.tags.length) {
          html += '<div class="result-tags">';
          e.tags.forEach((t) => {
            html +=
              '<a class="result-tag" href="?q=' +
              encodeURIComponent('tag:"' + t + '"') +
              '">' +
              escapeHtml(t) +
              "</a>";
          });
          html += "</div>";
        }
        li.innerHTML = html;
        list.appendChild(li);
      });
    }

    // Facet autocomplete: while the caret sits in a tag:<partial> token, suggest real
    // tag values from the index. Arrow/Enter/click completes it (quoting multi-word
    // tags) and re-runs; Escape, blur, or a completed token closes it.
    let allTags: string[] | null = null,
      fsel = -1;
    function tagsList(): string[] {
      if (!allTags && index) {
        const set: Record<string, boolean> = {};
        index.forEach((e) => {
          (e.tags || []).forEach((t) => {
            set[String(t)] = true;
          });
        });
        allTags = Object.keys(set).sort();
      }
      return allTags || [];
    }
    function caretToken(): {
      field: string;
      partial: string;
      quoted: boolean;
      valStart: number;
      caret: number;
    } | null {
      const caret = input.selectionStart == null ? input.value.length : input.selectionStart;
      const m = /(?:^|[\s(])(-?)([a-z][\w.-]*):("?)([^"\s()]*)$/i.exec(input.value.slice(0, caret));
      if (!m) return null;
      const quoted = m[3] === '"';
      return {
        field: m[2].toLowerCase(),
        partial: m[4].toLowerCase(),
        quoted: quoted,
        valStart: caret - m[4].length - (quoted ? 1 : 0),
        caret: caret,
      };
    }
    function hideFacets(): void {
      if (facets) {
        facets.hidden = true;
        facets.innerHTML = "";
        fsel = -1;
      }
    }
    function updateFacets(): void {
      if (!facets) return;
      const tk = caretToken();
      if (!tk || (tk.field !== "tag" && tk.field !== "tags")) {
        hideFacets();
        return;
      }
      const p = tk.partial,
        pre: string[] = [],
        inc: string[] = [];
      tagsList().forEach((t) => {
        const i = t.toLowerCase().indexOf(p);
        if (i === 0) pre.push(t);
        else if (i > 0) inc.push(t);
      });
      const matches = pre.concat(inc).slice(0, 8);
      if (!matches.length) {
        hideFacets();
        return;
      }
      facets.innerHTML = matches
        .map((t, i) => {
          return (
            '<li role="option" id="facet-opt-' +
            i +
            '" data-tag="' +
            escapeHtml(t) +
            '"><span class="facet-key">tag:</span>' +
            escapeHtml(t) +
            "</li>"
          );
        })
        .join("");
      facets.hidden = false;
      fsel = -1;
    }
    function facetOpts(): HTMLLIElement[] {
      return facets ? Array.from(facets.querySelectorAll<HTMLLIElement>('li[role="option"]')) : [];
    }
    function facetHighlight(opts: HTMLLIElement[]): void {
      for (let i = 0; i < opts.length; i++)
        opts[i].setAttribute("aria-selected", i === fsel ? "true" : "false");
    }
    function applyFacet(tag: string): void {
      const tk = caretToken();
      if (!tk) return;
      const value = /\s/.test(tag) ? '"' + tag + '"' : tag;
      const before = input.value.slice(0, tk.valStart);
      input.value = before + value + input.value.slice(tk.caret);
      const c = (before + value).length;
      input.setSelectionRange(c, c);
      hideFacets();
      renderPage();
      input.focus();
    }
    if (facets) {
      // mousedown (not click) so it fires before the input's blur hides the list.
      facets.addEventListener("mousedown", (ev) => {
        const li = (ev.target as Element | null)?.closest("li[role=option]");
        if (!li) return;
        ev.preventDefault();
        applyFacet(li.getAttribute("data-tag") ?? "");
      });
    }
    input.addEventListener("keydown", (e: KeyboardEvent) => {
      if (!facets || facets.hidden) return;
      const opts = facetOpts();
      if (e.key === "ArrowDown") {
        e.preventDefault();
        fsel = (fsel + 1) % opts.length;
        facetHighlight(opts);
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        fsel = (fsel <= 0 ? opts.length : fsel) - 1;
        facetHighlight(opts);
      } else if (e.key === "Enter" && fsel >= 0 && opts[fsel]) {
        e.preventDefault();
        applyFacet(opts[fsel].getAttribute("data-tag") ?? "");
      } else if (e.key === "Escape") {
        e.preventDefault();
        hideFacets();
      }
    });
    input.addEventListener("blur", () => {
      setTimeout(hideFacets, 120);
    });

    // A syntax example or a per-result tag chip runs its query in place (no full
    // reload). They stay real ?q= links, so middle-click, copy, and no-JS all work.
    document.getElementById("search-page")?.addEventListener("click", (ev) => {
      const a = (ev.target as Element | null)?.closest(
        "a.syntax-example, a.result-tag",
      ) as HTMLAnchorElement | null;
      if (!a) return;
      const q = new URL(a.href).searchParams.get("q");
      if (q === null) return;
      ev.preventDefault();
      input.value = q;
      renderPage();
      input.focus();
      window.scrollTo(0, 0);
    });

    const seeded = new URLSearchParams(location.search).get("q");
    if (seeded) input.value = seeded;

    let debounce: number | undefined;
    input.addEventListener("input", () => {
      updateFacets(); // immediate: suggestions should track the caret without lag
      clearTimeout(debounce);
      debounce = setTimeout(renderPage, 80);
    });
    // Back/forward between query states re-seeds the input and re-renders.
    window.addEventListener("popstate", () => {
      input.value = new URLSearchParams(location.search).get("q") || "";
      renderPage();
    });

    // Warm the index immediately so the first query is instant; renderPage runs
    // once it lands (showing the seeded query's results, or the empty prompt).
    fetchIndex(renderPage, () => {
      status.textContent = "Search index unavailable.";
    });
    input.focus();
  }
}
