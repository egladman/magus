// home.ts - the console's launcher. It is NOT a tab: the console renders it as the outlet's empty
// state (main.ts) whenever the workspace has zero open tabs (fresh load, or after the last tab is
// closed). Clicking a card opens that app as a real tab; with a tab open, the command bar
// ("Open ...") is how another app is launched. This module just builds the launcher DOM - a
// heading, a lede, and a PatternFly Gallery of clickable Cards - and leaves mounting to the console.
//
// A card is a PF clickable card: its title is the button that opens that app as a tab. Each card also
// carries a kebab in its header actions, a PF Menu whose one item, "Open in a new window", spawns a
// dedicated OS/PWA window for that app (openAppWindow) - an EXPLICIT opt-in, never the plain-click
// default, so a card can still never strand you in a window you did not ask for.
import { openAppWindow } from "../lib/appwindow";
import { wireMenu } from "../ui/menu";
import { kebabIcon, menuToggle } from "../ui/menu-toggle";
import type { PulseView } from "./pulse";
import {
  DEMO_HINT,
  renderConnectPrompt,
  type ConnectPromptOptions,
  type ConnectPromptState,
} from "./connectPrompt";
import { isWholeMotion, type AppManifest } from "../apps/manifest";
import { assignSigils, describeSigil, renderSigil, sigilSpec, type SigilSpec } from "./sigil";
import { shortName, workspaceScope } from "../lib/scope";

// The launcher says the same thing on every load: a screen whose heading changes each visit cannot be
// found by title, and a screen reader hears a different page each time. The heading and the line
// under it are fixed; the live reading beside them (syncLauncherPulse) is where the screen moves.
export const LAUNCHER_TITLE = "Open an app";
export const LAUNCHER_LEDE = "Each app opens as a tab. One you already have comes forward instead.";

// appIconSvg wraps an app's glyph (its manifest's) in the shared icon idiom: 24x24, stroked
// currentColor, round caps. The rail and the launcher both draw through it so the marks match.
// `size` omitted leaves the svg unsized, which is what the card's corner watermark wants (it is
// scaled by CSS).
export function appIconSvg(glyph: string, size?: number): string {
  const dims = size == null ? "" : ' width="' + size + '" height="' + size + '"';
  return (
    '<svg viewBox="0 0 24 24"' +
    dims +
    ' fill="none" stroke="currentColor" stroke-width="1.7" ' +
    'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    glyph +
    "</svg>"
  );
}

// buildLauncher builds the launcher DOM as the outlet's empty state. `apps` is what it offers to
// open; `open` asks the console to open one as a tab. The returned element carries data-app="home"
// (its heading/lede layout is ID-scoped in console.css) and is appended straight into
// #console-outlet-content as a sibling of the tab panes, shown only when no tab is active.
// syncLauncherPulse turns the welcome screen's first row into a LIVE reading when there is a server
// answering, and hides it when there is not. Called on every pulse tick, so the screen someone lands
// on says what the server is doing right now rather than the same sentence it always says.
//
// The counts are SERVER-WIDE and say so. There is one pool behind every loaded workspace, so
// pool.running is the machine's occupancy and not the caller's - the dashboard hero carries the same
// qualifier for the same reason, and a launcher that dropped it would be the one screen implying these
// numbers are yours.
export function syncLauncherPulse(root: HTMLElement, p: PulseView | null): void {
  const way = root.querySelector<HTMLElement>("[data-launcher-live]");
  const label = root.querySelector<HTMLElement>("[data-launcher-live-label]");
  const hint = root.querySelector<HTMLElement>("[data-launcher-live-hint]");
  if (!way || !label || !hint) return;
  // No answer is not the same as nothing running: an older server that does not serve the route, or a
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
  parts.push("Counts are server-wide.");
  hint.textContent = parts.join(". ");

  paintSaved(root, p);
  paintSigil(root, p);
}

// paintSaved renders the headline figure: how much work the cache replayed instead of ran.
//
// Every millisecond here was MEASURED - each entry records how long the run that produced it took,
// and a hit adds that entry's own figure. Nothing is averaged or extrapolated. It understates,
// because an entry written before durations were recorded contributes nothing, which is why the
// caption says "since this server started" rather than implying a lifetime total.
//
// Hidden below a minute: the number exists to be striking, and "0m" on a fresh server is an
// argument against the cache rather than for it.
function paintSaved(root: HTMLElement, p: PulseView): void {
  const wrap = root.querySelector<HTMLElement>("[data-launcher-saved]");
  const value = root.querySelector<HTMLElement>("[data-launcher-saved-value]");
  const unit = root.querySelector<HTMLElement>("[data-launcher-saved-unit]");
  if (!wrap || !value || !unit) return;
  const ms = p.cache?.savedMs ?? 0;
  wrap.hidden = ms < 60_000;
  if (wrap.hidden) return;
  const mins = Math.round(ms / 60_000);
  if (mins < 90) {
    value.textContent = String(mins);
    unit.textContent = mins === 1 ? "minute the cache saved" : "minutes the cache saved";
    return;
  }
  const hours = ms / 3_600_000;
  value.textContent = hours < 10 ? hours.toFixed(1) : String(Math.round(hours));
  unit.textContent = "hours the cache saved";
}

// paintSigil draws the mark for whichever workspace this window is answering for. Scope first,
// because that is the one the reader chose; otherwise the only loaded workspace. With several loaded
// and none picked there is no mark - drawing an arbitrary one of them would be a claim about which
// workspace you are looking at.
// paintSigil draws the mark for whichever workspace this window is answering for. Scope first, because
// that is the one the reader chose; otherwise the only loaded workspace. With several loaded and none
// picked there is no mark - drawing an arbitrary one would be a claim about which one you are in.
//
// Drawing ONLY. The first-sight ceremony belongs to the shell (cast.ts): it used to run from here,
// which meant it fired only if you happened to be on the zero-tab screen when the workspace became
// known - open a tab first and the one showing was never spent, and never seen either.
function paintSigil(root: HTMLElement, p: PulseView): void {
  const el = root.querySelector<HTMLElement>("[data-launcher-sigil]");
  if (!el) return;
  const seed = workspaceScope() || (p.workspaces.length === 1 ? p.workspaces[0] : "");
  el.hidden = seed === "";
  if (!seed) return;
  const spec: SigilSpec = assignSigils(p.workspaces).get(seed) ?? sigilSpec(seed);
  el.innerHTML = renderSigil(spec, 44);
  el.style.color = "var(" + spec.hue + ")";
  // The SHORT name, never the root. A full path carries a username, a client, an unreleased codename;
  // the sigil discloses none of that by construction, and a tooltip spelling the path out would hand
  // back exactly what the mark was careful not to encode. describeSigil reads the SPEC, not the seed,
  // for the same reason.
  el.title = shortName(seed) + " - " + describeSigil(spec);
}

// syncLauncherChord names the palette's CURRENT key in the zero-tab hint. Called by the shell every
// time the launcher is shown rather than once when it is built, because a rebind in Settings happens
// while the launcher is hidden behind the tab that did it.
export function syncLauncherChord(root: HTMLElement, chord: string): void {
  const hint = root.querySelector<HTMLElement>("[data-empty-hint]");
  if (!hint) return;
  hint.textContent = "";
  if (!chord) {
    hint.textContent = "Pick one from the rail, or use the command palette.";
    return;
  }
  // The chord is a KEYCAP YOU CAN PRESS, not a note about one. A hint that names a shortcut and
  // does nothing when you click it teaches the shortcut to people who already knew it; clicking is
  // how everyone else gets there, and they arrive having seen the chord that would have worked.
  hint.append(document.createTextNode("Pick one from the rail, or press "));
  const key = document.createElement("button");
  key.type = "button";
  key.className = "console-shell-keycap";
  key.dataset.openPalette = "";
  key.textContent = chord;
  key.title = "Open the command palette";
  key.setAttribute("aria-label", "Open the command palette (" + chord + ")");
  hint.append(key, document.createTextNode(" for the palette."));
}

// syncLauncherDemo hides the "Try the demo" way while the console is already in the demo: offering to
// enter the place you are in is a dead end, and the workspace menu is where the demo is left.
export function syncLauncherDemo(root: HTMLElement, inDemo: boolean): void {
  const way = root.querySelector<HTMLElement>("[data-launcher-demo]");
  if (way) way.hidden = inDemo;
}

export function buildLauncher(
  apps: readonly AppManifest[],
  open: (pageId: string) => void,
): HTMLElement {
  // data-app tags the empty state; its heading/lede layout is ID-scoped in console.css. The
  // launcher is a PatternFly Gallery of clickable Cards - the [data-open] hook the click handler keys
  // on rides on each card, and the whole card is the keyboard-reachable target (tabindex + Enter/Space).
  const root = document.createElement("div");
  root.dataset.app = "home";

  // The workspace's SIGIL (sigil.ts): one unique mark per workspace, derived from its root, so this
  // console looks like YOURS and a sibling worktree looks like itself. Fixed - an identifier that
  // changes is not one. Decorative and aria-hidden; the workspace already has a name in text.
  const sigil = document.createElement("span");
  sigil.setAttribute("data-launcher-sigil", "");
  sigil.hidden = true;

  const title = document.createElement("h1");
  title.textContent = LAUNCHER_TITLE;
  const sub = document.createElement("p");
  sub.textContent = LAUNCHER_LEDE;

  const gallery = document.createElement("ul");
  gallery.className = "pf-v6-l-gallery pf-m-gutter";
  // A list with no marker styling loses its list role in Safari; say it.
  gallery.setAttribute("role", "list");
  gallery.setAttribute("aria-label", "Apps");
  for (const s of apps) {
    const li = document.createElement("li");
    const card = document.createElement("div");
    card.className = "pf-v6-c-card pf-m-clickable console-shell-launcher__card";
    card.dataset.open = s.id;
    // This card's palette hue drives its icon, watermark, and hover border. An app with no accent
    // inherits the shared spruce accent via the --card-accent fallback in console.css.
    if (s.accent) card.style.setProperty("--card-accent", `var(${s.accent})`);

    // The header holds the glyph, the card's clickable action and its actions, the PF clickable-card
    // layout. The action is a real button that stretches over the card; the title names it, so a reader
    // hears "Dashboard, button" and not "Open Dashboard, button, Open Dashboard".
    const head = document.createElement("div");
    head.className = "pf-v6-c-card__header";
    const headMain = document.createElement("div");
    headMain.className = "pf-v6-c-card__header-main";
    // The representative glyph, drawn in the card's hue. Decorative, so aria-hidden. A whole motion
    // rides the icon slot; a part motion is already on one inner element of the glyph.
    const icon = document.createElement("span");
    icon.className = "console-shell-launcher__icon";
    if (isWholeMotion(s.motion)) icon.dataset.motion = s.motion;
    icon.innerHTML = appIconSvg(s.glyph, 24);
    headMain.append(icon);

    const titleId = "console-launcher-" + s.id + "-title";
    const hintId = "console-launcher-" + s.id + "-hint";
    const selectable = document.createElement("div");
    selectable.className = "pf-v6-c-card__selectable-actions";
    const action = document.createElement("button");
    action.type = "button";
    action.className = "pf-v6-c-card__clickable-action";
    action.setAttribute("aria-labelledby", titleId);
    action.setAttribute("aria-describedby", hintId);
    action.addEventListener("click", () => open(s.id));
    selectable.append(action);

    // The kebab sits in the card's actions, above the stretched button, and opens a PF Menu. wireMenu
    // gives it the arrow keys, Escape and outside-click close; a menu left open on one card closes when
    // another card's kebab is pressed, because that press is an outside click to the first.
    const actions = document.createElement("div");
    actions.className = "pf-v6-c-card__actions";
    const kebab = menuToggle({
      variant: "plain",
      icon: kebabIcon(),
      ariaLabel: "More actions for " + s.label,
    });
    kebab.dataset.cardKebab = "";
    const menu = document.createElement("div");
    menu.className = "pf-v6-c-menu console-shell-launcher__menu";
    menu.dataset.cardMenu = "";
    menu.hidden = true;
    const menuContent = document.createElement("div");
    menuContent.className = "pf-v6-c-menu__content";
    const menuList = document.createElement("ul");
    menuList.className = "pf-v6-c-menu__list";
    menuList.setAttribute("role", "menu");
    menuList.setAttribute("aria-label", "Actions for " + s.label);
    const menuItem = document.createElement("li");
    menuItem.className = "pf-v6-c-menu__list-item";
    menuItem.setAttribute("role", "none");
    const openWin = document.createElement("button");
    openWin.type = "button";
    openWin.className = "pf-v6-c-menu__item";
    openWin.setAttribute("role", "menuitem");
    const itemMain = document.createElement("span");
    itemMain.className = "pf-v6-c-menu__item-main";
    const itemText = document.createElement("span");
    itemText.className = "pf-v6-c-menu__item-text";
    itemText.textContent = "Open in a new window";
    itemMain.append(itemText);
    openWin.append(itemMain);
    openWin.addEventListener("click", () => openAppWindow(s.id));
    menuItem.append(openWin);
    menuList.append(menuItem);
    menuContent.append(menuList);
    menu.append(menuContent);
    wireMenu(menu, kebab);
    actions.append(kebab, menu);

    head.append(headMain, selectable, actions);

    const titleEl = document.createElement("div");
    titleEl.className = "pf-v6-c-card__title";
    const titleText = document.createElement("h2");
    titleText.className = "pf-v6-c-card__title-text";
    titleText.id = titleId;
    titleText.textContent = s.label;
    titleEl.append(titleText);
    const body = document.createElement("div");
    body.className = "pf-v6-c-card__body";
    body.id = hintId;
    body.textContent = s.hint;

    card.append(head, titleEl, body);
    // The corner watermark: the SAME glyph as the small icon, blown up and bled off the bottom-right,
    // drawn behind the text (z-index in console.css) in a neutral (colorless) tint that drifts on hover.
    // Decorative, aria-hidden. It reuses the icon markup (motion attrs and all), but the motion CSS is
    // icon-scoped so the watermark never animates.
    const mark = document.createElement("span");
    mark.className = "console-shell-launcher__watermark";
    mark.innerHTML = appIconSvg(s.glyph);
    card.append(mark);

    li.append(card);
    gallery.append(li);
  }

  // What the zero-tab screen says once there IS a rail: the console's shared cold-state shape (a row
  // of "ways" out of it, [data-empty-way] in console.css) rather than a second copy of the rail's own
  // eight destinations. Both compositions are built; console.css shows exactly one, on the same 48rem
  // edge the rail uses - above it the rail navigates and this speaks, below it there is no rail so the
  // card grid navigates and this would name a control that is not on screen.
  const ways = document.createElement("div");
  ways.dataset.launcherWays = "";
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
  // to have its own primary button on this screen and on each of five apps - six places offering
  // one thing, on a screen that already had a rail listing every destination. This POINTS at the one
  // control instead of competing with it.
  const demoWay = document.createElement("div");
  demoWay.setAttribute("data-empty-way", "");
  demoWay.dataset.launcherDemo = "";
  const demoLabel = document.createElement("span");
  demoLabel.setAttribute("data-empty-way-label", "");
  demoLabel.textContent = "Try the demo";
  const demoHint = document.createElement("span");
  demoHint.setAttribute("data-empty-hint", "");
  demoHint.textContent = DEMO_HINT;
  demoWay.append(demoLabel, demoHint);

  ways.append(pickWay, demoWay);

  // Stands in for the ways, at every width, once the shell knows no server answers. The first screen
  // is where someone who has never started one lands, and it is the one place that said nothing.
  const connect = document.createElement("div");
  connect.dataset.launcherConnect = "";
  connect.hidden = true;
  const connectLine = document.createElement("p");
  const connectTitle = document.createElement("strong");
  connectTitle.dataset.launcherConnectTitle = "";
  const connectMessage = document.createElement("span");
  connectMessage.dataset.launcherConnectMessage = "";
  connectLine.append(connectTitle, " ", connectMessage);
  const connectActions = document.createElement("div");
  connectActions.dataset.launcherConnectActions = "";
  connectActions.setAttribute("data-empty-ways", "");
  connect.append(connectLine, connectActions);

  // The headline figure, bottom-right of the screen rather than in the reading column: it is not a
  // step in the flow, it is the reason the tool exists, and it should be the thing your eye lands on
  // once it has finished with the choices. Hidden until there is something to say (paintSaved).
  const saved = document.createElement("div");
  saved.dataset.launcherSaved = "";
  saved.hidden = true;
  const savedValue = document.createElement("span");
  savedValue.dataset.launcherSavedValue = "";
  const savedUnit = document.createElement("span");
  savedUnit.dataset.launcherSavedUnit = "";
  const savedNote = document.createElement("span");
  savedNote.dataset.launcherSavedNote = "";
  // The three lines read as ONE phrase downward - "22 / hours the cache saved / this session" -
  // rather than a number with two labels stuck under it. The bound still has to be there, because
  // this is the server's lifetime and not the cache's.
  savedNote.textContent = "this session";
  saved.append(savedValue, savedUnit, savedNote);

  root.append(sigil, title, sub, live, connect, ways, gallery, saved);
  return root;
}

// syncLauncherConnectPrompt shows the connect prompt in place of the launcher's ways while state is
// non-null, and gives the ways back when it is null. Called on every readiness poll.
export function syncLauncherConnectPrompt(
  root: HTMLElement,
  state: ConnectPromptState | null,
  options?: ConnectPromptOptions,
): void {
  const connect = root.querySelector<HTMLElement>("[data-launcher-connect]");
  const ways = root.querySelector<HTMLElement>("[data-launcher-ways]");
  const title = root.querySelector<HTMLElement>("[data-launcher-connect-title]");
  const message = root.querySelector<HTMLElement>("[data-launcher-connect-message]");
  const actions = root.querySelector<HTMLElement>("[data-launcher-connect-actions]");
  if (!connect || !ways || !title || !message || !actions) return;
  connect.hidden = state === null;
  ways.hidden = state !== null;
  if (state) renderConnectPrompt({ title, message, actions }, state, options);
}
