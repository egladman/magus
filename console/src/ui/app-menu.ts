// app-menu.ts - the title-bar Applications menu: the console's app drawer. Picking an app ALWAYS lands in
// this window - it opens a tab, or focuses that app's tab if one is already open (console.open.* is
// single-instance). It never spawns an OS window: the console has exactly one route to a new window,
// moving an EXISTING tab out (the tab context menu, tabBar.ts), so picking an app can never strand you
// somewhere you did not ask to go. It also links back to the documentation site. Mirrors the settings
// gear's popover wiring (open/close, aria-expanded, click-outside, Escape, focus return) so the two
// title-bar popovers behave identically. No-ops without the markup.
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

  // Link items (e.g. Documentation, which opens the docs site in a new tab) don't dispatch a command, so
  // they aren't covered by the loop above. Close the menu when one is activated so it isn't left hanging
  // open behind the newly opened tab.
  for (const link of panel.querySelectorAll<HTMLAnchorElement>("a.pf-v6-c-menu__item")) {
    link.addEventListener("click", () => setOpen(false));
  }

  btn.addEventListener("click", (e) => {
    e.stopPropagation();
    setOpen(!open);
  });
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
