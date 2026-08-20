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

import type { Persisted } from "../lib/persist";
import { tabHostsSurface, type Workspace } from "./tabs";
import { bind, scope, type Scope, type Signal } from "./view";
import { surfaceIconSvg, type Launchable } from "./home";
import type { PulseView } from "./pulse";

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
  el: HTMLElement;
  destroy: () => void;
}

// sidebarItems maps a Workspace onto the rail's rows. Pure, so the "which surface is open, which is
// current" rules are unit-tested without a DOM - the same split tabBar.ts uses for its own mapping.
//
// The surface list drives the order, NOT the workspace: the rail is the fixed set of places you can
// go, so a row must never move because a tab opened. Only its state changes.
export function sidebarItems(ws: Workspace, surfaces: readonly Launchable[]): SidebarItem[] {
  const active = ws.tabs.find((t) => t.id === ws.activeId) ?? null;
  return surfaces.map((s) => ({
    pageId: s.pageId,
    label: s.label,
    hint: s.hint,
    open: ws.tabs.some((t) => tabHostsSurface(t, s.pageId)),
    current: active != null && tabHostsSurface(active, s.pageId),
  }));
}

// The chevron points the way the rail will MOVE, the convention every dock and drawer uses: right to
// grow, left to shrink back to icons.
function chevron(expanded: boolean): string {
  const d = expanded ? "M15 6l-6 6 6 6" : "M9 6l6 6-6 6";
  return (
    '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" ' +
    'stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<path d="' +
    d +
    '"/></svg>'
  );
}

// createSidebar fills `host` (the #console-sidebar element the page supplies) and keeps it in step
// with the workspace and the expanded preference. It REPLACES the host's children, and the caller
// owns whether there is a host at all - an app-mode window mounts one surface with no shell chrome,
// and simply never calls this.
//
// The rows are built ONCE and only their state attributes are rewritten on change: the surface list
// is fixed, so rebuilding the list would throw away focus mid-keyboard-navigation for nothing.
export function createSidebar(
  host: HTMLElement,
  ws: Persisted<Workspace>,
  expanded: Persisted<boolean>,
  pulse: Signal<PulseView | null>,
  surfaces: readonly Launchable[],
  cb: SidebarCallbacks,
): Sidebar {
  const sc: Scope = scope();

  const list = document.createElement("ul");
  list.className = "pf-v6-c-nav__list";

  // pageId -> its row's link, so the state pass is a map lookup rather than a re-query per change.
  const links = new Map<string, HTMLButtonElement>();

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

    link.append(icon, text);
    link.addEventListener("click", () => cb.onOpen(s.pageId));
    item.append(link);
    list.append(item);
    links.set(s.pageId, link);
  }

  // The live reading sits at the foot, between the apps and the toggle: it is about the workspace
  // rather than about any one app, so it belongs to the rail rather than to a row.
  const pulseEl = document.createElement("div");
  pulseEl.id = "console-sidebar-pulse";
  pulseEl.hidden = true;
  const pulseDot = document.createElement("span");
  pulseDot.className = "pf-v6-c-nav__link-icon";
  const pulseText = document.createElement("span");
  pulseText.className = "pf-v6-c-nav__link-text";
  pulseEl.append(pulseDot, pulseText);

  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.id = "console-sidebar-toggle";
  toggle.className = "pf-v6-c-button pf-m-plain";
  const toggleIcon = document.createElement("span");
  toggleIcon.className = "pf-v6-c-button__icon";
  toggle.append(toggleIcon);
  toggle.addEventListener("click", () => expanded.set(!expanded.get()));

  host.replaceChildren(list, pulseEl, toggle);

  sc.add(
    bind(ws, (w) => {
      for (const item of sidebarItems(w, surfaces)) {
        const link = links.get(item.pageId);
        if (!link) continue;
        link.classList.toggle("pf-m-current", item.current);
        // The pair to pf-m-current: the class is what a sighted reader sees, this is what a screen
        // reader hears, and they must never disagree.
        if (item.current) link.setAttribute("aria-current", "page");
        else link.removeAttribute("aria-current");
        link.toggleAttribute("data-tab-open", item.open);
      }
    }),
  );

  // One attribute on the root carries the whole expanded state, so console.css can key the width and
  // the label visibility off it and expanding costs a CSS transition rather than a re-render.
  sc.add(
    bind(expanded, (on) => {
      host.toggleAttribute("data-expanded", on);
      toggleIcon.innerHTML = chevron(on);
      const label = on ? "Collapse the navigation rail" : "Expand the navigation rail";
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
    pulseText.textContent = pulseLabel(p, expanded.get());
    pulseEl.title = pulseTitle(p);
    pulseEl.setAttribute("aria-label", pulseTitle(p));
    // Idle is stated plainly rather than colored: a green dot on an idle pool reads as a health
    // claim, and this is an occupancy reading, not a verdict.
    pulseEl.dataset.state = p.queued > 0 ? "queued" : p.running > 0 ? "running" : "idle";
  };
  sc.add(bind(pulse, paintPulse));
  sc.add(expanded.subscribe(paintPulse));

  return { el: host, destroy: () => sc.dispose() };
}
