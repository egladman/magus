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
import type { PulseView } from "./pulse";
import { sigilHue, sigilSvg } from "./sigil";
import { workspaceScope } from "../lib/scope";

// A surface the launcher can open: the pageId the console registered it under, and a human label.
export interface Launchable {
  pageId: string;
  label: string;
  hint: string;
  // A meta surface rather than a lens on the workspace - Settings and Shortcuts, the two you consult
  // rather than work in. The navigation rail pins these to its foot, away from the six you switch
  // between constantly; the launcher grid and the Applications menu ignore the flag and list
  // everything, because neither has a foot to pin them to.
  utility?: boolean;
}

// The launcher lede rotates a small tagline each fresh load - a quiet sign of polish, not a slogan.
// Each entry is dry and tool-flavored (magus is a build tool; the daemon keeps the graph warm),
// understated to match the earthy identity.
//
// Two independent gates, so a line can be specific without needing its own window:
//   `at` - an hour window, start inclusive and end exclusive; a wrapped window like night spans
//          midnight. ANY_HOURS is always eligible.
//   `on` - days of the week (0 = Sunday), for lines that only make sense on some of them. Omitted
//          means every day. Kept separate from `at` rather than folded into it because the two
//          genuinely cross: a Friday evening line is not a Friday line or an evening line.
//
// No entry may carry BOTH a narrow window and a narrow day set unless a broader line covers the same
// slot - see the eligibility test, which walks every hour of every weekday and asserts the pool is
// never empty. The ANY_HOURS entries are what guarantee that, so keep several of them dayless.
//
// The original "See what magus is up to." stays in the pool so nothing is lost. Plain ASCII
// throughout, and no exclamation marks: the warmth is meant to come from variety, not volume.
const ANY_HOURS: [number, number] = [0, 24];
const WEEKEND = [0, 6];
const MONDAY = [1];
const FRIDAY = [5];
interface Tagline {
  text: string;
  at: [number, number];
  on?: number[];
}
const TAGLINES: Tagline[] = [
  { text: "See what magus is up to.", at: ANY_HOURS },
  { text: "Cache warm, spells ready.", at: ANY_HOURS },
  { text: "The graph is warm.", at: ANY_HOURS },
  { text: "Everything is where you left it.", at: ANY_HOURS },
  { text: "Nothing is on fire.", at: ANY_HOURS },
  { text: "The workspace is yours.", at: ANY_HOURS },
  { text: "Ready when you are.", at: ANY_HOURS },
  { text: "The forge is warming up.", at: [5, 11] },
  { text: "Fresh build, fresh coffee.", at: [5, 11] },
  { text: "Morning. What are we building?", at: [5, 11] },
  { text: "First build of the day.", at: [5, 11] },
  { text: "The cache slept well.", at: [5, 11] },
  { text: "Deep in the afternoon build.", at: [11, 17] },
  { text: "Plenty of daylight left to ship.", at: [11, 17] },
  { text: "Hitting a good rhythm.", at: [11, 17] },
  { text: "Halfway through, still warm.", at: [11, 17] },
  { text: "Evening. One more target?", at: [17, 22] },
  { text: "Winding down the day's builds.", at: [17, 22] },
  { text: "Good time to leave it green.", at: [17, 22] },
  { text: "Last build before you log off?", at: [17, 22] },
  { text: "Burning the midnight build.", at: [22, 5] },
  { text: "The daemon never sleeps.", at: [22, 5] },
  { text: "Quiet hours. The cache is listening.", at: [22, 5] },
  { text: "Late one. Keep it cached.", at: [22, 5] },

  // Narrower windows sit inside the broad ones above, so these are extra colour at the edges of the
  // day rather than the only thing eligible there.
  { text: "Before the first coffee. Respect.", at: [4, 7] },
  { text: "The tree is quiet at this hour.", at: [4, 7] },
  { text: "Nobody has pushed yet.", at: [4, 7] },
  { text: "Lunchtime. The cache will keep.", at: [12, 14] },
  { text: "Half a day of green behind you.", at: [12, 14] },
  { text: "Eat something. The build will wait.", at: [12, 14] },

  // Day-gated. Each is deliberately broad on the hour so it reads as a note about the day, not a
  // note about the minute.
  { text: "Monday. Clean slate, warm cache.", at: [5, 12], on: MONDAY },
  { text: "Back at it. The graph kept your place.", at: [5, 12], on: MONDAY },
  { text: "Friday. Leave it green for Monday.", at: [12, 22], on: FRIDAY },
  { text: "Last builds of the week.", at: [12, 22], on: FRIDAY },
  { text: "Weekend build. Nobody is watching.", at: ANY_HOURS, on: WEEKEND },
  { text: "Saturday hacking. The daemon kept the lights on.", at: [8, 22], on: WEEKEND },
  { text: "The weekend tree is a quiet tree.", at: ANY_HOURS, on: WEEKEND },

  // More of the always-eligible pool, so the broad case stays as varied as the narrow ones.
  { text: "No stale artifacts in sight.", at: ANY_HOURS },
  { text: "Every target knows what it needs.", at: ANY_HOURS },
  { text: "The spells are where you left them.", at: ANY_HOURS },
  { text: "Nothing here runs twice.", at: ANY_HOURS },
  { text: "Declared, cached, and accounted for.", at: ANY_HOURS },
  { text: "Ask it what it will do. It will tell you.", at: ANY_HOURS },
  { text: "One binary, still no second toolchain.", at: ANY_HOURS },
  { text: "The graph is not guessing.", at: ANY_HOURS },
];

// The launcher HEADING also rotates, for the same reason the lede does: this is the first thing the
// console says on every fresh load, and one fixed sentence forever is the difference between a tool
// that feels alive and one that feels like a form. Kept as plain questions, ASCII, no exclamation
// marks - the warmth comes from variety, not from enthusiasm.
const TITLES: string[] = [
  "What do you want to open?",
  "Where are we starting?",
  "What are we looking at?",
  "Pick a place to begin.",
  "What needs your attention?",
  "Where to?",
  "What are we shipping?",
  "Where does this one start?",
  "What is worth a look?",
  "Open something.",
  "What is on your mind?",
];

// launcherTitle picks one of the headings at random. `pick` is injected only so the choice is
// testable, exactly as in launcherTagline; unlike the taglines these are not time-gated, because a
// heading that changed character through the day would read as a different app each time.
export function launcherTitle(pick: () => number = Math.random): string {
  return TITLES[Math.floor(pick() * TITLES.length)];
}

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
  const day = now.getDay();
  const eligible = TAGLINES.filter((t) => inWindow(hour, t.at) && (!t.on || t.on.includes(day)));
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
  // Clay, not one of the greens: the greens in this palette already mean "live/healthy" (moss on the
  // dashboard), and a note is not a status. A warm earth tone reads as something a person left behind.
  notes: "--console-clay", // soft terracotta: human prose, warm and hand-placed
  actions: "--console-gold", // ochre yellow: the warm, worn key-cap tone of a keyboard
  settings: "--console-stone", // neutral gray: utility
  // The two review surfaces take the remaining greens, kept clear of moss so neither reads
  // as health: spruce for the diff (deeper - the thing you sit and read), sage for the plan
  // (lighter - a sketch of work not done yet).
  diff: "--console-spruce",
  plan: "--console-sage",
};

// One representative glyph per surface, drawn in the console's shared icon idiom (24x24, stroked
// currentColor, round caps). It is used for BOTH the small tinted-tile icon and the large corner
// watermark, so a card's two marks match. Keyed by pageId; a surface with no entry falls back to a
// neutral square. A single inner element per animated icon carries data-motion="<kind>": on card hover
// the SMALL icon plays ONE in-character micro-motion (gauge needle sweeps, node pulses, waveform
// breathes, spacebar presses) - see the @keyframes in console.css, all one-shot and reduced-motion gated;
// the watermark reuses the same markup but never animates (the motion CSS is icon-scoped). Two glyphs
// animate WHOLE and so hook the icon slot instead (buildLauncher sets it): the gear turns, and the note
// settles - a note's page and its prose have to move as one thing or it tears.
const SURFACE_ICONS: Record<string, string> = {
  // Log viewer: stacked text lines.
  logs: '<path d="M4 5h16M4 10h10M4 15h13M4 19h7"/>',
  // Graph explorer: three connected nodes; the lead node pulses on hover.
  graph:
    '<circle data-motion="pulse" cx="6" cy="7" r="2.2"/><circle cx="18" cy="6" r="2.2"/><circle cx="15" cy="18" r="2.2"/><path d="M8 8l6 9M8 7l8-1"/>',
  // Dashboard: a small bar chart on a baseline (live stats); the tall bar grows on hover.
  dashboard:
    '<path d="M3 21h18"/><rect x="5" y="11" width="4" height="8" rx="1"/><rect data-motion="bars" x="10" y="6" width="4" height="13" rx="1"/><rect x="15" y="14" width="4" height="5" rx="1"/>',
  // Activity: a waveform; it breathes on hover.
  activity: '<path data-motion="wave" d="M3 12h3l2-5 3 10 3-8 2 3h5"/>',
  // Notes: a page of prose lying ASKEW - the one glyph in this set that is not square to the grid,
  // because a note is the one thing in the graph a person put there by hand. The tilt is the whole
  // idea; drawn upright it is just the generic document icon and says "file", not "someone wrote
  // this". The folded corner and two short lines survive down to 16px. The motion rides the icon
  // SLOT (like the gear) rather than an inner element: the page and its prose must settle together.
  notes:
    '<path d="M13.6 3.1 7.4 4.8a2 2 0 0 0-1.4 2.45l3 11a2 2 0 0 0 2.45 1.4l6.8-1.85a2 2 0 0 0 1.4-2.45L17.2 6.6z"/><path d="m13.6 3.1 1 3.6 3.6-1"/><path d="m10.5 11.8 5-1.35"/><path d="m11.4 15.1 3.4-.9"/>',
  // Shortcuts: a keyboard, deliberately not a lightning bolt - a bolt reads as "fast", which is the
  // Palette's job, not this surface's. The spacebar presses on hover. (Key is still the "actions"
  // pageId - see main.ts.)
  actions:
    '<rect x="2" y="6" width="20" height="12" rx="2"/><path d="M6 10h.01M10 10h.01M14 10h.01M18 10h.01"/><path data-motion="press" d="M8.5 14h7"/>',
  // Diff: a split view - a centre rule with shorter lines either side. The left column grows
  // on hover, the one motion that reads as text arriving.
  diff: '<path d="M12 4v16"/><path data-motion="bars" d="M4 8h5M4 12h6M4 16h4"/><path d="M15 8h5M15 12h4M15 16h5"/>',
  // Plan: one unit branching into two, the shape of the delegation tree. The root pulses,
  // matching the graph's lead node - both say "this is where it starts".
  plan: '<circle data-motion="pulse" cx="12" cy="5" r="2.2"/><circle cx="6" cy="19" r="2.2"/><circle cx="18" cy="19" r="2.2"/><path d="M12 7.2v3.8M6 17v-6h12v6"/>',
  // Settings: a proper cog (not the sun-like spoked glyph); the whole icon turns on hover.
  settings:
    '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>',
};

// surfaceIconSvg renders one surface's glyph at `size` px. Exported because the shell's navigation
// rail (sidebar.ts) marks the same surfaces with the same marks: two hand-kept copies of eight
// glyphs would drift the first time one is redrawn. `size` omitted leaves the svg unsized, which is
// what the card's corner watermark wants (it is scaled by CSS). A surface with no glyph of its own
// falls back to a neutral square, so the rail is never left with a hole where an icon should be.
export function surfaceIconSvg(pageId: string, size?: number): string {
  const dims = size == null ? "" : ' width="' + size + '" height="' + size + '"';
  return (
    '<svg viewBox="0 0 24 24"' +
    dims +
    ' fill="none" stroke="currentColor" stroke-width="1.7" ' +
    'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    (SURFACE_ICONS[pageId] ?? '<rect x="4" y="4" width="16" height="16" rx="2"/>') +
    "</svg>"
  );
}

// buildLauncher builds the launcher DOM as the outlet's empty state. `surfaces` is what it offers to
// open; `open` asks the console to open one as a tab. The returned element carries data-surface="home"
// (its heading/lede layout is ID-scoped in console.css) and is appended straight into
// #console-outlet-content as a sibling of the tab panes, shown only when no tab is active.
// syncLauncherPulse turns the welcome screen's first row into a LIVE reading when there is a daemon
// answering, and hides it when there is not. Called on every pulse tick, so the screen someone lands
// on says what the daemon is doing right now rather than the same sentence it always says.
//
// The counts are DAEMON-WIDE and say so. There is one pool behind every loaded workspace, so
// pool.running is the machine's occupancy and not the caller's - the dashboard hero carries the same
// qualifier for the same reason, and a launcher that dropped it would be the one screen implying these
// numbers are yours.
export function syncLauncherPulse(root: HTMLElement, p: PulseView | null): void {
  const way = root.querySelector<HTMLElement>("[data-launcher-live]");
  const label = root.querySelector<HTMLElement>("[data-launcher-live-label]");
  const hint = root.querySelector<HTMLElement>("[data-launcher-live-hint]");
  if (!way || !label || !hint) return;
  // No answer is not the same as nothing running: an older daemon that does not serve the route, or a
  // dropped request, must not render as an idle machine. Hide rather than report a zero nobody measured.
  way.hidden = p === null;
  if (!p) return;
  const running = p.running;
  label.textContent =
    running > 0
      ? running === 1
        ? "1 target running"
        : running + " targets running"
      : "Nothing running";
  const parts: string[] = [];
  if (p.queued > 0) parts.push(p.queued === 1 ? "1 queued" : p.queued + " queued");
  if (p.workspaces.length > 0) {
    parts.push(
      p.workspaces.length === 1 ? "1 workspace loaded" : p.workspaces.length + " workspaces loaded",
    );
  }
  // What the cache actually did, in the units the wire actually carries. NOT "time saved": Cache
  // reports hits/misses/errors/size and no duration, so a saved-minutes figure would be an invented
  // average for work that never ran - a fabricated number on the most-read screen in the app.
  const c = p.cache;
  if (c && c.hits + c.misses > 0) {
    parts.push(Math.round((c.hits / (c.hits + c.misses)) * 100) + "% served from cache");
  }
  parts.push("Counts are daemon-wide.");
  hint.textContent = parts.join(". ");

  paintSigil(root, p);
}

// paintSigil draws the mark for whichever workspace this window is answering for. Scope first,
// because that is the one the reader chose; otherwise the only loaded workspace. With several loaded
// and none picked there is no mark - drawing an arbitrary one of them would be a claim about which
// workspace you are looking at.
function paintSigil(root: HTMLElement, p: PulseView): void {
  const el = root.querySelector<HTMLElement>("[data-launcher-sigil]");
  if (!el) return;
  const seed = workspaceScope() || (p.workspaces.length === 1 ? p.workspaces[0] : "");
  el.hidden = seed === "";
  if (!seed) return;
  el.innerHTML = sigilSvg(seed, 30);
  el.style.color = "var(" + sigilHue(seed) + ")";
  el.title = seed;
}

// syncLauncherChord names the palette's CURRENT key in the zero-tab hint. Called by the shell every
// time the launcher is shown rather than once when it is built, because a rebind in Settings happens
// while the launcher is hidden behind the tab that did it.
export function syncLauncherChord(root: HTMLElement, chord: string): void {
  const hint = root.querySelector<HTMLElement>("[data-empty-hint]");
  if (!hint) return;
  hint.textContent = chord
    ? "Pick one from the rail, or press " + chord + " for the palette."
    : "Pick one from the rail, or use the command palette.";
}

export function buildLauncher(surfaces: Launchable[], open: (pageId: string) => void): HTMLElement {
  // data-surface tags the empty state; its heading/lede layout is ID-scoped in console.css. The
  // launcher is a PatternFly Gallery of clickable Cards - the [data-open] hook the click handler keys
  // on rides on each card, and the whole card is the keyboard-reachable target (tabindex + Enter/Space).
  const root = document.createElement("div");
  root.dataset.surface = "home";

  // The workspace's SIGIL (sigil.ts): one unique mark per workspace, derived from its root, so this
  // console looks like YOURS and a sibling worktree looks like itself. Fixed - an identifier that
  // changes is not one. Decorative and aria-hidden; the workspace already has a name in text.
  const sigil = document.createElement("span");
  sigil.setAttribute("data-launcher-sigil", "");
  sigil.hidden = true;

  const title = document.createElement("h1");
  title.textContent = launcherTitle();
  const sub = document.createElement("p");
  sub.textContent = launcherTagline();

  const gallery = document.createElement("div");
  gallery.className = "pf-v6-l-gallery pf-m-gutter";
  // Every card's kebab menu registers its closer here so an outside click / Escape can shut whichever
  // one is open, and opening one closes the rest.
  const menuClosers: (() => void)[] = [];
  const closeAllMenus = (except?: () => void): void => {
    for (const c of menuClosers) if (c !== except) c();
  };
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
    // aria-label), so aria-hidden. The gear and the note animate as a WHOLE glyph on hover, so their
    // motion hook rides the icon slot; the other surfaces mark a single inner element (data-motion, in
    // SURFACE_ICONS).
    const icon = document.createElement("span");
    icon.className = "console-launcher-card__icon";
    if (s.pageId === "settings") icon.dataset.motion = "gear";
    if (s.pageId === "notes") icon.dataset.motion = "settle";
    icon.innerHTML = surfaceIconSvg(s.pageId, 24);
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
      mark.innerHTML = surfaceIconSvg(s.pageId);
      card.append(mark);
    }
    card.addEventListener("click", () => open(s.pageId));
    // Enter/Space open the surface only when the CARD itself is focused - a key press on the kebab or a
    // menu item bubbles here too, so guard on the target to avoid a stray open.
    card.addEventListener("keydown", (ev) => {
      if (ev.target === card && (ev.key === "Enter" || ev.key === " ")) {
        ev.preventDefault();
        open(s.pageId);
      }
    });

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
    const setMenu = (v: boolean): void => {
      menuOpen = v;
      menu.hidden = !v;
      kebab.setAttribute("aria-expanded", v ? "true" : "false");
    };
    const closeMenu = (): void => setMenu(false);
    menuClosers.push(closeMenu);
    kebab.addEventListener("click", (ev) => {
      ev.stopPropagation();
      const willOpen = !menuOpen;
      closeAllMenus();
      setMenu(willOpen);
      if (willOpen) openWin.focus();
    });
    kebab.addEventListener("keydown", (ev) => {
      if (ev.key === "Escape" && menuOpen) {
        ev.stopPropagation();
        closeMenu();
        kebab.focus();
      }
    });
    menu.addEventListener("click", (ev) => ev.stopPropagation());
    openWin.addEventListener("click", (ev) => {
      ev.stopPropagation();
      closeMenu();
      openSurfaceWindow(s.pageId);
    });
    card.append(kebab, menu);

    gallery.append(card);
  }

  // Dismiss any open kebab menu on an outside tap or Escape. pointerdown (not click) so a TAP outside
  // reliably closes it on touch, where a synthesized click can be dropped when the tapped node changes;
  // this mirrors the Panes popup / Reference panel outside-dismiss idiom. A pointerdown on a kebab or
  // inside its menu is left for that element's own handler (the toggle, or an item), so those never
  // self-close here.
  document.addEventListener("pointerdown", (ev) => {
    const el =
      ev.target instanceof Element
        ? ev.target
        : ev.target instanceof Node
          ? ev.target.parentElement
          : null;
    if (el?.closest("[data-card-kebab], [data-card-menu]")) return;
    closeAllMenus();
  });
  document.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") closeAllMenus();
  });

  // What the zero-tab screen says once there IS a rail: the console's shared cold-state shape (a row
  // of "ways" out of it, [data-empty-way] in console.css) rather than a second copy of the rail's own
  // eight destinations. Both compositions are built; console.css shows exactly one, on the same 48rem
  // edge the rail uses - above it the rail navigates and this speaks, below it there is no rail so the
  // card grid navigates and this would name a control that is not on screen.
  const ways = document.createElement("div");
  ways.dataset.launcherWays = "";
  ways.className = "pf-v6-c-empty-state__actions";
  ways.setAttribute("data-empty-ways", "");

  // The live reading lives in the HEADER, not among the ways. As a third card it wrapped the row to two
  // lines and put a full-width primary button under a READING - the loudest control on the screen
  // belonged to the thing you look at rather than the thing you do. Up here it makes the top of the
  // page move without adding anything to choose between.
  //
  // Hidden until a pulse arrives, which on a cold visit is never; that visit gets the ways below and
  // nothing that looks half-loaded.
  const live = document.createElement("p");
  live.setAttribute("data-launcher-live", "");
  live.hidden = true;
  const liveLabel = document.createElement("strong");
  liveLabel.setAttribute("data-launcher-live-label", "");
  const liveHint = document.createElement("span");
  liveHint.setAttribute("data-launcher-live-hint", "");
  const liveBtn = document.createElement("button");
  liveBtn.type = "button";
  liveBtn.className = "pf-v6-c-button pf-m-link pf-m-inline";
  const liveBtnText = document.createElement("span");
  liveBtnText.className = "pf-v6-c-button__text";
  liveBtnText.textContent = "Open the dashboard";
  liveBtn.append(liveBtnText);
  liveBtn.addEventListener("click", () => open("dashboard"));
  live.append(
    liveLabel,
    document.createTextNode(" "),
    liveHint,
    document.createTextNode(" "),
    liveBtn,
  );

  const pickWay = document.createElement("div");
  pickWay.setAttribute("data-empty-way", "");
  const pickLabel = document.createElement("span");
  pickLabel.setAttribute("data-empty-way-label", "");
  pickLabel.textContent = "Open an app";
  const pickHint = document.createElement("span");
  pickHint.setAttribute("data-empty-hint", "");
  // Names the rail because this composition only ever renders at a width where the rail exists. The
  // palette's chord is deliberately absent: it is user-remappable, and the launcher is built once at
  // startup, so a chord baked in here would keep naming the key someone rebound. The shell stamps the
  // live one through syncLauncherChord each time it shows this.
  pickHint.textContent = "Pick one from the rail, or use the command palette.";
  pickWay.append(pickLabel, pickHint);

  // The demo is reached from the title bar's workspace control now, not from a button here. It used
  // to have its own primary button on this screen and on each of five surfaces - six places offering
  // one thing, on a screen that already had a rail listing every destination. This POINTS at the one
  // control instead of competing with it.
  const demoWay = document.createElement("div");
  demoWay.setAttribute("data-empty-way", "");
  const demoLabel = document.createElement("span");
  demoLabel.setAttribute("data-empty-way-label", "");
  demoLabel.textContent = "Try the demo";
  const demoHint = document.createElement("span");
  demoHint.setAttribute("data-empty-hint", "");
  demoHint.textContent = "Pick Demo data from the Workspace menu. Sample data, no daemon needed.";
  demoWay.append(demoLabel, demoHint);

  ways.append(pickWay, demoWay);

  root.append(sigil, title, sub, live, ways, gallery);
  return root;
}
