// appwindow.ts - open a console surface in its own dedicated window ("app mode"). ONE caller by design:
// the tab context menu's "Move to new window" (tabBar.ts -> main.ts). Leaving this window is always an
// explicit act on a tab you already have; nothing that merely opens an app (the launcher cards, the
// title-bar app drawer) may spawn one, so a stray click can never strand you in a window you did not ask
// for. The window loads index.html?app=<id>, which main.ts boots as a single-surface window with the tab
// bar hidden; "popup" strips the browser's tab/URL chrome, and an installed PWA promotes it to a
// standalone app window. A stable per-surface window name focuses the existing window on a repeat open
// instead of stacking copies.
export function openSurfaceWindow(pageId: string): void {
  const url = location.pathname + "?app=" + encodeURIComponent(pageId);
  window.open(url, "magus-app-" + pageId, "popup,width=1180,height=800");
}
