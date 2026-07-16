// app-menu.ts - the title-bar Applications menu: the console's app drawer. It opens any console
// surface (log viewer, graph, dashboard, activity) in its OWN dedicated window - a URL-bar-less popup
// that boots the console in single-surface "app mode" (index.html?app=<id>, handled in main.ts), so an
// installed PWA reads it as a native app window. It also links back to the documentation site. It
// mirrors the settings gear's popover wiring (open/close, aria-expanded, click-outside, Escape, focus
// return) so the two title-bar popovers behave identically. No-ops without the markup.
import { openSurfaceWindow } from "../lib/appwindow";

export function initAppMenu(): void {
  const btn = document.getElementById("console-appmenu-btn");
  const panel = document.getElementById("console-appmenu");
  if (!btn || !panel) return;

  let open = false;
  const render = (): void => {
    panel.hidden = !open;
    btn.setAttribute("aria-expanded", open ? "true" : "false");
  };
  // Opening moves focus to the first menu item; closing via Escape returns focus to the toggle.
  const setOpen = (v: boolean, restoreFocus = false): void => {
    open = v;
    render();
    if (v) panel.querySelector<HTMLElement>("a, button")?.focus();
    else if (restoreFocus) btn.focus();
  };

  // Each [data-app-window] item opens that surface in its own window. "popup" strips the tab/URL chrome
  // in a browser; an installed PWA promotes it to a standalone app window. A stable per-surface window
  // name means re-picking the same app focuses its existing window instead of stacking duplicates. The
  // drawer closes once the window is requested (a menu item does not navigate this document away).
  for (const item of panel.querySelectorAll<HTMLElement>("[data-app-window]")) {
    item.addEventListener("click", (e) => {
      e.preventDefault();
      const id = item.dataset.appWindow;
      if (!id) return;
      openSurfaceWindow(id);
      setOpen(false, true);
    });
  }

  btn.addEventListener("click", (e) => { e.stopPropagation(); setOpen(!open); });
  document.addEventListener("click", (e) => {
    if (!open) return;
    const t = e.target as Node;
    if (panel.contains(t) || btn.contains(t)) return;
    setOpen(false);
  });
  document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key === "Escape" && open) setOpen(false, true);
  });

  render();
}
