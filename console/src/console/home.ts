// home.ts - the console's launcher. It is NOT a tab: the console renders it as the outlet's empty
// state (main.ts) whenever the workspace has zero open tabs (fresh load, or after the last tab is
// closed). Clicking a card opens that surface as a real tab; with a tab open, the command palette
// ("Open ...") is how another surface is launched. This module just builds the launcher DOM - a
// heading, a lede, and a PatternFly Gallery of clickable Cards - and leaves mounting to the console.

import { openSurfaceWindow } from "../lib/appwindow";

// A surface the launcher can open: the pageId the console registered it under, and a human label.
export interface Launchable {
  pageId: string;
  label: string;
  hint: string;
}

// One representative glyph per surface, drawn in the console's shared icon idiom (24x24, stroked
// currentColor, round caps) so the launcher matches the title-bar and toolbar iconography. Keyed by
// pageId; a surface with no entry falls back to a neutral square. The paths are the inner geometry -
// buildLauncher wraps each in the <svg> shell.
const SURFACE_ICONS: Record<string, string> = {
  // Log viewer: a document with text lines.
  logs: '<path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M14 3v6h6"/><path d="M8 13h8M8 17h6"/>',
  // Graph explorer: three connected nodes.
  graph: '<circle cx="6" cy="6" r="2.5"/><circle cx="18" cy="9" r="2.5"/><circle cx="9" cy="18" r="2.5"/><path d="M8.3 7.2 15.6 8.4M8.6 16 8.2 8.6"/>',
  // Dashboard: a gauge/speedometer.
  dashboard: '<path d="M3.5 18a10 10 0 1 1 17 0"/><path d="M12 14l3.5-3.5"/>',
  // Activity: a pulse/activity line.
  activity: '<path d="M3 12h4l3 8 4-16 3 8h4"/>',
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
  sub.textContent = "Each tool opens in its own tab.";

  const gallery = document.createElement("div");
  gallery.className = "pf-v6-l-gallery pf-m-gutter";
  // Every card's kebab menu registers a closer here; one document listener shuts them all on an
  // outside click or Escape, so only one card menu is ever open.
  const closeMenus: Array<() => void> = [];
  const closeAll = (): void => { for (const c of closeMenus) c(); };
  for (const s of surfaces) {
    const card = document.createElement("div");
    card.className = "pf-v6-c-card pf-m-clickable console-launcher-card";
    card.dataset.open = s.pageId;
    // A real clickable button: role=button + tabindex make it keyboard-reachable and announce it
    // as a button; the Enter/Space handler below completes the contract.
    card.setAttribute("role", "button");
    card.setAttribute("tabindex", "0");
    card.setAttribute("aria-label", "Open " + s.label);
    // A representative glyph in an accent-tinted tile, in the console's shared icon style. Decorative
    // (the accessible name is the card's aria-label), so aria-hidden.
    const icon = document.createElement("span");
    icon.className = "console-launcher-card__icon";
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

    // Per-card kebab: a PF plain-button toggle over a small PF Menu with the open actions. It sits in
    // the card's top-right corner. Clicks on the kebab/menu stopPropagation so they never trigger the
    // card's own open handler.
    const kebab = document.createElement("button");
    kebab.type = "button";
    kebab.className = "pf-v6-c-button pf-m-plain console-launcher-card__kebab";
    kebab.setAttribute("aria-label", "More actions for " + s.label);
    kebab.setAttribute("aria-haspopup", "true");
    kebab.setAttribute("aria-expanded", "false");
    kebab.innerHTML =
      '<span class="pf-v6-c-button__icon"><svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor" aria-hidden="true">' +
      '<circle cx="12" cy="5" r="1.7"/><circle cx="12" cy="12" r="1.7"/><circle cx="12" cy="19" r="1.7"/></svg></span>';
    const menu = document.createElement("div");
    menu.className = "pf-v6-c-menu console-launcher-card__menu";
    menu.hidden = true;
    const mkItem = (label: string, run: () => void): HTMLLIElement => {
      const li = document.createElement("li");
      li.className = "pf-v6-c-menu__list-item";
      li.setAttribute("role", "none");
      const b = document.createElement("button");
      b.className = "pf-v6-c-menu__item";
      b.type = "button";
      b.setAttribute("role", "menuitem");
      b.innerHTML = '<span class="pf-v6-c-menu__item-main"><span class="pf-v6-c-menu__item-text">' + label + "</span></span>";
      b.addEventListener("click", (ev) => { ev.stopPropagation(); setMenu(false); run(); });
      li.append(b);
      return li;
    };
    const list = document.createElement("ul");
    list.className = "pf-v6-c-menu__list";
    list.setAttribute("role", "menu");
    // Clicking the card body already opens it in a tab, so the menu only carries the distinct action.
    list.append(mkItem("Open in a new window", () => openSurfaceWindow(s.pageId)));
    const menuContent = document.createElement("div");
    menuContent.className = "pf-v6-c-menu__content";
    menuContent.append(list);
    menu.append(menuContent);

    const setMenu = (v: boolean): void => {
      menu.hidden = !v;
      kebab.setAttribute("aria-expanded", v ? "true" : "false");
    };
    closeMenus.push(() => setMenu(false));
    kebab.addEventListener("click", (ev) => { ev.stopPropagation(); const willOpen = menu.hidden; closeAll(); setMenu(willOpen); });
    menu.addEventListener("click", (ev) => ev.stopPropagation());

    card.append(kebab, menu, icon, titleEl, body);
    card.addEventListener("click", () => open(s.pageId));
    card.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); open(s.pageId); } });
    gallery.append(card);
  }

  // Outside-click and Escape close any open card menu. Registered once for the whole launcher.
  document.addEventListener("click", closeAll);
  document.addEventListener("keydown", (ev) => { if (ev.key === "Escape") closeAll(); });

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
