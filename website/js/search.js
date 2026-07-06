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
(function () {
  var main = document.querySelector("main.container") || document.querySelector("main");
  if (!main) return;

  // Resolve gen/'s root from the bundle's own URL so the index fetch and result
  // links resolve at any page depth. import.meta.url is the module's own URL under
  // esbuild's --format=esm output (main.js sits at gen/'s root); pre-bundle this
  // used document.currentScript, which is null in ES modules.
  var ROOT = import.meta.url.replace(/main\.js(\?.*)?$/, "");

  // A dedicated /search/ page carries a #search-page container; when present, run
  // the full-page results mode (its own input + uncapped, URL-synced results) and
  // skip building the header dropdown so there's only one search box on that page.
  if (document.getElementById("search-page")) { initSearchPage(); return; }

  // page() renders the toolbar (with the edit link); reuse it, else create one so
  // search works on any page.
  var tools = main.querySelector(".page-tools");
  if (!tools) {
    tools = document.createElement("div");
    tools.className = "page-tools";
    main.insertBefore(tools, main.firstChild);
  }
  var edit = tools.querySelector(".page-tools-edit");

  // Detect the platform once: it drives both the visual hint badge and the
  // input's tooltip/aria label, so all three name the same modifier key.
  var platform = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || "";
  var isMac = /mac/i.test(platform);
  var modKey = isMac ? "⌘K" : "Ctrl+K";        // the compact hint badge
  var modName = isMac ? "Cmd-K" : "Ctrl-K";    // spoken/tooltip form

  var input = document.createElement("input");
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

  var results = document.createElement("ul");
  results.id = "search-results";
  results.className = "search-results";
  results.setAttribute("role", "listbox");
  results.hidden = true;

  // Polite live region announcing the result count as the query changes. The
  // combobox's aria-activedescendant announces the focused option while arrowing,
  // but a screen-reader user still needs the "N results" summary after typing.
  var live = document.createElement("div");
  live.className = "sr-only";
  live.setAttribute("role", "status");
  live.setAttribute("aria-live", "polite");

  // Wrap input + keybinding hint together so the hint can be positioned inside
  // the input's visual boundary. Screen readers skip the hint (aria-hidden).
  var wrap = document.createElement("div");
  wrap.className = "search-wrap";
  wrap.appendChild(input);

  var hint = document.createElement("span");
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
  var index = null, loading = false;
  function fetchIndex(ready, fail) {
    if (index) { ready(); return; }
    if (loading) return;
    loading = true;
    fetch(ROOT + "search-index.json")
      .then(function (r) { return r.json(); })
      .then(function (data) { index = data; loading = false; ready(); })
      .catch(function () { loading = false; if (fail) fail(); });
  }
  function loadIndex() {
    fetchIndex(
      function () { render(input.value); },
      function () {
        if (input.value.trim() && document.activeElement === input) {
          showMessage("Search index unavailable");
        }
      }
    );
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"]/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c];
    });
  }

  function wordStart(hay, i) { return i === 0 || /[^a-z0-9]/.test(hay.charAt(i - 1)); }

  // True if needle's chars appear in order within hay (typo-tolerant fallback).
  function subseq(needle, hay) {
    var j = 0;
    for (var i = 0; i < hay.length && j < needle.length; i++) {
      if (hay.charAt(i) === needle.charAt(j)) j++;
    }
    return j === needle.length;
  }

  // Lowercased fields for one record, computed once and cached (search re-scans every
  // record on every keystroke, so lowercasing per-call was pure waste). tags stays an
  // array (tag: needs whole-tag equality); tagsJoined + hay serve substring matching.
  function lc(e) {
    if (!e._lc) {
      var title = (e.title || "").toLowerCase();
      var tags = (e.tags || []).map(function (t) { return String(t).toLowerCase(); });
      var joined = tags.join(" ");
      var desc = (e.description || "").toLowerCase();
      var text = (e.text || "").toLowerCase();
      e._lc = { title: title, tags: tags, tagsJoined: joined, desc: desc, text: text,
                hay: title + " " + joined + " " + desc + " " + text };
    }
    return e._lc;
  }

  // A wildcard value ("build*", "*cache*") compiles to a regex: escape regex specials,
  // turn * into .*, anchor whole-tag matches (^...$) but leave field/free-text loose.
  function buildWild(value, field) {
    var body = value.replace(/[.*+?^${}()|[\]\\]/g, function (ch) { return ch === "*" ? ".*" : "\\" + ch; });
    var anchored = field === "tag" || field === "tags";
    return new RegExp(anchored ? "^" + body + "$" : body, "i");
  }

  // Tokenize a raw query into (, ), AND, OR, NOT, and term objects. Datadog-style:
  // uppercase AND/OR/NOT are operators, a leading - is NOT, field:value scopes a term,
  // "quoted" is a phrase (or a multi-word field value), and field:(...) distributes the
  // field over the bare terms inside via a field stack. Lenient by construction.
  function tokenize(raw) {
    var toks = [], i = 0, n = raw.length, fieldStack = [];
    function curField() { return fieldStack.length ? fieldStack[fieldStack.length - 1] : null; }
    function isSpace(c) { return c === " " || c === "\t" || c === "\n" || c === "\r"; }
    while (i < n) {
      var c = raw.charAt(i);
      if (isSpace(c)) { i++; continue; }
      if (c === "(") { toks.push({ t: "(" }); fieldStack.push(null); i++; continue; }
      if (c === ")") { toks.push({ t: ")" }); if (fieldStack.length) fieldStack.pop(); i++; continue; }
      if (c === "-" && i + 1 < n && !isSpace(raw.charAt(i + 1))) { toks.push({ t: "not" }); i++; continue; }
      // Optional field: prefix (word chars then a colon).
      var field = null, j = i;
      while (j < n && /[a-z0-9_.\-]/i.test(raw.charAt(j))) j++;
      if (j < n && raw.charAt(j) === ":" && j > i) {
        field = raw.slice(i, j).toLowerCase();
        i = j + 1;
        if (i < n && raw.charAt(i) === "(") { toks.push({ t: "(" }); fieldStack.push(field); i++; continue; }
      }
      // Value: quoted or bare (stopping at whitespace/parens).
      var value, phrase = false;
      if (i < n && raw.charAt(i) === '"') {
        var end = raw.indexOf('"', i + 1);
        if (end === -1) end = n;
        value = raw.slice(i + 1, end);
        phrase = field === null;
        i = end + 1;
      } else {
        var vs = i;
        while (i < n && !isSpace(raw.charAt(i)) && raw.charAt(i) !== "(" && raw.charAt(i) !== ")") i++;
        value = raw.slice(vs, i);
      }
      if (value === "" && field === null) continue;
      if (field === null && !phrase) {
        var up = value.toUpperCase();
        if (up === "AND") { toks.push({ t: "and" }); continue; }
        if (up === "OR") { toks.push({ t: "or" }); continue; }
        if (up === "NOT") { toks.push({ t: "not" }); continue; }
      }
      toks.push({ t: "term", field: field !== null ? field : curField(), value: value.toLowerCase(),
                  phrase: phrase, wildcard: value.indexOf("*") !== -1, display: value });
    }
    return toks;
  }

  // Recursive-descent parse into an AST. Precedence: NOT > AND (implicit or explicit) >
  // OR. Leaves precompile their wildcard regex. Tolerant of stray/unbalanced tokens.
  function parse(toks) {
    var pos = 0;
    function peek() { return toks[pos]; }
    function parseOr() {
      var kids = [parseAnd()];
      while (peek() && peek().t === "or") { pos++; kids.push(parseAnd()); }
      kids = kids.filter(Boolean);
      return kids.length === 1 ? kids[0] : (kids.length ? { op: "or", kids: kids } : null);
    }
    function parseAnd() {
      var kids = [parseNot()];
      while (peek() && peek().t !== "or" && peek().t !== ")") {
        if (peek().t === "and") { pos++; if (!peek() || peek().t === "or" || peek().t === ")") break; }
        kids.push(parseNot());
      }
      kids = kids.filter(Boolean);
      return kids.length === 1 ? kids[0] : (kids.length ? { op: "and", kids: kids } : null);
    }
    function parseNot() {
      if (peek() && peek().t === "not") { pos++; var k = parseNot(); return k ? { op: "not", kid: k } : null; }
      return parsePrimary();
    }
    function parsePrimary() {
      var tk = peek();
      if (!tk) return null;
      if (tk.t === "(") { pos++; var inner = parseOr(); if (peek() && peek().t === ")") pos++; return inner; }
      if (tk.t === "term") {
        pos++;
        return { op: "term", field: tk.field, value: tk.value, phrase: tk.phrase,
                 wildcard: tk.wildcard, re: tk.wildcard ? buildWild(tk.value, tk.field) : null };
      }
      pos++; // stray ) / operator — skip and continue
      return parsePrimary();
    }
    return parseOr();
  }

  // Build the parsed query once: token stream (drives the chip preview) + AST.
  function buildQuery(raw) { var tokens = tokenize(raw); return { tokens: tokens, ast: parse(tokens) }; }

  // Does one term leaf match a record? tag = whole-tag equality (or regex); other fields
  // are substring on that field; a free-text term matches the combined haystack.
  function matchLeaf(leaf, e) {
    var L = lc(e);
    if (leaf.field === "tag" || leaf.field === "tags") {
      for (var i = 0; i < L.tags.length; i++) {
        if (leaf.wildcard ? leaf.re.test(L.tags[i]) : L.tags[i] === leaf.value) return true;
      }
      return false;
    }
    var hay = leaf.field === "title" ? L.title
      : (leaf.field === "description" || leaf.field === "desc") ? L.desc
      : (leaf.field === "text" || leaf.field === "body") ? L.text
      : L.hay;
    return leaf.wildcard ? leaf.re.test(hay) : hay.indexOf(leaf.value) !== -1;
  }

  // Boolean inclusion: evaluate the AST against a record. Missing node = matches all.
  function evalNode(node, e) {
    if (!node) return true;
    if (node.op === "term") return matchLeaf(node, e);
    if (node.op === "not") return !evalNode(node.kid, e);
    if (node.op === "and") { for (var i = 0; i < node.kids.length; i++) if (!evalNode(node.kids[i], e)) return false; return true; }
    if (node.op === "or") { for (var j = 0; j < node.kids.length; j++) if (evalNode(node.kids[j], e)) return true; return false; }
    return true;
  }

  // Relevance weight of one positive term leaf (field-weighted, mirroring the old
  // scoring: title > tags > description > body, with a word-start / title-start bonus).
  function scoreLeaf(leaf, e) {
    var L = lc(e), v = leaf.value;
    function hit(hay, base, ws) {
      if (leaf.wildcard) return leaf.re.test(hay) ? base : 0;
      var idx = hay.indexOf(v);
      return idx === -1 ? 0 : base + (wordStart(hay, idx) ? ws : 0);
    }
    if (leaf.field === "tag" || leaf.field === "tags") return matchLeaf(leaf, e) ? 8 : 0;
    if (leaf.field === "title") { var s = hit(L.title, 10, 4); if (!leaf.wildcard && L.title.indexOf(v) === 0) s += 4; return s; }
    if (leaf.field === "description" || leaf.field === "desc") return hit(L.desc, 4, 2);
    if (leaf.field === "text" || leaf.field === "body") return hit(L.text, 2, 1);
    var free = hit(L.title, 10, 4) + hit(L.tagsJoined, 8, 3) + hit(L.desc, 4, 2) + hit(L.text, 2, 1);
    if (!leaf.wildcard && L.title.indexOf(v) === 0) free += 4;
    return free;
  }

  // Rank = sum of positively-reachable leaf weights (an even number of NOTs above it),
  // so OR/NOT decide inclusion while the positive hits that landed decide ordering.
  function scoreAst(node, e, neg) {
    if (!node) return 0;
    if (node.op === "term") return neg ? 0 : scoreLeaf(node, e);
    if (node.op === "not") return scoreAst(node.kid, e, !neg);
    if (node.op === "and" || node.op === "or") {
      var s = 0; for (var i = 0; i < node.kids.length; i++) s += scoreAst(node.kids[i], e, neg); return s;
    }
    return 0;
  }

  // Positive (non-negated) leaf values, for snippet/description highlighting.
  function positiveTerms(node, neg, acc) {
    acc = acc || [];
    if (!node) return acc;
    if (node.op === "term") { if (!neg && node.value) acc.push(node.value); return acc; }
    if (node.op === "not") return positiveTerms(node.kid, !neg, acc);
    if (node.kids) node.kids.forEach(function (k) { positiveTerms(k, neg, acc); });
    return acc;
  }

  function runSearch(raw) {
    var q = buildQuery(raw);
    // Need at least one positive term (a bare -exclusion or pure operators match nothing,
    // matching the old behaviour and avoiding a stray "-" dumping the whole corpus).
    var pos = positiveTerms(q.ast, false);
    if (!pos.length) return [];
    var out = [];
    for (var i = 0; i < index.length; i++) {
      if (evalNode(q.ast, index[i])) out.push({ e: index[i], s: scoreAst(q.ast, index[i], false) });
    }
    // Typo-tolerant fallback: only when the query is a plain AND of bare words that found
    // nothing (no fields/operators/parens), rescue near-misses via subsequence on titles.
    var simple = q.tokens.length > 0 && q.tokens.every(function (t) { return t.t === "term" && t.field === null && !t.phrase && !t.wildcard; });
    if (!out.length && simple) {
      index.forEach(function (e) {
        var ok = true, sc = 0;
        for (var k = 0; k < pos.length; k++) { if (subseq(pos[k], lc(e).title)) sc += 2; else { ok = false; break; } }
        if (ok) out.push({ e: e, s: sc });
      });
    }
    out.sort(function (a, b) { return b.s - a.s || a.e.title.length - b.e.title.length; });
    return out;
  }

  // sel indexes the arrow-key-highlighted option within the rendered results
  // (-1 = none). highlight() reflects it into the DOM/ARIA; render() resets it.
  var sel = -1;
  function options() { return results.querySelectorAll('a[role="option"]'); }
  function highlight(opts) {
    for (var i = 0; i < opts.length; i++) {
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

  function setExpanded(open) {
    input.setAttribute("aria-expanded", open ? "true" : "false");
    if (!open) input.removeAttribute("aria-activedescendant");
  }

  // showMessage replaces the dropdown with a single status line and opens it, so
  // the user always gets feedback (no matches, or an index that failed to load).
  function showMessage(text) {
    results.innerHTML = "";
    sel = -1;
    var li = document.createElement("li");
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
  function footerLi(query, count) {
    var li = document.createElement("li");
    li.className = "search-footer";
    li.setAttribute("role", "presentation");
    var label = count > 0 ? "See all " + count + " result" + (count === 1 ? "" : "s") : "Open full search";
    li.innerHTML =
      '<a class="search-seeall" href="' + ROOT + "search/?q=" + encodeURIComponent(query) + '">' + label + ' <span class="xref-arrow" aria-hidden="true">&rarr;</span></a>' +
      '<span class="search-synhint">Syntax: <code>tag:name</code> <code>-term</code> <code>&quot;phrase&quot;</code></span>';
    return li;
  }

  function render(raw) {
    results.innerHTML = "";
    sel = -1;
    var query = (raw || "").trim();
    if (!query) { results.hidden = true; setExpanded(false); live.textContent = ""; return; }
    if (!index) { loadIndex(); results.hidden = true; setExpanded(false); return; } // re-renders once loaded
    results.hidden = false;
    setExpanded(true);

    var res = runSearch(query);
    if (!res.length) {
      var empty = document.createElement("li");
      empty.className = "search-empty";
      empty.textContent = "No matches";
      results.appendChild(empty);
      results.appendChild(footerLi(query, 0));
      live.textContent = "No matches";
      return;
    }
    var shown = Math.min(res.length, 8);
    live.textContent = res.length + (res.length === 1 ? " result" : " results");
    res.slice(0, 8).forEach(function (r, i) {
      var li = document.createElement("li");
      li.setAttribute("role", "presentation"); // the <a> is the listbox option, not the <li>
      var a = document.createElement("a");
      a.href = ROOT + r.e.url;
      a.id = "search-opt-" + i;
      a.setAttribute("role", "option");
      a.innerHTML =
        '<span class="search-title">' + escapeHtml(r.e.title) + "</span>" +
        '<span class="search-url">' + escapeHtml(r.e.url) + "</span>";
      li.appendChild(a);
      results.appendChild(li);
    });
    // Preview shows the top 8; the footer names the true total and links to the
    // dedicated page, so "dropdown = quick preview, page = everything" reads clearly.
    results.appendChild(footerLi(query, res.length));
  }

  input.addEventListener("focus", function () { loadIndex(); if (input.value.trim()) render(input.value); });
  // Debounce typing so a burst of keystrokes coalesces into one index scan +
  // DOM rebuild instead of one per character. Focus/arrow paths stay immediate.
  var debounce = null;
  input.addEventListener("input", function () {
    clearTimeout(debounce);
    debounce = setTimeout(function () { render(input.value); }, 80);
  });
  input.addEventListener("keydown", function (e) {
    var opts = options();
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
      if (sel >= 0 && opts[sel]) { window.location.href = opts[sel].href; return; }
      var q = input.value.trim();
      if (q) window.location.href = ROOT + "search/?q=" + encodeURIComponent(q);
    } else if (e.key === "Escape") {
      results.hidden = true;
      setExpanded(false);
      input.blur();
    }
  });
  document.addEventListener("click", function (e) {
    if (!results.hidden && !tools.contains(e.target)) { results.hidden = true; setExpanded(false); }
  });

  // Global shortcut: "/" or Cmd/Ctrl-K focuses the search field, unless the user
  // is already typing in a field (so "/" stays typable in text inputs). Escape,
  // handled on the input above, blurs it.
  document.addEventListener("keydown", function (e) {
    var cmdK = (e.key === "k" || e.key === "K") && (e.metaKey || e.ctrlKey);
    var slash = e.key === "/" && !e.metaKey && !e.ctrlKey && !e.altKey;
    if (!cmdK && !slash) return;
    var el = document.activeElement;
    if (el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.isContentEditable)) return;
    e.preventDefault();
    input.focus();
    input.select();
  });

  function escapeRegExp(s) { return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"); }

  // Escape raw text for HTML, wrapping any term occurrence in <mark>. Escaping
  // happens per-segment so a term containing HTML-special chars can't break out.
  function markup(raw, terms) {
    if (!terms.length) return escapeHtml(raw);
    var re = new RegExp("(" + terms.map(escapeRegExp).join("|") + ")", "gi");
    var out = "", last = 0, m;
    while ((m = re.exec(raw)) !== null) {
      if (m.index === re.lastIndex) { re.lastIndex++; continue; }
      out += escapeHtml(raw.slice(last, m.index)) + "<mark>" + escapeHtml(m[0]) + "</mark>";
      last = m.index + m[0].length;
    }
    return out + escapeHtml(raw.slice(last));
  }

  // A one-line body excerpt centered on the first matched term (or the opening when
  // no term appears in the body, e.g. a pure tag: filter), with matches marked. The
  // text field is already cleaned at build time (fenced code stripped, whitespace
  // collapsed), so no client-side cleanup is needed here.
  function snippet(text, terms) {
    if (!text) return "";
    var low = text.toLowerCase(), pos = -1;
    for (var i = 0; i < terms.length; i++) {
      var p = low.indexOf(terms[i]);
      if (p !== -1 && (pos === -1 || p < pos)) pos = p;
    }
    var WIN = 170, start = pos === -1 ? 0 : Math.max(0, pos - 55);
    if (start > 0) { var sp = text.indexOf(" ", start); if (sp !== -1 && sp - start < 20) start = sp + 1; }
    return (start > 0 ? "..." : "") + markup(text.slice(start, start + WIN), terms) +
      (start + WIN < text.length ? "..." : "");
  }

  // Coarse page kind from the URL's leading segment, shown as a scannable badge.
  function typeLabel(url) {
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
  function initSearchPage() {
    var input = document.getElementById("search-page-input");
    var list = document.getElementById("search-page-results");
    var status = document.getElementById("search-page-status");
    var chips = document.getElementById("search-chips");
    var facets = document.getElementById("search-facets");
    if (!input || !list) return;

    function syncUrl(query) {
      var target = location.pathname + (query ? "?q=" + encodeURIComponent(query) : "");
      if (location.pathname + location.search !== target) history.replaceState(null, "", target);
    }

    // Datadog-style "your logic, chunked": render the token stream as chips (field
    // facets, phrases, free terms) with AND/OR/NOT connectives and bracket markers, so
    // the reader sees exactly how the query parsed. Cleared when the box is empty.
    function renderChips(tokens) {
      if (!chips) return;
      if (!tokens.length) { chips.innerHTML = ""; chips.hidden = true; return; }
      chips.hidden = false;
      chips.innerHTML = tokens.map(function (tk) {
        if (tk.t === "(") return '<span class="qbracket">(</span>';
        if (tk.t === ")") return '<span class="qbracket">)</span>';
        if (tk.t === "and" || tk.t === "or" || tk.t === "not") return '<span class="qop">' + tk.t.toUpperCase() + "</span>";
        if (tk.field) return '<span class="qchip qchip-field">' + escapeHtml(tk.field) + ":<b>" + escapeHtml(tk.display) + "</b></span>";
        if (tk.phrase) return '<span class="qchip qchip-phrase">&quot;' + escapeHtml(tk.display) + "&quot;</span>";
        return '<span class="qchip">' + escapeHtml(tk.display) + "</span>";
      }).join("");
    }

    function renderPage() {
      var query = input.value.trim();
      syncUrl(query);
      renderChips(buildQuery(query).tokens);
      list.innerHTML = "";
      if (!query) { status.textContent = "Type a query, or click a tag on any page."; return; }
      if (!index) {
        status.textContent = "Loading...";
        fetchIndex(renderPage, function () { status.textContent = "Search index unavailable."; });
        return;
      }
      var res = runSearch(query);
      if (!res.length) { status.textContent = "No matches for " + query; return; }
      status.textContent = res.length + (res.length === 1 ? " result" : " results");
      var terms = positiveTerms(buildQuery(query).ast, false);
      res.forEach(function (r) {
        var e = r.e;
        var li = document.createElement("li");
        var html =
          '<a class="result-main" href="' + ROOT + escapeHtml(e.url) + '">' +
            '<span class="result-head"><span class="search-title">' + markup(e.title, terms) + "</span>" +
            '<span class="result-type">' + escapeHtml(typeLabel(e.url)) + "</span></span>" +
            '<span class="search-url">' + escapeHtml(e.url) + "</span>";
        if (e.description) html += '<span class="result-desc">' + markup(e.description, terms) + "</span>";
        var snip = snippet(e.text || "", terms);
        if (snip) html += '<span class="result-snippet">' + snip + "</span>";
        html += "</a>";
        if (e.tags && e.tags.length) {
          html += '<div class="result-tags">';
          e.tags.forEach(function (t) {
            html += '<a class="result-tag" href="?q=' + encodeURIComponent('tag:"' + t + '"') + '">' + escapeHtml(t) + "</a>";
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
    var allTags = null, fsel = -1;
    function tagsList() {
      if (!allTags && index) {
        var set = {};
        index.forEach(function (e) { (e.tags || []).forEach(function (t) { set[String(t)] = true; }); });
        allTags = Object.keys(set).sort();
      }
      return allTags || [];
    }
    function caretToken() {
      var caret = input.selectionStart == null ? input.value.length : input.selectionStart;
      var m = /(?:^|[\s(])(-?)([a-z][\w.-]*):("?)([^"\s()]*)$/i.exec(input.value.slice(0, caret));
      if (!m) return null;
      var quoted = m[3] === '"';
      return { field: m[2].toLowerCase(), partial: m[4].toLowerCase(), quoted: quoted,
               valStart: caret - m[4].length - (quoted ? 1 : 0), caret: caret };
    }
    function hideFacets() { if (facets) { facets.hidden = true; facets.innerHTML = ""; fsel = -1; } }
    function updateFacets() {
      if (!facets) return;
      var tk = caretToken();
      if (!tk || (tk.field !== "tag" && tk.field !== "tags")) { hideFacets(); return; }
      var p = tk.partial, pre = [], inc = [];
      tagsList().forEach(function (t) { var i = t.toLowerCase().indexOf(p); if (i === 0) pre.push(t); else if (i > 0) inc.push(t); });
      var matches = pre.concat(inc).slice(0, 8);
      if (!matches.length) { hideFacets(); return; }
      facets.innerHTML = matches.map(function (t, i) {
        return '<li role="option" id="facet-opt-' + i + '" data-tag="' + escapeHtml(t) + '"><span class="facet-key">tag:</span>' + escapeHtml(t) + "</li>";
      }).join("");
      facets.hidden = false;
      fsel = -1;
    }
    function facetOpts() { return facets ? facets.querySelectorAll('li[role="option"]') : []; }
    function facetHighlight(opts) { for (var i = 0; i < opts.length; i++) opts[i].setAttribute("aria-selected", i === fsel ? "true" : "false"); }
    function applyFacet(tag) {
      var tk = caretToken(); if (!tk) return;
      var value = /\s/.test(tag) ? '"' + tag + '"' : tag;
      var before = input.value.slice(0, tk.valStart);
      input.value = before + value + input.value.slice(tk.caret);
      var c = (before + value).length;
      input.setSelectionRange(c, c);
      hideFacets();
      renderPage();
      input.focus();
    }
    if (facets) {
      // mousedown (not click) so it fires before the input's blur hides the list.
      facets.addEventListener("mousedown", function (ev) {
        var li = ev.target.closest("li[role=option]"); if (!li) return;
        ev.preventDefault(); applyFacet(li.getAttribute("data-tag"));
      });
    }
    input.addEventListener("keydown", function (e) {
      if (!facets || facets.hidden) return;
      var opts = facetOpts();
      if (e.key === "ArrowDown") { e.preventDefault(); fsel = (fsel + 1) % opts.length; facetHighlight(opts); }
      else if (e.key === "ArrowUp") { e.preventDefault(); fsel = (fsel <= 0 ? opts.length : fsel) - 1; facetHighlight(opts); }
      else if (e.key === "Enter" && fsel >= 0 && opts[fsel]) { e.preventDefault(); applyFacet(opts[fsel].getAttribute("data-tag")); }
      else if (e.key === "Escape") { e.preventDefault(); hideFacets(); }
    });
    input.addEventListener("blur", function () { setTimeout(hideFacets, 120); });

    // A syntax example or a per-result tag chip runs its query in place (no full
    // reload). They stay real ?q= links, so middle-click, copy, and no-JS all work.
    document.getElementById("search-page").addEventListener("click", function (ev) {
      var a = ev.target.closest("a.syntax-example, a.result-tag");
      if (!a) return;
      var q = new URL(a.href).searchParams.get("q");
      if (q === null) return;
      ev.preventDefault();
      input.value = q;
      renderPage();
      input.focus();
      window.scrollTo(0, 0);
    });

    var seeded = new URLSearchParams(location.search).get("q");
    if (seeded) input.value = seeded;

    var debounce = null;
    input.addEventListener("input", function () {
      updateFacets(); // immediate: suggestions should track the caret without lag
      clearTimeout(debounce);
      debounce = setTimeout(renderPage, 80);
    });
    // Back/forward between query states re-seeds the input and re-renders.
    window.addEventListener("popstate", function () {
      input.value = new URLSearchParams(location.search).get("q") || "";
      renderPage();
    });

    // Warm the index immediately so the first query is instant; renderPage runs
    // once it lands (showing the seeded query's results, or the empty prompt).
    fetchIndex(renderPage, function () { status.textContent = "Search index unavailable."; });
    input.focus();
  }
})();
