// popups.ts - a tiny coordinator so only one popup-style menu is open at a time.
// The top bar carries several independent, self-managing overlays (the mobile nav
// menu, the settings gear panel, the reference drawer) and the content area adds the
// mobile table-of-contents sheet. Each owns its own outside-click / Escape dismissal,
// but none knew about the others, so opening one on top of an already-open one left
// both showing (e.g. the gear panel stacked over the open nav menu).
//
// Instead of wiring every pair together (N^2, and each import would couple two
// modules), each overlay registers a close callback here and calls notifyPopupOpen()
// the instant it opens; every OTHER registered overlay is asked to close. That is the
// "a context menu that loses focus closes" behavior, expressed once. No DOM
// assumptions: a controller whose page lacks its targets simply never registers, and
// one that never opens never notifies. close() must be safe to call when already
// closed (all our controllers no-op in that case).
export type Dismissable = { close: () => void };

const registered = new Set<Dismissable>();

// registerPopup adds a dismissable to the coordinator and returns an unregister fn.
export function registerPopup(d: Dismissable): () => void {
  registered.add(d);
  return (): void => {
    registered.delete(d);
  };
}

// notifyPopupOpen closes every registered popup except the one that just opened.
// Call it from a controller's open path, passing the same object it registered.
export function notifyPopupOpen(self: Dismissable): void {
  registered.forEach((d) => {
    if (d !== self) d.close();
  });
}
