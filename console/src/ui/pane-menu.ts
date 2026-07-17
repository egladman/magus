// pane-menu.ts - the title-bar Panes menu: split and close the tiled panes from the UI. Tiling was
// chord-only, so on a phone (no keyboard) it was unreachable entirely. Each item dispatches the SAME
// command id its keybinding does, so the action stays defined once, and the effective chord is stamped in
// so the menu teaches the shortcut rather than replacing it. Focus is deliberately not listed: a pane is
// focused by tapping/clicking it, which already works on touch. Mirrors initAppMenu's popover wiring
// (open/close, aria-expanded, Escape, click-outside). No-ops without the markup.
import { dispatchCommand, listCommands } from "../console/commands";

export function initPaneMenu(chordFor: (commandId: string) => string): void {
  const btn = document.getElementById("console-panes-btn");
  const panel = document.getElementById("console-panesmenu");
  if (!btn || !panel) return;

  // Labels come from the command registry, not the markup, so this menu and the command bar can never
  // drift apart or mislabel an action - "Split pane" is adaptive (it picks the pane's long axis, so it
  // splits down on a phone), which a hardcoded "Split right" would have lied about.
  const labels = new Map(listCommands().map((c) => [c.id, c.label]));

  let open = false;
  const render = (): void => {
    panel.hidden = !open;
    btn.setAttribute("aria-expanded", open ? "true" : "false");
  };
  const setOpen = (v: boolean, restoreFocus = false): void => {
    open = v;
    render();
    if (v) panel.querySelector<HTMLElement>("button")?.focus();
    else if (restoreFocus) btn.focus();
  };

  for (const item of panel.querySelectorAll<HTMLElement>("[data-command]")) {
    const id = item.dataset.command;
    if (!id) continue;
    const label = labels.get(id);
    const text = item.querySelector<HTMLElement>(".pf-v6-c-menu__item-text");
    if (text && label) text.textContent = label;
    const slot = item.querySelector<HTMLElement>("[data-chord]");
    if (slot) slot.textContent = chordFor(id);
    // Close WITHOUT restoring focus to the toggle: the command itself takes focus (a split focuses the
    // new pane), and stealing it back would undo that.
    item.addEventListener("click", () => { setOpen(false); dispatchCommand(id); });
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
