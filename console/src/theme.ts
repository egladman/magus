// theme.ts — site-wide color theme control for the magus docs + console pages.
//
// Pico v2 follows the OS preference when no data-theme attribute is present, so "auto" means: set
// nothing and let the system decide. A persisted choice (light/dark) overrides it. The early set()
// call runs from <head> before paint to avoid a flash. On the console, the Settings surface drives
// theme changes over the `magus:theme-set` CustomEvent (see the listener below).
//
// This is a classic <script src> in <head> (NOT a module - it must run before paint and set nothing
// global), so it stays an IIFE with no import/export; esbuild transpiles it to a minified script.
(function () {
  "use strict";
  type Theme = "auto" | "light" | "dark";
  const root = document.documentElement;

  // Flip the <html class="no-js"> marker to "js" before paint (no flash, no layout
  // shift), so CSS can gate JS-only affordances — the mobile TOC bottom-sheet, and
  // the settings gear (dead without JS) under .no-js — without stranding no-JS users.
  root.classList.remove("no-js");
  root.classList.add("js");

  // Platform-aware corner radius: Apple platforms (macOS / iOS / iPadOS) are opinionated toward
  // rounder corners, so tag the root and let tokens.css nudge the console's radius scale for them
  // (the docs site does not consume the --pf-t radius tokens, so this only affects the console). Set
  // pre-paint from <head> so there is no radius flash. iPadOS reports as "MacIntel"; the Mac match
  // covers it.
  try {
    const plat = (navigator.platform || navigator.userAgent || "");
    if (/Mac|iPhone|iPad|iPod/i.test(plat)) root.setAttribute("data-platform", "apple");
  } catch (e) { /* ignore */ }

  function get(): Theme {
    try {
      return (localStorage.getItem("theme") || "auto") as Theme;
    } catch (e) {
      return "auto";
    }
  }

  // Update the split theme-color metas (light + dark) so the browser chrome (tab
  // bar, address bar on Android) matches the current theme on manual override.
  // When "auto", clear both overrides and let the media-query pair decide again.
  function updateThemeColorMeta(t: Theme): void {
    const lightMeta = document.querySelector('meta[name="theme-color"][media*="light"]');
    const darkMeta = document.querySelector('meta[name="theme-color"][media*="dark"]');
    if (!lightMeta || !darkMeta) return;
    if (t === "light") {
      lightMeta.setAttribute("content", "#ffffff");
      darkMeta.setAttribute("content", "#ffffff");
    } else if (t === "dark") {
      lightMeta.setAttribute("content", "#13171f");
      darkMeta.setAttribute("content", "#13171f");
    } else {
      // "auto": restore originals so the media-query condition governs again
      lightMeta.setAttribute("content", "#ffffff");
      darkMeta.setAttribute("content", "#13171f");
    }
  }

  // PatternFly v6 dark mode is a class on <html> (pf-v6-theme-dark), NOT Pico's data-theme, so we
  // toggle it alongside (data-theme is kept for the surfaces still on Pico until the W4 cutover drops
  // it). "auto" follows the OS via prefers-color-scheme (see the matchMedia listener below, which
  // re-applies on OS change while in auto); the early set() runs from <head> before paint, so a fresh
  // load in OS-dark applies the class with no flash. Verified light + dark + the auto/light/dark cycle.
  const darkMql = typeof window !== "undefined" && window.matchMedia
    ? window.matchMedia("(prefers-color-scheme: dark)") : null;
  function applyPfTheme(t: Theme): void {
    const dark = t === "dark" || (t === "auto" && !!darkMql && darkMql.matches);
    root.classList.toggle("pf-v6-theme-dark", dark);
    // Pin the browser's own color-scheme to the RESOLVED theme so the page canvas and native controls
    // follow the chosen theme even when it differs from the OS. The <meta> declares "light dark", so
    // without this the UA keeps following the OS - picking "light" on a dark OS left the canvas dark
    // (a dark page behind light elements). In auto this tracks the OS (re-applied on OS change).
    root.style.colorScheme = dark ? "dark" : "light";
  }

  function set(t: Theme): void {
    if (t === "auto") {
      root.removeAttribute("data-theme");
      try { localStorage.removeItem("theme"); } catch (e) { /* ignore */ }
    } else {
      root.setAttribute("data-theme", t);
      try { localStorage.setItem("theme", t); } catch (e) { /* ignore */ }
    }
    applyPfTheme(t);
    updateThemeColorMeta(t);
  }

  set(get()); // pre-paint, prevents a flash of the wrong theme

  // While in auto, follow live OS theme flips so the PatternFly dark class tracks prefers-color-scheme.
  if (darkMql) {
    const onOsChange = (): void => { if (get() === "auto") applyPfTheme("auto"); };
    if (darkMql.addEventListener) darkMql.addEventListener("change", onOsChange);
    else if (darkMql.addListener) darkMql.addListener(onOsChange); // older Safari
  }

  // Theme bridge for the Settings surface. That surface cannot import this pre-paint IIFE, so it drives
  // the theme over a `magus:theme-set` CustomEvent carrying { theme, persistOnly }. Apply (persistOnly
  // false) runs set(): persist + repaint. Save (persistOnly true) mirrors only set()'s storage side, so
  // the value lands for the next load without repainting the running session.
  document.addEventListener("magus:theme-set", function (ev) {
    const detail = (ev as CustomEvent).detail || {};
    const t = detail.theme as Theme;
    if (t !== "auto" && t !== "light" && t !== "dark") return;
    if (detail.persistOnly) {
      try {
        if (t === "auto") localStorage.removeItem("theme");
        else localStorage.setItem("theme", t);
      } catch (e) { /* ignore */ }
    } else {
      set(t);
    }
  });

  document.addEventListener("DOMContentLoaded", function () {
    document.querySelectorAll("time[datetime]").forEach(function (el) {
      const raw = el.getAttribute("datetime");
      if (!raw) return;
      const d = new Date(raw);
      if (isNaN(d.getTime())) return;
      const opts: Intl.DateTimeFormatOptions = { month: "short", day: "numeric", year: "numeric" };
      // A date-only value ("2026-07-01") is a calendar day, not an instant:
      // format it in UTC so a viewer west of GMT doesn't see it slip a day.
      if (raw.length === 10) opts.timeZone = "UTC";
      el.textContent = new Intl.DateTimeFormat(undefined, opts).format(d);
    });
  });
})();
