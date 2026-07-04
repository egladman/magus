// theme.js — site-wide color theme control for the magus docs site.
//
// Pico v2 follows the OS preference automatically when no data-theme attribute
// is present, so "auto" simply means: set nothing and let the system decide.
// A persisted choice (light/dark) overrides it. The early set() call runs from
// <head> before paint to avoid a flash; the toggle button (id="theme-toggle")
// is wired once the DOM is ready.
(function () {
  "use strict";
  var root = document.documentElement;

  // Flip the <html class="no-js"> marker to "js" before paint (no flash, no layout
  // shift), so CSS can gate JS-only affordances — the mobile TOC bottom-sheet, and
  // later the dead theme-toggle etc. under .no-js — without stranding no-JS users.
  root.classList.remove("no-js");
  root.classList.add("js");

  // Feather-style stroke icons (matching the search magnifier, hamburger, and TOC
  // glyphs), not unicode dingbats: a full sun for light, a crescent for dark, and a
  // half-filled circle for auto (the OS decides, so the disc is split).
  function svg(inner) {
    return '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + inner + "</svg>";
  }
  var ICON = {
    auto: svg('<circle cx="12" cy="12" r="9"></circle><path d="M12 3a9 9 0 0 0 0 18z" fill="currentColor" stroke="none"></path>'),
    light: svg('<circle cx="12" cy="12" r="4.2"></circle><line x1="12" y1="2" x2="12" y2="4"></line><line x1="12" y1="20" x2="12" y2="22"></line><line x1="4.2" y1="4.2" x2="5.6" y2="5.6"></line><line x1="18.4" y1="18.4" x2="19.8" y2="19.8"></line><line x1="2" y1="12" x2="4" y2="12"></line><line x1="20" y1="12" x2="22" y2="12"></line><line x1="4.2" y1="19.8" x2="5.6" y2="18.4"></line><line x1="18.4" y1="5.6" x2="19.8" y2="4.2"></line>'),
    dark: svg('<path d="M21 12.8A9 9 0 1 1 11.2 3 7 7 0 0 0 21 12.8z"></path>'),
  };

  function get() {
    try {
      return localStorage.getItem("theme") || "auto";
    } catch (e) {
      return "auto";
    }
  }

  function set(t) {
    if (t === "auto") {
      root.removeAttribute("data-theme");
      try { localStorage.removeItem("theme"); } catch (e) {}
    } else {
      root.setAttribute("data-theme", t);
      try { localStorage.setItem("theme", t); } catch (e) {}
    }
    var btn = document.getElementById("theme-toggle");
    if (btn) {
      btn.innerHTML = ICON[t];
      btn.setAttribute("aria-label", "Color theme: " + t);
    }
  }

  set(get()); // pre-paint, prevents a flash of the wrong theme

  document.addEventListener("DOMContentLoaded", function () {
    var btn = document.getElementById("theme-toggle");
    if (btn) {
      set(get());
      btn.addEventListener("click", function () {
        var order = ["auto", "light", "dark"];
        set(order[(order.indexOf(get()) + 1) % order.length]);
      });
    }

    document.querySelectorAll("time[datetime]").forEach(function (el) {
      var raw = el.getAttribute("datetime");
      var d = new Date(raw);
      if (isNaN(d)) return;
      var opts = { month: "short", day: "numeric", year: "numeric" };
      // A date-only value ("2026-07-01") is a calendar day, not an instant:
      // format it in UTC so a viewer west of GMT doesn't see it slip a day.
      if (raw.length === 10) opts.timeZone = "UTC";
      el.textContent = new Intl.DateTimeFormat(undefined, opts).format(d);
    });
  });
})();
