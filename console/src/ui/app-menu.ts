// app-menu.ts - the title-bar Applications menu: the console's app drawer. Picking an app ALWAYS lands in
// this window - it opens a tab, or focuses that app's tab if one is already open (console.open.* is
// single-instance). It never spawns an OS window: the console has exactly one route to a new window,
// moving an EXISTING tab out (the tab context menu, tabBar.ts), so picking an app can never strand you
// somewhere you did not ask to go. It also links back to the documentation site. The open/close and
// keyboard behavior is ui/menu.ts's. No-ops without the markup.
import { dispatchCommand } from "../desktop/commands";
import { wireMenu } from "./menu";

export interface AppMenuItem {
  id: string;
  label: string;
}

// initAppMenu fills the menu with one row per app and wires it. The returned disposer releases the
// listeners.
export function initAppMenu(items: readonly AppMenuItem[]): () => void {
  const btn = document.getElementById("console-appmenu-btn");
  const panel = document.getElementById("console-appmenu");
  const list = panel?.querySelector<HTMLElement>("[data-app-list]");
  if (!btn || !panel || !list) return () => {};

  list.replaceChildren(
    ...items.map((item) => {
      const row = document.createElement("li");
      row.className = "pf-v6-c-menu__list-item";
      row.setAttribute("role", "none");
      const open = document.createElement("button");
      open.className = "pf-v6-c-menu__item";
      open.type = "button";
      open.setAttribute("role", "menuitem");
      open.dataset.appOpen = item.id;
      const main = document.createElement("span");
      main.className = "pf-v6-c-menu__item-main";
      const text = document.createElement("span");
      text.className = "pf-v6-c-menu__item-text";
      text.textContent = item.label;
      main.append(text);
      open.append(main);
      row.append(open);
      return row;
    }),
  );

  // dispatchCommand (not a threaded-in callback) because main.ts's open() is a closure inside
  // startConsole; console.open.* is its registered seam, and it is single-instance, so picking an
  // already-open app focuses its tab instead of duplicating it.
  for (const item of panel.querySelectorAll<HTMLElement>("[data-app-open]")) {
    item.addEventListener("click", () => {
      const id = item.dataset.appOpen;
      if (id) dispatchCommand("console.open." + id);
    });
  }

  return wireMenu(panel, btn);
}
