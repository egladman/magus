// app-menu.ts - the title-bar Applications menu: the console's app drawer. Each row carries the same two
// actions the launcher cards do - the row opens the surface as a TAB (console.open.*, single-instance, so
// it focuses an existing tab rather than duplicating it), and the trailing icon opens it in its own window
// (index.html?app=<id>, which an installed PWA reads as a native app window). New-window used to be the
// row's default, which surprised people into stray OS windows. It also links back to the documentation
// site. Mirrors the settings gear's popover wiring (open/close, aria-expanded, click-outside, Escape,
// focus return) so the two title-bar popovers behave identically. No-ops without the markup.
import { openSurfaceWindow } from "../lib/appwindow";
import { dispatchCommand } from "../console/commands";

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

  // The row itself opens the surface as a tab. dispatchCommand (not a threaded-in callback) because
  // main.ts's open() is a closure inside startConsole; console.open.* is its registered seam, and it is
  // single-instance, so picking an already-open app focuses its tab instead of duplicating it.
  for (const item of panel.querySelectorAll<HTMLElement>("[data-app-open]")) {
    item.addEventListener("click", () => {
      const id = item.dataset.appOpen;
      if (!id) return;
      setOpen(false);
      dispatchCommand("console.open." + id);
    });
  }

  // The trailing icon opens that surface in its own window instead. "popup" strips the tab/URL chrome in
  // a browser; an installed PWA promotes it to a standalone app window. A stable per-surface window name
  // means re-picking the same app focuses its existing window instead of stacking duplicates.
  // stopPropagation so the trailing click never also triggers the row's open-as-tab.
  for (const item of panel.querySelectorAll<HTMLElement>("[data-app-window]")) {
    item.addEventListener("click", (e) => {
      e.preventDefault();
      e.stopPropagation();
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
