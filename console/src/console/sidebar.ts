// sidebar.ts - the shell's left navigation rail: every surface the console can open, always on
// screen, with the ones that already have a tab marked. It renders a Workspace (tabs.ts) the way
// tabBar.ts does and reports intent through a callback; the console still owns opening and focusing.
//
// Every other route to a surface is TRANSIENT: the tab strip shows only what is already open, the
// Applications menu is a popover, the launcher is the empty state and disappears the moment a tab
// opens, and the Command Palette needs a chord. The rail is the one that stays, so that with a tab
// open there is still something on screen saying what else the console can show.
//
// LEFT, not right: the right edge is the Reference panel's (a PF Drawer, pf-m-panel-right, which
// insets the content when pinned). Two panels docking to the same edge would fight over it.
//
// NARROW by default: the Graph Explorer and the Diff surface already carry their own left sidebars
// INSIDE the pane, so a wide shell panel would put two left columns side by side. Collapsed the rail
// is an icon strip; expanding it is a deliberate, persisted choice (layoutPrefs.ts).
//
// PatternFly: this is a real PF Nav (pf-v6-c-nav, __list, __item, __link, __link-icon, __link-text,
// pf-m-current) rather than an invented component - the console consumes PF's vocabulary as-is and
// tunes it through PF's own per-link custom properties in console.css. Per the naming convention,
// the two things PF has no word for are app hooks and state: data-rail-surface (which surface a row
// opens) and data-tab-open / data-expanded (state), never a --modifier class.
//
// The hook is data-RAIL-surface, not data-surface: the console already uses data-surface to mark a
// mounted SURFACE ROOT, and the rail lives inside #console-outlet where those rules apply - a row
// named data-surface="actions" picks up the Shortcuts surface's own layout and breaks.

import { tabHostsSurface, type Workspace } from "./tabs";
import { bind, scope, type Scope, type Signal } from "./view";
import { surfaceIconSvg, type Launchable } from "./home";
import { openSurfaceWindow } from "../lib/appwindow";
import { dispatchCommand } from "./commands";
import type { PulseView } from "./pulse";
import { badgeLabel, type Badge } from "./badges";

// One row of the rail: the surface it opens, and what the workspace currently makes of it.
// `open` means some tab hosts that surface; `current` means the ACTIVE tab does. They are separate
// because a background tab's surface is open without being what you are looking at, and the rail
// distinguishes the two the way a dock does.
export interface SidebarItem {
  pageId: string;
  label: string;
  hint: string;
  open: boolean;
  current: boolean;
}

export interface SidebarCallbacks {
  onOpen: (pageId: string) => void;
}

// pulseLabel renders the reading for the rail's foot. Collapsed there is room for ONE number, and it
// is the running count: "how busy is this workspace" is the question a glance at a dock answers, and
// a queue only means anything once you know something is occupying the slots. Expanded, the queue
// joins it. A queue of zero is left off rather than shown as "0 queued", so the second number
// appearing is itself the signal that work is backing up.
export function pulseLabel(p: PulseView, expanded: boolean): string {
  if (!expanded) return String(p.running);
  if (p.queued > 0) return p.running + " running, " + p.queued + " queued";
  return p.running + " running";
}

// The full sentence, for the tooltip and the accessible name, in both states - collapsed, the visible
// "3" is not a thing a screen reader can make sense of on its own.
//
// "on this daemon", NOT "in this workspace": the pool is daemon-wide and one daemon serves several
// workspaces, so a workspace-scoped reading would attribute another workspace's runs to yours. The
// wire carries no per-workspace run attribution to narrow it with (see the dashboard's bigPicture.ts,
// which is why its workspace picker filters nothing either).
export function pulseTitle(p: PulseView): string {
  const queued = p.queued > 0 ? ", " + p.queued + " queued" : "";
  return p.running + " running on this daemon" + queued;
}

export interface Sidebar {
  destroy: () => void;
}

// sidebarItems maps a Workspace onto the rail's rows. Pure, so the "which surface is open, which is
// current" rules are unit-tested without a DOM - the same split tabBar.ts uses for its own mapping.
//
// The surface list drives the order, NOT the workspace: the rail is the fixed set of places you can
// go, so a row must never move because a tab opened. Only its state changes.
//
// EXACTLY ONE row is current, and it is the FOCUSED pane's surface - not every surface the active tab
// holds. A tiled tab shows several at once, and marking them all made a starter workspace open with
// two rows lit and no way to tell which one input would reach. Focus is the thing that answers "where
// am I", so focus is what the rail reflects; `focusedPageId` comes from the tile that owns it
// (tileView's onTitleChange), because the persisted Workspace does not carry runtime focus.
export function sidebarItems(
  ws: Workspace,
  surfaces: readonly Launchable[],
  focusedPageId: string | null,
): SidebarItem[] {
  return surfaces.map((s) => ({
    pageId: s.pageId,
    label: s.label,
    hint: s.hint,
    open: ws.tabs.some((t) => tabHostsSurface(t, s.pageId)),
    current: focusedPageId === s.pageId,
  }));
}

// railAction builds a rail row that RUNS something rather than opening a surface. Same markup as a
// surface row, so the two read and behave identically; it simply carries no data-rail-surface hook,
// which is what keeps it out of the current/open bookkeeping that only surfaces have.
function railAction(label: string, iconSvg: string, run: () => void): HTMLLIElement {
  const item = document.createElement("li");
  item.className = "pf-v6-c-nav__item";
  const link = document.createElement("button");
  link.type = "button";
  link.className = "pf-v6-c-nav__link";
  link.setAttribute("aria-label", label);
  link.title = label;
  const icon = document.createElement("span");
  icon.className = "pf-v6-c-nav__link-icon";
  icon.innerHTML = iconSvg;
  const text = document.createElement("span");
  text.className = "pf-v6-c-nav__link-text";
  text.textContent = label;
  link.append(icon, text);
  link.addEventListener("click", run);
  item.append(link);
  return item;
}

// railLink is railAction's counterpart for a destination OUTSIDE the console. An anchor, not a button
// running window.open: the docs are a page, so middle-click, "copy link", and "open in new tab" have
// to work the way they do on every other link.
function railLink(label: string, iconSvg: string, href: string): HTMLLIElement {
  const item = document.createElement("li");
  item.className = "pf-v6-c-nav__item";
  const link = document.createElement("a");
  link.className = "pf-v6-c-nav__link";
  link.href = href;
  link.target = "_blank";
  link.rel = "noopener noreferrer";
  link.title = label;
  link.setAttribute("aria-label", label + " (opens the docs site in a new tab)");
  const icon = document.createElement("span");
  icon.className = "pf-v6-c-nav__link-icon";
  icon.innerHTML = iconSvg;
  const text = document.createElement("span");
  text.className = "pf-v6-c-nav__link-text";
  text.textContent = label;
  link.append(icon, text);
  item.append(link);
  return item;
}

// A PANEL, not a chevron. A chevron is a direction - next, back, more - and every other one in this
// console means exactly that, so on the rail's foot it read as navigation rather than as a control
// over the rail itself. This is the glyph editors settled on for the same job: the frame is the
// window, the filled column is the rail, and which side is filled says which state pressing it
// produces. Same shape in both states, so the target does not appear to change identity mid-toggle.
function panelToggle(expanded: boolean): string {
  return (
    '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" ' +
    'stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<rect x="3" y="4" width="18" height="16" rx="2"/>' +
    '<line x1="9" y1="4" x2="9" y2="20"/>' +
    (expanded
      ? '<rect x="3" y="4" width="6" height="16" rx="2" fill="currentColor" stroke="none"/>'
      : "") +
    "</svg>"
  );
}

// What the rail reads. Every cell is a Signal rather than the Persisted it usually arrives as: the
// rail only ever reads, writes and subscribes, and naming the wider type would make a caller supply
// persistOnly and flushed that nothing here calls - which is exactly what the DOM tests had to
// fabricate before this narrowed.
export interface SidebarState {
  ws: Signal<Workspace>;
  expanded: Signal<boolean>;
  pulse: Signal<PulseView | null>;
  focused: Signal<string | null>;
  badges: Signal<Record<string, Badge>>;
  surfaces: readonly Launchable[];
}

// createSidebar fills `host` (the #console-sidebar element the page supplies) and keeps it in step
// with the workspace and the expanded preference. It REPLACES the host's children.
//
// It is built even in an app-mode window, which does not want one: the shell constructs its chrome
// well before startConsole discovers the ?app param, and the markup is identical either way. CSS
// hides it there (:root[data-appmode], console.css) - do not "fix" that by skipping construction
// without checking that ordering first.
//
// The rows are built ONCE and only their state attributes are rewritten on change: the surface list
// is fixed, so rebuilding the list would throw away focus mid-keyboard-navigation for nothing.
export function createSidebar(
  host: HTMLElement,
  state: SidebarState,
  cb: SidebarCallbacks,
): Sidebar {
  const { ws, expanded, pulse, focused, badges, surfaces } = state;
  const sc: Scope = scope();

  // One menu, moved to the pointer, living on <body> rather than in the rail - the same arrangement
  // tabBar.ts uses and for the same reason: a menu parented to a list that gets rebuilt is a menu torn
  // out from under the pointer. Its document listeners ride an AbortSignal that destroy() fires.
  const menu = document.createElement("div");
  menu.className = "pf-v6-c-menu console-shell-railmenu";
  menu.hidden = true;
  const menuList = document.createElement("ul");
  menuList.className = "pf-v6-c-menu__list";
  menuList.setAttribute("role", "menu");
  const menuContent = document.createElement("div");
  menuContent.className = "pf-v6-c-menu__content";
  menuContent.append(menuList);
  menu.append(menuContent);
  // The row the menu was opened from, so focus has somewhere to land when it closes. Hiding the menu
  // while focus is inside it drops focus to the body, and the next Tab restarts at the top of the page.
  let menuInvoker: HTMLElement | null = null;
  const closeMenu = (): void => {
    if (!menu.hidden && menu.contains(document.activeElement)) menuInvoker?.focus();
    menu.hidden = true;
    menuInvoker = null;
  };
  const menuItem = (label: string, run: () => void): HTMLLIElement => {
    const li = document.createElement("li");
    li.className = "pf-v6-c-menu__list-item";
    li.setAttribute("role", "none");
    const b = document.createElement("button");
    b.type = "button";
    b.className = "pf-v6-c-menu__item";
    b.setAttribute("role", "menuitem");
    const main = document.createElement("span");
    main.className = "pf-v6-c-menu__item-main";
    const text = document.createElement("span");
    text.className = "pf-v6-c-menu__item-text";
    text.textContent = label;
    main.append(text);
    b.append(main);
    b.addEventListener("click", () => {
      closeMenu();
      run();
    });
    li.append(b);
    return li;
  };
  const openMenu = (pageId: string, label: string, x: number, y: number): void => {
    menuInvoker = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    menuList.replaceChildren(
      menuItem("Open " + label, () => cb.onOpen(pageId)),
      menuItem("Open in new window", () => openSurfaceWindow(pageId)),
    );
    menu.hidden = false;
    // Measured after unhiding so the box has a real size, then pulled back inside the viewport - a row
    // near the bottom edge would otherwise open its menu off-screen.
    const r = menu.getBoundingClientRect();
    menu.style.left = Math.min(x, window.innerWidth - r.width - 4) + "px";
    menu.style.top = Math.min(y, window.innerHeight - r.height - 4) + "px";
    menu.querySelector<HTMLElement>("button")?.focus();
  };
  document.body.append(menu);
  const ac = new AbortController();
  document.addEventListener(
    "click",
    (e) => {
      const target = e.target instanceof Node ? e.target : null;
      if (!menu.hidden && !menu.contains(target)) closeMenu();
    },
    { signal: ac.signal },
  );
  document.addEventListener(
    "keydown",
    (e: KeyboardEvent) => {
      if (e.key === "Escape" && !menu.hidden) closeMenu();
    },
    { signal: ac.signal },
  );
  sc.add(() => {
    ac.abort();
    menu.remove();
  });

  // Two lists, one vocabulary: the lenses you work in, then the meta surfaces you consult. The split
  // is what keeps Settings out of the path of the six rows above it, and it is the arrangement both
  // VS Code (gear pinned under the view icons) and macOS sidebars use.
  const list = document.createElement("ul");
  list.className = "pf-v6-c-nav__list";
  const utilityList = document.createElement("ul");
  utilityList.className = "pf-v6-c-nav__list";
  utilityList.dataset.railUtility = "";

  // pageId -> its row's link, so the state pass is a map lookup rather than a re-query per change.
  const links = new Map<string, HTMLButtonElement>();
  const badgeEls = new Map<string, HTMLElement>();
  const labels = new Map<string, string>();

  for (const s of surfaces) {
    const item = document.createElement("li");
    item.className = "pf-v6-c-nav__item";

    // A button, not PF's usual anchor: this opens a tab in place, it does not navigate. PF's nav
    // CSS keys on the class rather than the tag, so the component still styles it.
    const link = document.createElement("button");
    link.type = "button";
    link.className = "pf-v6-c-nav__link";
    link.dataset.railSurface = s.pageId;
    // The accessible name is on the button and stays there in BOTH states, so collapsing the rail to
    // icons never leaves a row unnamed. The visible text below is therefore decorative.
    link.setAttribute("aria-label", s.label);
    link.title = s.label + " - " + s.hint;

    const icon = document.createElement("span");
    icon.className = "pf-v6-c-nav__link-icon";
    icon.innerHTML = surfaceIconSvg(s.pageId, 18);

    const text = document.createElement("span");
    text.className = "pf-v6-c-nav__link-text";
    text.textContent = s.label;

    const badge = document.createElement("span");
    badge.dataset.railBadge = "";
    badge.hidden = true;
    link.append(icon, text, badge);
    link.addEventListener("click", () => cb.onOpen(s.pageId));
    // Right-click for the one thing a row cannot say on its own. The launcher card carried this on a
    // kebab and the tab strip carries it on its own context menu; without it here the rail would be
    // the only way to reach a surface that could not also send it to its own window.
    link.addEventListener("contextmenu", (ev) => {
      ev.preventDefault();
      openMenu(s.pageId, s.label, ev.clientX, ev.clientY);
    });
    item.append(link);
    (s.utility ? utilityList : list).append(item);
    links.set(s.pageId, link);
    badgeEls.set(s.pageId, badge);
    labels.set(s.pageId, s.label);
  }

  // The live reading sits at the foot, between the apps and the toggle: it is about the workspace
  // rather than about any one app, so it belongs to the rail rather than to a row.
  const pulseEl = document.createElement("div");
  pulseEl.id = "console-sidebar-pulse";
  // role=status for TWO reasons, and the first is the one that is easy to miss: aria-label is only
  // honored on an element whose role supports naming from the author, and a bare div's generic role
  // does not - without a role the sentence below is dropped and a screen reader is left with "3".
  // Second, it is a live region, so a count that changes while you are elsewhere is announced
  // politely rather than silently. The paint below writes only on a real change, so the politeness
  // is not spent re-announcing the same number every poll.
  pulseEl.setAttribute("role", "status");
  pulseEl.hidden = true;
  const pulseDot = document.createElement("span");
  pulseDot.className = "pf-v6-c-nav__link-icon";
  const pulseText = document.createElement("span");
  pulseText.className = "pf-v6-c-nav__link-text";
  pulseEl.append(pulseDot, pulseText);

  // A ROW in the utility group, not a control tacked onto the rail's foot. It was a bare icon button
  // sitting alone under a hairline, which read as a band with a glyph adrift in it rather than as
  // something to press - and at the expanded width it had 140px of empty rail beside it. Built from
  // the same nav markup as Settings, so it inherits the row's geometry, hover and focus treatment and
  // lines up with every glyph above it.
  const toggleItem = document.createElement("li");
  toggleItem.className = "pf-v6-c-nav__item";
  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.id = "console-sidebar-toggle";
  toggle.className = "pf-v6-c-nav__link";
  const toggleIcon = document.createElement("span");
  toggleIcon.className = "pf-v6-c-nav__link-icon";
  const toggleText = document.createElement("span");
  toggleText.className = "pf-v6-c-nav__link-text";
  toggle.append(toggleIcon, toggleText);
  toggleItem.append(toggle);
  toggle.addEventListener("click", () => expanded.set(!expanded.get()));

  // The Command Palette leads the utility group. It is an ACTION rather than a destination, which by
  // the strict reading belongs in a toolbar - but it is the "go anywhere" control, and it earns its
  // place beside the navigation it substitutes for when you would rather type than point. Routed
  // through the registered command (the same seam the Applications menu uses) rather than a threaded
  // callback, so the rail cannot become a second way of opening the palette that drifts from the first.
  const paletteItem = railAction(
    "Command Palette",
    '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.7" ' +
      'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
      '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 10l3 3-3 3M13 16h4"/></svg>',
    () => dispatchCommand("console.actionBar.open"),
  );
  utilityList.prepend(paletteItem);
  // The Documentation link lives ONLY in the applications menu, and the rail hides that menu above
  // 48rem - so without a copy here, taking the menu away took the docs with it on every desktop
  // window. Same destination and same new-tab behavior as the menu item it stands in for.
  utilityList.append(
    railLink(
      "Documentation",
      '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.7" ' +
        'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
        '<path d="M4 5a2 2 0 0 1 2-2h9l5 5v11a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2z"/>' +
        '<path d="M14 3v6h6M8 13h8M8 17h5"/></svg>',
      "../documentation/",
    ),
  );
  utilityList.append(toggleItem);
  host.replaceChildren(list, pulseEl, utilityList);

  const paintRows = (): void => {
    for (const item of sidebarItems(ws.get(), surfaces, focused.get())) {
      const link = links.get(item.pageId);
      if (!link) continue;
      link.classList.toggle("pf-m-current", item.current);
      // The pair to pf-m-current: the class is what a sighted reader sees, this is what a screen
      // reader hears, and they must never disagree.
      if (item.current) link.setAttribute("aria-current", "page");
      else link.removeAttribute("aria-current");
      link.toggleAttribute("data-tab-open", item.open);
    }
  };
  // Two sources decide a row's state: which tabs exist, and which pane has focus inside the active one.
  sc.add(bind(ws, paintRows));
  sc.add(focused.subscribe(paintRows));

  // A surface with nothing waiting carries NO badge rather than a zero: the badge answers "how much is
  // waiting", and nothing waiting is not a quantity worth a mark on the rail. An absent reading (no
  // daemon, an older one) is indistinguishable from that here on purpose - both mean "say nothing".
  sc.add(
    bind(badges, (counts) => {
      for (const [pageId, el] of badgeEls) {
        const b = counts[pageId];
        const show = b != null && b.count > 0;
        el.hidden = !show;
        const link = links.get(pageId);
        const label = labels.get(pageId) ?? "";
        if (!show) {
          link?.setAttribute("aria-label", label);
          continue;
        }
        el.textContent = badgeLabel(b.count);
        // The count joins the row's NAME rather than riding as text inside it: aria-label wins over
        // an element's contents, so a badge left as bare text is announced to nobody.
        const spoken = label + ", " + b.count + " " + b.noun;
        link?.setAttribute("aria-label", spoken);
        el.title = spoken;
      }
    }),
  );

  // One attribute on the root carries the whole expanded state, so console.css can key the width and
  // the label visibility off it and expanding costs a CSS transition rather than a re-render.
  sc.add(
    bind(expanded, (on) => {
      host.toggleAttribute("data-expanded", on);
      toggleIcon.innerHTML = panelToggle(on);
      const label = on ? "Collapse the navigation rail" : "Expand the navigation rail";
      toggleText.textContent = on ? "Collapse" : "Expand";
      toggle.setAttribute("aria-label", label);
      toggle.title = label;
      toggle.setAttribute("aria-expanded", on ? "true" : "false");
    }),
  );

  // Two sources, one paint: the reading itself, and the state that decides how much of it fits. A
  // null pulse HIDES the element rather than showing a zero - the daemon not answering and the pool
  // being idle are different facts, and only one of them is a number.
  const paintPulse = (): void => {
    const p = pulse.get();
    pulseEl.hidden = p == null;
    if (!p) return;
    // Idle is stated plainly rather than colored: a green dot on an idle pool reads as a health
    // claim, and this is an occupancy reading, not a verdict. Set before the early return below, so
    // the color can never be left behind by a reading whose text happened not to change.
    pulseEl.dataset.state = p.queued > 0 ? "queued" : p.running > 0 ? "running" : "idle";
    const label = pulseLabel(p, expanded.get());
    const title = pulseTitle(p);
    // Unchanged means untouched: this is a live region, and rewriting the same text re-announces it.
    if (pulseText.textContent === label && pulseEl.title === title) return;
    pulseText.textContent = label;
    pulseEl.title = title;
    pulseEl.setAttribute("aria-label", title);
  };
  sc.add(bind(pulse, paintPulse));
  sc.add(expanded.subscribe(paintPulse));

  return { destroy: () => sc.dispose() };
}
