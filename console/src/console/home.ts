// home.ts - the console's launcher. It is NOT a tab: the console renders it as the outlet's empty
// state (main.ts) whenever the workspace has zero open tabs (fresh load, or after the last tab is
// closed). Clicking a card opens that surface as a real tab; with a tab open, the command bar
// ("Open ...") is how another surface is launched. This module just builds the launcher DOM - a
// heading, a lede, and a PatternFly Gallery of clickable Cards - and leaves mounting to the console.
//
// A card only ever opens a tab. Reaching a separate OS window is one route and one route only: move an
// EXISTING tab out (the tab context menu, tabBar.ts). So nothing here can strand you in a window you did
// not ask for - which is why the cards carry no per-card "open in a new window" kebab any more.

// A surface the launcher can open: the pageId the console registered it under, and a human label.
export interface Launchable {
  pageId: string;
  label: string;
  hint: string;
}

// Each launcher card carries its OWN earthy palette hue (--card-accent, set per card below) - the
// icon glyph, the corner watermark, and the hover border all take it, so the home reads as a spread
// of muted color pops, one per tool. Settings has no entry and falls back to the shared functional
// spruce accent. Decorative only: semantic color stays reserved for health.
const SURFACE_ACCENTS: Record<string, string> = {
  logs: "--console-clay",
  graph: "--console-spruce",
  dashboard: "--console-moss",
  activity: "--console-rust",
  actions: "--console-sage",
  // settings: intentionally none -> --card-accent falls back to --console-accent (spruce)
};

// One representative glyph per surface, drawn in the console's shared icon idiom (24x24, stroked
// currentColor, round caps). Keyed by pageId; a surface with no entry falls back to a neutral square.
// A single inner element per animated icon carries data-motion="<kind>": on card hover it plays ONE
// in-character micro-motion (gear turns, gauge needle sweeps, graph node pulses, waveform breathes,
// bolt flickers) - see the @keyframes in console.css, all one-shot and reduced-motion gated. The paths
// are the inner geometry - buildLauncher wraps each in the <svg> shell.
const SURFACE_ICONS: Record<string, string> = {
  // Log viewer: stacked text lines.
  logs: '<path d="M4 5h16M4 10h10M4 15h13M4 19h7"/>',
  // Graph explorer: three connected nodes; the lead node pulses on hover.
  graph: '<circle data-motion="pulse" cx="6" cy="7" r="2.2"/><circle cx="18" cy="6" r="2.2"/><circle cx="15" cy="18" r="2.2"/><path d="M8 8l6 9M8 7l8-1"/>',
  // Dashboard: a gauge; the needle sweeps on hover.
  dashboard: '<path d="M4 13a8 8 0 0 1 16 0"/><path data-motion="needle" d="M12 19l4-6"/><circle cx="12" cy="19" r="1.2" fill="currentColor" stroke="none"/>',
  // Activity: a waveform; it breathes on hover.
  activity: '<path data-motion="wave" d="M3 12h3l2-5 3 10 3-8 2 3h5"/>',
  // Actions: a lightning bolt; it flickers on hover.
  actions: '<path data-motion="bolt" d="M13 2L4 14h6l-1 8 9-12h-6z"/>',
  // Settings: a gear; the whole glyph turns on hover (data-motion on the icon slot, below).
  settings: '<circle cx="12" cy="12" r="3"/><path d="M12 3v3M12 18v3M3 12h3M18 12h3M5.5 5.5l2 2M16.5 16.5l2 2M18.5 5.5l-2 2M7.5 16.5l-2 2"/>',
};

// A large domain glyph per surface, bled off the card's bottom-right corner as a low-opacity watermark
// in the card's own hue (the biggest single "pop of color"). 100x100 viewBox; graph runs a thinner
// stroke so its four nodes do not clot. Keyed by pageId; a surface with no entry shows no watermark.
const SURFACE_WATERMARKS: Record<string, { svg: string; sw: number }> = {
  logs: { svg: '<path d="M14 22h72M14 40h48M14 58h60M14 76h34"/>', sw: 3 },
  graph: { svg: '<circle cx="24" cy="30" r="7"/><circle cx="70" cy="22" r="7"/><circle cx="62" cy="66" r="7"/><circle cx="30" cy="74" r="7"/><path d="M30 33l28 30M31 32l35-6M64 60L36 71"/>', sw: 2.6 },
  dashboard: { svg: '<path d="M16 66a34 34 0 0 1 68 0"/><path d="M50 66l20-26"/>', sw: 3 },
  activity: { svg: '<path d="M8 52h12l8-26 12 46 10-34 7 14h27"/>', sw: 3 },
  actions: { svg: '<path d="M52 8L20 56h24l-4 36 40-52H56z"/>', sw: 3 },
  settings: { svg: '<circle cx="50" cy="50" r="13"/><path d="M50 20v12M50 68v12M20 50h12M68 50h12M29 29l8 8M63 63l8 8M71 29l-8 8M37 63l-8 8"/>', sw: 3 },
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

  const hero = buildHero();

  const gallery = document.createElement("div");
  gallery.className = "pf-v6-l-gallery pf-m-gutter";
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
    // The corner watermark: a large domain glyph bled off the bottom-right in the card's hue, drawn
    // behind the text (z-index in console.css) and drifting on hover. Decorative, aria-hidden.
    const wm = SURFACE_WATERMARKS[s.pageId];
    if (wm) {
      const mark = document.createElement("span");
      mark.className = "console-launcher-card__watermark";
      mark.innerHTML =
        '<svg viewBox="0 0 100 100" fill="none" stroke="currentColor" stroke-width="' + wm.sw + '" ' +
        'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + wm.svg + "</svg>";
      card.append(mark);
    }
    card.addEventListener("click", () => open(s.pageId));
    card.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); open(s.pageId); } });
    gallery.append(card);
  }

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

  root.append(hero, gallery, demo);
  return root;
}

// buildHero builds the launcher's identity band: the node monogram, the "Magus" wordmark and a line of
// intent, and a status cluster (daemon health / host / version) on the right - so the first thing on the
// empty console is what this is and whether it is connected. The status pills carry data hooks the
// console fills live: [data-hero-health] (the poller mirrors the docked #console-conn state here),
// [data-hero-host] (set from the daemon-address setting), and [data-version-chip] (the shared build-info
// fill, same hook the status bar uses). Host and version pills start hidden until there is a value.
function buildHero(): HTMLElement {
  const hero = document.createElement("div");
  hero.className = "console-launcher-hero";
  hero.innerHTML =
    '<svg class="console-launcher-hero__mark" viewBox="0 0 24 24" fill="none" stroke="currentColor" ' +
    'stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<circle cx="6" cy="7" r="2.4"/><circle cx="18" cy="6" r="2.4"/><circle cx="15" cy="18" r="2.4"/>' +
    '<circle cx="5" cy="17" r="2.4"/><path d="M8 8l5 8M8.2 7.2L15.8 6.4M7.3 15.6L12.9 17.4"/></svg>' +
    '<div class="console-launcher-hero__txt">' +
    '<h1 class="console-launcher-hero__name">Magus</h1>' +
    '<p class="console-launcher-hero__sub">Local build daemon. Pick a lens, or open the action bar.</p>' +
    "</div>" +
    '<div class="console-launcher-hero__status">' +
    '<span class="console-launcher-hero__pill" data-hero-health data-state="disconnected">' +
    '<span class="console-launcher-hero__dot"></span><span data-hero-health-text>not connected</span></span>' +
    '<span class="console-launcher-hero__pill" data-hero-host hidden></span>' +
    '<span class="console-launcher-hero__pill console-launcher-hero__pill--ver" data-version-chip hidden></span>' +
    "</div>";
  return hero;
}
