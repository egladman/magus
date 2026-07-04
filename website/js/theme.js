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

  var ICON = { auto: "◐", light: "☀", dark: "☾" };

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
      btn.textContent = ICON[t];
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
