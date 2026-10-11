// menu.ts - a menu toggle that opens a PF Menu of actions, for the board's two choices of "where to go"
// (a failing target's output or rerun command, a run to open in the log viewer). The keyboard and
// outside-click behaviour is ui/menu.ts's wireMenu; this builds the markup it needs and keeps the
// rows replaceable.

import { h } from "../../../desktop/view";
import { wireMenu } from "../../../ui/menu";

export interface MenuAction {
  label: string;
  run(): void;
}

export interface MenuButton {
  // The anchor: the button and its menu, positioned against each other by the stylesheet.
  readonly el: HTMLElement;
  readonly button: HTMLButtonElement;
  setActions(actions: readonly MenuAction[]): void;
  // Unwires the menu. Call it before the element is discarded: wireMenu holds a document listener.
  dispose(): void;
}

// menuButton builds the anchor. `button` is the caller's own PF MenuToggle (ui/menu-toggle.ts),
// already labelled, so the same helper serves a chip and a secondary toggle.
export function menuButton(button: HTMLButtonElement, actions: readonly MenuAction[]): MenuButton {
  const el = h("div", "console-dashboard-menu");
  const menu = h("div", "pf-v6-c-menu");
  menu.hidden = true;
  const content = h("div", "pf-v6-c-menu__content");
  const list = h("ul", "pf-v6-c-menu__list");
  list.setAttribute("role", "menu");
  list.setAttribute("aria-label", button.textContent?.trim() || "Actions");
  content.append(list);
  menu.append(content);
  el.append(button, menu);

  const setActions = (next: readonly MenuAction[]): void => {
    list.replaceChildren(
      ...next.map((action) => {
        const li = h("li", "pf-v6-c-menu__list-item");
        li.setAttribute("role", "none");
        const item = h("button", "pf-v6-c-menu__item");
        item.type = "button";
        item.setAttribute("role", "menuitem");
        const main = h("span", "pf-v6-c-menu__item-main");
        main.append(h("span", "pf-v6-c-menu__item-text", action.label));
        item.append(main);
        item.addEventListener("click", () => action.run());
        li.append(item);
        return li;
      }),
    );
  };
  setActions(actions);

  const unwire = wireMenu(menu, button);
  return { el, button, setActions, dispose: unwire };
}
