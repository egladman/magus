// contextMenu.ts - the console's context menu: one PF Menu, moved to where it was asked for. The tab
// strip and the navigation rail each hand-built one, and each was a menu with a role and no keyboard:
// no arrows, no Home or End, nothing to open it but a right-click. This is the one menu, with
// ui/menu.ts's keyboard half and an open path for the keyboard (the ContextMenu key, Shift+F10).
//
// ui/menu.ts wants one trigger, and this menu has several origins, so the menu is opened through a
// hidden button. Where focus returns when it closes is the origin, found again if a re-render has
// replaced it.

import { wireMenu } from "../ui/menu";

export interface ContextItem {
  label: string;
  run: () => void;
}

export interface ContextRequest {
  items: ContextItem[];
  // The control the menu was asked from: it gets aria-expanded while the menu is open (if it declares
  // a popup) and focus when the menu closes.
  origin: HTMLElement;
  // The pointer position for a mouse request; null hangs the menu under the origin.
  at: { x: number; y: number } | null;
  // Finds the origin again after a re-render replaced it, or null when it is gone.
  refind?: () => HTMLElement | null;
}

export interface ContextMenu {
  readonly el: HTMLElement;
  open(req: ContextRequest): void;
  close(): void;
  isOpen(): boolean;
  destroy(): void;
}

// onContextKey runs `run` for the keyboard's right-click on `el`: the ContextMenu key and Shift+F10.
export function onContextKey(el: HTMLElement, run: () => void): void {
  el.addEventListener("keydown", (ev) => {
    if (ev.key === "ContextMenu" || (ev.key === "F10" && ev.shiftKey)) {
      ev.preventDefault();
      run();
    }
  });
}

// isKeyboardContext reports a contextmenu event the keyboard raised: it carries no pointer position.
export function isKeyboardContext(ev: MouseEvent): boolean {
  return ev.clientX === 0 && ev.clientY === 0;
}

export function createContextMenu(className: string, label: string): ContextMenu {
  const menu = document.createElement("div");
  menu.className = "pf-v6-c-menu " + className;
  menu.hidden = true;
  const list = document.createElement("ul");
  list.className = "pf-v6-c-menu__list";
  list.setAttribute("role", "menu");
  list.setAttribute("aria-label", label);
  const content = document.createElement("div");
  content.className = "pf-v6-c-menu__content";
  content.append(list);
  menu.append(content);
  const trigger = document.createElement("button");
  trigger.type = "button";
  trigger.hidden = true;
  trigger.tabIndex = -1;

  let current: ContextRequest | null = null;

  const item = (it: ContextItem): HTMLLIElement => {
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
    text.textContent = it.label;
    main.append(text);
    b.append(main);
    b.addEventListener("click", it.run);
    li.append(b);
    return li;
  };

  // place puts the menu at the pointer, or under the control that opened it, then pulls it back inside
  // the viewport (a row near an edge would otherwise open its menu off-screen). Measured while
  // unhidden but invisible, so the box has a real size before it is shown.
  const place = (req: ContextRequest): void => {
    const rect = req.origin.getBoundingClientRect();
    const x = req.at ? req.at.x : rect.left;
    const y = req.at ? req.at.y : rect.bottom;
    menu.style.visibility = "hidden";
    menu.hidden = false;
    const r = menu.getBoundingClientRect();
    menu.hidden = true;
    menu.style.visibility = "";
    menu.style.left = Math.max(4, Math.min(x, window.innerWidth - r.width - 4)) + "px";
    menu.style.top = Math.max(4, Math.min(y, window.innerHeight - r.height - 4)) + "px";
  };

  const popup = (el: HTMLElement): boolean => el.hasAttribute("aria-haspopup");

  const dispose = wireMenu(menu, trigger, {
    onOpen() {
      const req = current;
      if (!req) return;
      list.replaceChildren(...req.items.map(item));
      place(req);
      if (popup(req.origin)) req.origin.setAttribute("aria-expanded", "true");
    },
    onClose() {
      const req = current;
      if (!req) return;
      if (popup(req.origin)) req.origin.setAttribute("aria-expanded", "false");
      // Put focus back where the menu was asked for, unless the reader already moved it somewhere
      // else (an outside click). The origin may have been re-rendered since, so find it again.
      const active = document.activeElement;
      const lost =
        !active || active === document.body || active === trigger || menu.contains(active);
      if (!lost) return;
      const again = req.origin.isConnected ? req.origin : (req.refind?.() ?? null);
      again?.focus();
    },
  });

  // Lives on <body>, not in the control that asked: a re-render of that control would tear an open menu
  // out from under the pointer.
  document.body.append(menu, trigger);

  return {
    el: menu,
    open(req) {
      // Opening for another origin while one is open: close first so the items rebuild.
      if (!menu.hidden) trigger.click();
      current = req;
      trigger.click();
    },
    close() {
      if (!menu.hidden) trigger.click();
    },
    isOpen: () => !menu.hidden,
    destroy() {
      dispose();
      menu.remove();
      trigger.remove();
    },
  };
}
