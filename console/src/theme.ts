// theme.ts — site-wide color theme control for the magus docs + console pages.
//
// Pico v2 follows the OS preference automatically when no data-theme attribute
// is present, so "auto" simply means: set nothing and let the system decide.
// A persisted choice (light/dark) overrides it. The early set() call runs from
// <head> before paint to avoid a flash. The theme control lives in the gear
// dropdown (rendered by renderSettingsPanel) as a single click-to-cycle button
// (id="theme-cycle") that steps auto -> light -> dark: one control on every page,
// no separate top-bar button and no dropdown-within-a-dropdown. The button is
// seeded and wired once the DOM is ready; console-settings.js owns opening/closing
// the panel it sits in, and a click on the button cycles without closing it.
//
// This is a classic <script src> in <head> (NOT a module - it must run before paint
// and set nothing global), so it stays an IIFE with no import/export; esbuild transpiles
// it to a minified script, unlike the deferred main.js bundle.
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

  // Feather-style stroke icons (matching the search magnifier, hamburger, and TOC
  // glyphs): a full sun for light, a crescent for dark, and a half-filled circle for
  // auto (the OS decides, so the disc is split). Sized in em via CSS so they track the
  // button label.
  function svg(inner: string): string {
    return '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + inner + "</svg>";
  }
  const ICON: Record<Theme, string> = {
    auto: svg('<circle cx="12" cy="12" r="9"></circle><path d="M12 3a9 9 0 0 0 0 18z" fill="currentColor" stroke="none"></path>'),
    light: svg('<circle cx="12" cy="12" r="4.2"></circle><line x1="12" y1="2" x2="12" y2="4"></line><line x1="12" y1="20" x2="12" y2="22"></line><line x1="4.2" y1="4.2" x2="5.6" y2="5.6"></line><line x1="18.4" y1="18.4" x2="19.8" y2="19.8"></line><line x1="2" y1="12" x2="4" y2="12"></line><line x1="20" y1="12" x2="22" y2="12"></line><line x1="4.2" y1="19.8" x2="5.6" y2="18.4"></line><line x1="18.4" y1="5.6" x2="19.8" y2="4.2"></line>'),
    dark: svg('<path d="M21 12.8A9 9 0 1 1 11.2 3 7 7 0 0 0 21 12.8z"></path>'),
  };
  const LABEL: Record<Theme, string> = { auto: "Auto", light: "Light", dark: "Dark" };

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
    // Reflect the current mode on the gear's cycle button (it may not exist yet at
    // pre-paint, or on pages without the panel — the guard covers both).
    const btn = document.getElementById("theme-cycle");
    if (btn) {
      const iconEl = btn.querySelector(".console-shell-themecycle__icon");
      const labelEl = btn.querySelector(".console-shell-themecycle__label");
      if (iconEl) iconEl.innerHTML = ICON[t];
      if (labelEl) labelEl.textContent = LABEL[t];
      btn.setAttribute("aria-label", "Color theme: " + LABEL[t] + " (click to cycle)");
    }
  }

  set(get()); // pre-paint, prevents a flash of the wrong theme

  // While in auto, follow live OS theme flips so the PatternFly dark class tracks prefers-color-scheme.
  if (darkMql) {
    const onOsChange = (): void => { if (get() === "auto") applyPfTheme("auto"); };
    if (darkMql.addEventListener) darkMql.addEventListener("change", onOsChange);
    else if (darkMql.addListener) darkMql.addListener(onOsChange); // older Safari
  }

  document.addEventListener("DOMContentLoaded", function () {
    const btn = document.getElementById("theme-cycle");
    if (btn) {
      set(get());
      btn.addEventListener("click", function () {
        const order: Theme[] = ["auto", "light", "dark"];
        set(order[(order.indexOf(get()) + 1) % order.length]);
      });
    }

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
