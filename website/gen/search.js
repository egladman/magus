// Site search. An always-visible search field in the .page-tools toolbar (beside
// the TOC toggle); it carries its own magnifier, so there's no separate button.
// Matches gen/search-index.json (a flat {url, title, text, tags, description}
// record per page, built at render time, fetched lazily on first focus) and shows
// ranked results in a dropdown under the toolbar. No-ops if there's no <main>.
//
// Query syntax: space-separated terms are AND-ed; "quoted phrases" must appear
// contiguously; a -term excludes pages containing it. Matching is substring with
// relevance ranking: title hits weigh highest, then curated frontmatter tags,
// then the page description, then body text (word-start hits add a bonus at each
// level). If nothing matches exactly, a typo-tolerant subsequence pass on titles
// rescues near-misses.
(function () {
  var main = document.querySelector("main.container") || document.querySelector("main");
  if (!main) return;

  // Resolve gen/'s root from this script's own URL so the index fetch and result
  // links resolve at any page depth.
  var ROOT = "";
  if (document.currentScript && document.currentScript.src) {
    ROOT = document.currentScript.src.replace(/search\.js(\?.*)?$/, "");
  }

  // page() renders the toolbar (with the edit link); reuse it, else create one so
  // search works on any page.
  var tools = main.querySelector(".page-tools");
  if (!tools) {
    tools = document.createElement("div");
    tools.className = "page-tools";
    main.insertBefore(tools, main.firstChild);
  }
  var edit = tools.querySelector(".page-tools-edit");

  var input = document.createElement("input");
  input.type = "text"; // not type=search: avoids Pico's pill shape; the magnifier is a CSS bg
  input.className = "search-input";
  input.placeholder = "Search docs...";
  input.autocomplete = "off";
  input.setAttribute("aria-label", "Search the documentation");
  input.title = "Press / or Cmd-K to search";
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

  if (edit) tools.insertBefore(input, edit);
  else tools.appendChild(input);
  tools.appendChild(results);

  var index = null, loading = false;
  function loadIndex() {
    if (index || loading) return;
    loading = true;
    fetch(ROOT + "search-index.json")
      .then(function (r) { return r.json(); })
      .then(function (data) { index = data; loading = false; render(input.value); })
      // Leave index null (not []) on failure so a later focus/input retries
      // rather than wedging search until a full page reload; surface a status
      // line meanwhile so a typed query is not met with a silently empty box.
      .catch(function () {
        loading = false;
        if (input.value.trim() && document.activeElement === input) {
          showMessage("Search index unavailable");
        }
      });
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"]/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c];
    });
  }

  // Split a raw query into "phrases", -negatives, and positive terms.
  function parseQuery(raw) {
    var positives = [], negatives = [], phrases = [], re = /"([^"]+)"|(\S+)/g, m;
    while ((m = re.exec(raw)) !== null) {
      if (m[1] !== undefined) {
        var p = m[1].trim().toLowerCase();
        if (p) phrases.push(p);
      } else {
        var t = m[2];
        if (t.charAt(0) === "-" && t.length > 1) negatives.push(t.slice(1).toLowerCase());
        else positives.push(t.toLowerCase());
      }
    }
    return { positives: positives, negatives: negatives, phrases: phrases };
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

  // Score one page against the query; -1 means "excluded / doesn't match".
  function scoreEntry(e, q, fuzzy) {
    var title = (e.title || "").toLowerCase();
    var tags = (e.tags || []).join(" ").toLowerCase();
    var desc = (e.description || "").toLowerCase();
    var text = (e.text || "").toLowerCase();
    var hay = title + " " + tags + " " + desc + " " + text;

    for (var n = 0; n < q.negatives.length; n++) {
      if (hay.indexOf(q.negatives[n]) !== -1) return -1; // exclude
    }
    for (var p = 0; p < q.phrases.length; p++) {
      if (hay.indexOf(q.phrases[p]) === -1) return -1; // phrase required
    }

    var score = 0;
    for (var t = 0; t < q.positives.length; t++) {
      var term = q.positives[t];
      var it = title.indexOf(term), ig = tags.indexOf(term),
          id = desc.indexOf(term), ix = text.indexOf(term);
      if (it === -1 && ig === -1 && id === -1 && ix === -1) {
        if (fuzzy && subseq(term, title)) { score += 2; continue; }
        return -1; // a required term is absent
      }
      // Curated tags rank just under the title and above body prose, so a tag can
      // surface a page under a term its text never mentions.
      if (it !== -1) { score += 10; if (wordStart(title, it)) score += 4; if (it === 0) score += 4; }
      if (ig !== -1) { score += 8;  if (wordStart(tags, ig)) score += 3; }
      if (id !== -1) { score += 4;  if (wordStart(desc, id)) score += 2; }
      if (ix !== -1) { score += 2;  if (wordStart(text, ix)) score += 1; }
    }
    for (var ph = 0; ph < q.phrases.length; ph++) {
      score += title.indexOf(q.phrases[ph]) !== -1 ? 12 : 4;
    }
    return score;
  }

  function runSearch(raw) {
    var q = parseQuery(raw);
    if (!q.positives.length && !q.phrases.length) return [];
    function pass(fuzzy) {
      var out = [];
      index.forEach(function (e) {
        var s = scoreEntry(e, q, fuzzy);
        if (s >= 0) out.push({ e: e, s: s });
      });
      // Higher score first; break ties toward shorter (more specific) titles.
      out.sort(function (a, b) { return b.s - a.s || a.e.title.length - b.e.title.length; });
      return out;
    }
    var res = pass(false);
    if (!res.length) res = pass(true); // typo-tolerant only when nothing matched exactly
    return res;
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
  }

  function render(raw) {
    results.innerHTML = "";
    sel = -1;
    var query = (raw || "").trim();
    if (!query) { results.hidden = true; setExpanded(false); return; }
    if (!index) { loadIndex(); results.hidden = true; setExpanded(false); return; } // re-renders once loaded
    results.hidden = false;
    setExpanded(true);

    var res = runSearch(query);
    if (!res.length) { showMessage("No matches"); return; }
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
      // The highlighted option, or the top result when none is highlighted.
      var target = (sel >= 0 && opts[sel]) ? opts[sel] : opts[0];
      if (target) window.location.href = target.href;
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
})();
