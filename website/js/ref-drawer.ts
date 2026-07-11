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
  const setOpen = (open: boolean): void => {
    drawer.classList.toggle("open", open);
    backdrop.classList.toggle("open", open);
    drawer.setAttribute("aria-hidden", open ? "false" : "true");
    triggers.forEach((t) => t.setAttribute("aria-expanded", open ? "true" : "false"));
  };

  triggers.forEach((t) => t.addEventListener("click", () => setOpen(!drawer.classList.contains("open"))));
  const closeBtn = drawer.querySelector(".ref-drawer-close");
  if (closeBtn) closeBtn.addEventListener("click", () => setOpen(false));
  backdrop.addEventListener("click", () => setOpen(false));
  document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key === "Escape" && drawer.classList.contains("open")) setOpen(false);
  });
})();
