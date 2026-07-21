// home.ts - the console's launcher. It is NOT a tab: the console renders it as the outlet's empty
// state (main.ts) whenever the workspace has zero open tabs (fresh load, or after the last tab is
// closed). Clicking a card opens that surface as a real tab; with a tab open, the command bar
// ("Open ...") is how another surface is launched. This module just builds the launcher DOM - a
// heading, a lede, and a PatternFly Gallery of clickable Cards - and leaves mounting to the console.
//
// A plain click on a card opens that surface as a tab. Each card also carries a top-right kebab menu
// whose one item, "Open in a new window", spawns a dedicated OS/PWA window for that surface
// (openSurfaceWindow) - an EXPLICIT opt-in, never the plain-click default, so a card can still never
// strand you in a window you did not ask for.
import { openSurfaceWindow } from "../lib/appwindow";

// A surface the launcher can open: the pageId the console registered it under, and a human label.
export interface Launchable {
  pageId: string;
  label: string;
  hint: string;
}

// The launcher lede rotates a small time-of-day tagline each fresh load - a quiet sign of polish, not a
// slogan. Each entry is dry and tool-flavored (magus is a build tool; the daemon keeps the graph warm),
// understated to match the earthy identity. `at` gates an entry to an hour window (start inclusive, end
// exclusive; a wrapped window like night spans midnight); `ANY_HOURS` entries are always eligible. The
// original "See what magus is up to." stays in the pool so nothing is lost. Plain ASCII throughout.
const ANY_HOURS: [number, number] = [0, 24];
interface Tagline {
  text: string;
  at: [number, number];
}
const TAGLINES: Tagline[] = [
  { text: "See what magus is up to.", at: ANY_HOURS },
  { text: "Cache warm, spells ready.", at: ANY_HOURS },
  { text: "The graph is warm.", at: ANY_HOURS },
  { text: "The forge is warming up.", at: [5, 11] },
  { text: "Fresh build, fresh coffee.", at: [5, 11] },
  { text: "Morning. What are we building?", at: [5, 11] },
  { text: "Deep in the afternoon build.", at: [11, 17] },
  { text: "Plenty of daylight left to ship.", at: [11, 17] },
  { text: "Evening. One more target?", at: [17, 22] },
  { text: "Winding down the day's builds.", at: [17, 22] },
  { text: "Burning the midnight build.", at: [22, 5] },
  { text: "The daemon never sleeps.", at: [22, 5] },
];

// inWindow reports whether `hour` sits in a [start, end) window, handling a window that wraps past
// midnight (start > end, e.g. 22..5).
function inWindow(hour: number, [start, end]: [number, number]): boolean {
  return start <= end ? hour >= start && hour < end : hour >= start || hour < end;
}

// launcherTagline picks a tagline eligible for the given hour, at random. `pick` is injected only so the
// choice is testable; it defaults to Math.random. Always non-empty (the ANY_HOURS entries are eligible
// at every hour), so it never falls back to a placeholder.
export function launcherTagline(now: Date = new Date(), pick: () => number = Math.random): string {
  const hour = now.getHours();
  const eligible = TAGLINES.filter((t) => inWindow(hour, t.at));
  return eligible[Math.floor(pick() * eligible.length)].text;
}

// Each launcher card carries its OWN earthy palette hue (--card-accent, set per card below): the small
// icon takes it (a pop of color per tool). Decorative only - the functional UI keeps PatternFly's brand
// accent, and semantic status color stays reserved for health.
const SURFACE_ACCENTS: Record<string, string> = {
  dashboard: "--console-moss", // green: live/healthy status
  activity: "--console-rust", // terracotta: warm history trail
  logs: "--console-indigo", // restrained indigo: cool, reading captured output
  graph: "--console-slate", // steel blue: nodes and connections
  actions: "--console-gold", // ochre yellow: energy (the lightning bolt)
  settings: "--console-stone", // neutral gray: utility
};

// One representative glyph per surface, drawn in the console's shared icon idiom (24x24, stroked
// currentColor, round caps). It is used for BOTH the small tinted-tile icon and the large corner
// watermark, so a card's two marks match. Keyed by pageId; a surface with no entry falls back to a
// neutral square. A single inner element per animated icon carries data-motion="<kind>": on card hover
// the SMALL icon plays ONE in-character micro-motion (gear turns, gauge needle sweeps, node pulses,
// waveform breathes, bolt flickers) - see the @keyframes in console.css, all one-shot and reduced-
// motion gated; the watermark reuses the same markup but never animates (the motion CSS is icon-scoped).
const SURFACE_ICONS: Record<string, string> = {
  // Log viewer: stacked text lines.
  logs: '<path d="M4 5h16M4 10h10M4 15h13M4 19h7"/>',
  // Graph explorer: three connected nodes; the lead node pulses on hover.
  graph: '<circle data-motion="pulse" cx="6" cy="7" r="2.2"/><circle cx="18" cy="6" r="2.2"/><circle cx="15" cy="18" r="2.2"/><path d="M8 8l6 9M8 7l8-1"/>',
  // Dashboard: a small bar chart on a baseline (live stats); the tall bar grows on hover.
  dashboard: '<path d="M3 21h18"/><rect x="5" y="11" width="4" height="8" rx="1"/><rect data-motion="bars" x="10" y="6" width="4" height="13" rx="1"/><rect x="15" y="14" width="4" height="5" rx="1"/>',
  // Activity: a waveform; it breathes on hover.
  activity: '<path data-motion="wave" d="M3 12h3l2-5 3 10 3-8 2 3h5"/>',
  // Actions: a lightning bolt; it flickers on hover.
  actions: '<path data-motion="bolt" d="M13 2L4 14h6l-1 8 9-12h-6z"/>',
  // Settings: a proper cog (not the sun-like spoked glyph); the whole icon turns on hover.
  settings: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>',
};

// buildLauncher builds the launcher DOM as the outlet's empty state. `surfaces` is what it offers to
// open; `open` asks the console to open one as a tab. The returned element carries data-surface="home"
// (its heading/lede layout is ID-scoped in console.css) and is appended straight into
// #console-outlet-content as a sibling of the tab panes, shown only when no tab is active.
export function buildLauncher(surfaces: Launchable[], open: (pageId: string) => void, launchDemo: () => void): HTMLElement {
  // data-surface tags the empty state; its heading/lede layout is ID-scoped in console.css. The
  // launcher is a PatternFly Gallery of clickable Cards - the [data-open] hook the click handler keys
  // on rides on each card, and the whole card is the keyboard-reachable target (tabindex + Enter/Space).
  const root = document.createElement("div");
  root.dataset.surface = "home";

  const title = document.createElement("h1");
  title.textContent = "What do you want to open?";
  const sub = document.createElement("p");
  sub.textContent = launcherTagline();

  const gallery = document.createElement("div");
  gallery.className = "pf-v6-l-gallery pf-m-gutter";
  // Every card's kebab menu registers its closer here so an outside click / Escape can shut whichever
  // one is open, and opening one closes the rest.
  const menuClosers: (() => void)[] = [];
  const closeAllMenus = (except?: () => void): void => { for (const c of menuClosers) if (c !== except) c(); };
  for (const s of surfaces) {
    const card = document.createElement("div");
    card.className = "pf-v6-c-card pf-m-clickable console-launcher-card";
    card.dataset.open = s.pageId;
    // This card's palette hue drives its icon, watermark, and hover border. Settings sets none and
    // inherits the shared spruce accent via the --card-accent fallback in console.css.
    const accentVar = SURFACE_ACCENTS[s.pageId];
    if (accentVar) card.style.setProperty("--card-accent", `var(${accentVar})`);
    // A real clickable button: role=button + tabindex make it keyboard-reachable and announce it
    // as a button; the Enter/Space handler below completes the contract.
    card.setAttribute("role", "button");
    card.setAttribute("tabindex", "0");
    card.setAttribute("aria-label", "Open " + s.label);
    // The representative glyph, drawn in the card's hue. Decorative (the accessible name is the card's
    // aria-label), so aria-hidden. The gear's whole glyph turns on hover, so its motion hook rides the
    // icon slot; the other surfaces mark a single inner element (data-motion, in SURFACE_ICONS).
    const icon = document.createElement("span");
    icon.className = "console-launcher-card__icon";
    if (s.pageId === "settings") icon.dataset.motion = "gear";
    icon.innerHTML =
      '<svg viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="1.7" ' +
      'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
      (SURFACE_ICONS[s.pageId] ?? '<rect x="4" y="4" width="16" height="16" rx="2"/>') +
      "</svg>";
    const titleEl = document.createElement("div");
    titleEl.className = "pf-v6-c-card__title";
    const titleText = document.createElement("span");
    titleText.className = "pf-v6-c-card__title-text";
    titleText.textContent = s.label;
    titleEl.append(titleText);
    const body = document.createElement("div");
    body.className = "pf-v6-c-card__body";
    body.textContent = s.hint;

    card.append(icon, titleEl, body);
    // The corner watermark: the SAME glyph as the small icon, blown up and bled off the bottom-right,
    // drawn behind the text (z-index in console.css) in a neutral (colorless) tint that drifts on hover.
    // Decorative, aria-hidden. It reuses the icon markup (motion attrs and all), but the motion CSS is
    // icon-scoped so the watermark never animates.
    const glyph = SURFACE_ICONS[s.pageId];
    if (glyph) {
      const mark = document.createElement("span");
      mark.className = "console-launcher-card__watermark";
      mark.innerHTML =
        '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" ' +
        'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + glyph + "</svg>";
      card.append(mark);
    }
    card.addEventListener("click", () => open(s.pageId));
    // Enter/Space open the surface only when the CARD itself is focused - a key press on the kebab or a
    // menu item bubbles here too, so guard on the target to avoid a stray open.
    card.addEventListener("keydown", (ev) => { if (ev.target === card && (ev.key === "Enter" || ev.key === " ")) { ev.preventDefault(); open(s.pageId); } });

    // The kebab: a top-right three-dot button opening a one-item menu ("Open in a new window"). It stops
    // propagation so its click never reaches the card's own open-as-tab handler, and it is a real button
    // (aria-label, aria-haspopup, aria-expanded) with Escape-to-close and outside-click dismissal below.
    const kebab = document.createElement("button");
    kebab.type = "button";
    kebab.className = "console-launcher-card__kebab";
    kebab.dataset.cardKebab = "";
    kebab.setAttribute("aria-label", "More actions for " + s.label);
    kebab.setAttribute("aria-haspopup", "menu");
    kebab.setAttribute("aria-expanded", "false");
    kebab.innerHTML =
      '<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true">' +
      '<circle cx="12" cy="5" r="1.6"/><circle cx="12" cy="12" r="1.6"/><circle cx="12" cy="19" r="1.6"/></svg>';
    const menu = document.createElement("div");
    menu.className = "console-launcher-card__menu";
    menu.dataset.cardMenu = "";
    menu.setAttribute("role", "menu");
    menu.hidden = true;
    const openWin = document.createElement("button");
    openWin.type = "button";
    openWin.className = "console-launcher-card__menuitem";
    openWin.setAttribute("role", "menuitem");
    openWin.textContent = "Open in a new window";
    menu.append(openWin);

    let menuOpen = false;
    const setMenu = (v: boolean): void => { menuOpen = v; menu.hidden = !v; kebab.setAttribute("aria-expanded", v ? "true" : "false"); };
    const closeMenu = (): void => setMenu(false);
    menuClosers.push(closeMenu);
    kebab.addEventListener("click", (ev) => { ev.stopPropagation(); const willOpen = !menuOpen; closeAllMenus(); setMenu(willOpen); if (willOpen) openWin.focus(); });
    kebab.addEventListener("keydown", (ev) => { if (ev.key === "Escape" && menuOpen) { ev.stopPropagation(); closeMenu(); kebab.focus(); } });
    menu.addEventListener("click", (ev) => ev.stopPropagation());
    openWin.addEventListener("click", (ev) => { ev.stopPropagation(); closeMenu(); openSurfaceWindow(s.pageId); });
    card.append(kebab, menu);

    gallery.append(card);
  }

  // Dismiss any open kebab menu on an outside tap or Escape. pointerdown (not click) so a TAP outside
  // reliably closes it on touch, where a synthesized click can be dropped when the tapped node changes;
  // this mirrors the Panes popup / Reference panel outside-dismiss idiom. A pointerdown on a kebab or
  // inside its menu is left for that element's own handler (the toggle, or an item), so those never
  // self-close here.
  document.addEventListener("pointerdown", (ev) => {
    const el = ev.target instanceof Element ? ev.target : (ev.target instanceof Node ? ev.target.parentElement : null);
    if (el?.closest("[data-card-kebab], [data-card-menu]")) return;
    closeAllMenus();
  });
  document.addEventListener("keydown", (ev) => { if (ev.key === "Escape") closeAllMenus(); });

  // A quiet corner affordance to launch the full demo: opens every surface with representative,
  // daemon-free demo data (see main.ts's launchDemo). It sits bottom-right of the launcher and reveals
  // its label on hover/focus, so it reads as a subtle "try it" rather than a primary action.
  const demo = document.createElement("button");
  demo.type = "button";
  demo.className = "console-launcher-demo";
  demo.setAttribute("aria-label", "Launch the demo");
  demo.setAttribute("title", "Launch the demo");
  demo.innerHTML =
    '<span class="console-launcher-demo__icon"><svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true">' +
    '<path d="M12 3l1.6 4.8L18 9l-4.4 1.2L12 15l-1.6-4.8L6 9l4.4-1.2z"/>' +
    '<path d="M18 14l.6 1.9 1.9.6-1.9.6L18 19l-.6-1.9-1.9-.6 1.9-.6z"/></svg></span>' +
    '<span class="console-launcher-demo__label">Launch the demo</span>';
  demo.addEventListener("click", () => launchDemo());

  root.append(title, sub, gallery, demo);
  return root;
}
