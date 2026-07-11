// ref-drawer.ts - a right-side slide-out reference panel shared by the console apps (graph
// explorer, log viewer, ...). A page marks its reference blocks with .ref-section and supplies a
// trigger (.ref-trigger), the drawer (#ref-drawer) and a backdrop (#ref-backdrop). This relocates
// the sections into the drawer (so the same blocks serve as inline no-JS content AND as the drawer
// body - no duplicate markup) and wires open/close: the trigger, the close button, a backdrop
// click, and Escape. No-ops where there is no drawer, like every other main.js module.
(function () {
  const drawer = document.getElementById("ref-drawer");
  const backdrop = document.getElementById("ref-backdrop");
  if (!drawer || !backdrop) return;

  // Pull the injected docs search bar (.page-tools, built by search.js) up into the drawer first,
  // so it sits at the top - "quick search" lives in the reference panel, not the app's page body.
  // search.js is imported before this module (see main.ts) so the element already exists.
  const search = document.querySelector(".page-tools");
  if (search) drawer.appendChild(search);

  // Then relocate the page's reference sections, in document order. CSS hides them inline
  // (.js .ref-section) and reveals them once inside (#ref-drawer .ref-section).
  document.querySelectorAll(".ref-section").forEach((s) => drawer.appendChild(s));

  const triggers = document.querySelectorAll(".ref-trigger");
  const pinBtn = drawer.querySelector(".ref-pin");

  // Pinned state persists across pages (localStorage): pin it once and the panel stays docked as
  // you navigate between the console apps. A pinned panel docks beside the content (no dim); an
  // unpinned one is a temporary overlay with a dimming backdrop.
  const LS_PINNED = "magus-ref-pinned";
  let pinned = false;
  try { pinned = localStorage.getItem(LS_PINNED) === "1"; } catch { /* ignore */ }
  const savePinned = (v: boolean): void => {
    try { if (v) localStorage.setItem(LS_PINNED, "1"); else localStorage.removeItem(LS_PINNED); } catch { /* ignore */ }
  };

  let isOpen = pinned; // a pinned panel is open on load

  const render = (): void => {
    drawer.classList.toggle("open", isOpen);
    drawer.classList.toggle("pinned", isOpen && pinned);
    // The reflow (content shrinks to make room) and the un-dimmed view are the pinned mode.
    document.body.classList.toggle("ref-pinned", isOpen && pinned);
    // The backdrop only dims in overlay mode; a pinned panel sits beside the content, undimmed.
    backdrop.classList.toggle("open", isOpen && !pinned);
    drawer.setAttribute("aria-hidden", isOpen ? "false" : "true");
    triggers.forEach((t) => t.setAttribute("aria-expanded", isOpen ? "true" : "false"));
    if (pinBtn) pinBtn.setAttribute("aria-pressed", pinned ? "true" : "false");
  };

  const setOpen = (open: boolean): void => {
    isOpen = open;
    // Closing a pinned panel also unpins it, so it does not spring back on the next page.
    if (!open && pinned) { pinned = false; savePinned(false); }
    render();
  };
  const togglePin = (): void => {
    pinned = !pinned;
    savePinned(pinned);
    if (pinned) isOpen = true; // pinning docks it open
    render();
  };

  triggers.forEach((t) => t.addEventListener("click", () => setOpen(!isOpen)));
  const closeBtn = drawer.querySelector(".ref-drawer-close");
  if (closeBtn) closeBtn.addEventListener("click", () => setOpen(false));
  backdrop.addEventListener("click", () => setOpen(false));
  if (pinBtn) pinBtn.addEventListener("click", togglePin);
  document.addEventListener("keydown", (e: KeyboardEvent) => {
    // Escape closes only an overlay panel; a pinned panel stays put.
    if (e.key === "Escape" && isOpen && !pinned) setOpen(false);
  });

  // Apply the persisted state on load without animating the slide/reflow on every navigation.
  document.documentElement.classList.add("ref-instant");
  render();
  requestAnimationFrame(() => document.documentElement.classList.remove("ref-instant"));
})();
