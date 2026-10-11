// menu.ts - the keyboard half of a menu. The console's menus (Applications, Workspace, the
// notification panel's "Dismiss older") were each a button that toggled `hidden` and nothing else, so
// the arrow keys did nothing and Tab walked into items the reader had not asked to enter. This gives
// them the WAI-ARIA menu button pattern once.

const ITEMS = '[role="menuitem"], [role="menuitemradio"], [role="menuitemcheckbox"]';

export interface MenuOptions {
  // Runs just before the menu is shown, for a caller that rebuilds its items on open.
  onOpen?: () => void;
  // Runs after the menu is hidden, whichever way it closed.
  onClose?: () => void;
}

function enabled(el: HTMLElement): boolean {
  return !el.hasAttribute("disabled") && el.getAttribute("aria-disabled") !== "true" && !el.hidden;
}

// wireMenu makes `menu` the popup of `trigger`.
//
// The trigger toggles it, and Down or Up on a closed trigger opens it onto the first or last item.
// Inside, one item holds tabindex 0 at a time (roving focus); Up, Down, Home and End move it, and
// Up and Down wrap. Activating an item closes the menu. Escape closes it and returns focus to the
// trigger. Tab sends focus to the trigger first and then closes, so the Tab carries on from the
// trigger instead of from an element that has just been hidden. A click outside closes it and moves
// focus only if it was still inside.
//
// Visibility is the menu's `hidden` attribute and the trigger's aria-expanded; the caller owns the
// styling. Items are read at the moment of each key, so a menu that rebuilds its rows needs no
// re-wiring. The returned disposer removes every listener this added, and is safe to call twice.
export function wireMenu(
  menu: HTMLElement,
  trigger: HTMLElement,
  opts: MenuOptions = {},
): () => void {
  const listeners = new AbortController();
  const { signal } = listeners;

  const items = (): HTMLElement[] => [...menu.querySelectorAll<HTMLElement>(ITEMS)].filter(enabled);
  const isOpen = (): boolean => !menu.hidden;

  const focusItem = (el: HTMLElement | undefined): void => {
    if (!el) return;
    for (const item of items()) item.tabIndex = item === el ? 0 : -1;
    el.focus();
  };

  // The checked radio is where a reader expects to land, if there is one.
  const entry = (list: HTMLElement[]): HTMLElement | undefined =>
    list.find((i) => i.getAttribute("aria-checked") === "true") ?? list[0];

  trigger.setAttribute("aria-haspopup", "menu");
  trigger.setAttribute("aria-expanded", isOpen() ? "true" : "false");

  const open = (at?: "first" | "last"): void => {
    if (isOpen()) return;
    opts.onOpen?.();
    menu.hidden = false;
    trigger.setAttribute("aria-expanded", "true");
    const list = items();
    for (const item of list) item.tabIndex = -1;
    focusItem(at === "last" ? list[list.length - 1] : entry(list));
  };

  const close = (returnFocus: "always" | "if-inside" | "never"): void => {
    if (!isOpen()) return;
    const inside = menu.contains(document.activeElement);
    menu.hidden = true;
    trigger.setAttribute("aria-expanded", "false");
    if (returnFocus === "always" || (returnFocus === "if-inside" && inside)) trigger.focus();
    opts.onClose?.();
  };

  trigger.addEventListener(
    "click",
    () => {
      if (isOpen()) close("always");
      else open();
    },
    { signal },
  );

  trigger.addEventListener(
    "keydown",
    (e: KeyboardEvent) => {
      if (isOpen() || (e.key !== "ArrowDown" && e.key !== "ArrowUp")) return;
      e.preventDefault();
      open(e.key === "ArrowUp" ? "last" : "first");
    },
    { signal },
  );

  menu.addEventListener(
    "keydown",
    (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        // Not propagated: a panel that holds this menu would otherwise close too.
        e.stopPropagation();
        close("always");
        return;
      }
      if (e.key === "Tab") {
        trigger.focus();
        close("never");
        return;
      }
      const list = items();
      if (list.length === 0) return;
      const at = list.indexOf(document.activeElement as HTMLElement);
      let next: number;
      switch (e.key) {
        case "ArrowDown":
          next = at < 0 ? 0 : (at + 1) % list.length;
          break;
        case "ArrowUp":
          next = at < 0 ? list.length - 1 : (at - 1 + list.length) % list.length;
          break;
        case "Home":
          next = 0;
          break;
        case "End":
          next = list.length - 1;
          break;
        default:
          return;
      }
      e.preventDefault();
      focusItem(list[next]);
    },
    { signal },
  );

  // Bubbles after the item's own handler, so a handler that moves focus elsewhere (an app opening
  // its tab) is respected by the "if-inside" rule.
  menu.addEventListener(
    "click",
    (e) => {
      if (e.target instanceof Element && e.target.closest(ITEMS)) close("if-inside");
    },
    { signal },
  );

  document.addEventListener(
    "click",
    (e) => {
      if (!isOpen()) return;
      const target = e.target instanceof Node ? e.target : null;
      if (menu.contains(target) || trigger.contains(target)) return;
      close("if-inside");
    },
    { signal },
  );

  return () => listeners.abort();
}
