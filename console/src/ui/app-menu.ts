// app-menu.ts - the title-bar Applications menu: a PatternFly Menu popover that links back to the
// sibling magus web apps (the documentation site and the playground). It mirrors the settings gear's
// popover wiring (open/close, aria-expanded, click-outside, Escape, focus return) so the two title-bar
// popovers behave identically. No-ops without the #console-appmenu-btn / #console-appmenu markup. The
// menu items are plain links, so selecting one navigates to that app; this only manages visibility+a11y.
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
