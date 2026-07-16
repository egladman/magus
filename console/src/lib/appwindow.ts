// appwindow.ts - open a console surface in its own dedicated window ("app mode"). Shared by the
// title-bar app drawer (ui/app-menu.ts) and the launcher-card kebab (console/home.ts) so the
// "open in a new window" behavior is defined exactly once. The window loads index.html?app=<id>,
// which main.ts boots as a single-surface window with the tab strip hidden; "popup" strips the
// browser's tab/URL chrome, and an installed PWA promotes it to a standalone app window. A stable
// per-surface window name focuses the existing window on a repeat open instead of stacking copies.
export function openSurfaceWindow(pageId: string): void {
  const url = location.pathname + "?app=" + encodeURIComponent(pageId);
  window.open(url, "magus-app-" + pageId, "popup,width=1180,height=800");
}
