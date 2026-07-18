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

// Each launcher card carries its OWN earthy palette hue (--card-accent, set per card below): the small
// icon takes it (a pop of color per tool). Decorative only - the functional UI keeps PatternFly's brand
// accent, and semantic status color stays reserved for health.
const SURFACE_ACCENTS: Record<string, string> = {
  logs: "--console-clay",
  graph: "--console-spruce",
  dashboard: "--console-moss",
  activity: "--console-rust",
  actions: "--console-sage",
  settings: "--console-spruce",
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
  // Dashboard: a gauge; the needle sweeps on hover.
  dashboard: '<path d="M4 13a8 8 0 0 1 16 0"/><path data-motion="needle" d="M12 19l4-6"/><circle cx="12" cy="19" r="1.2" fill="currentColor" stroke="none"/>',
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
  sub.textContent = "See what magus is up to.";

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

  root.append(title, sub, gallery, demo);
  return root;
}
