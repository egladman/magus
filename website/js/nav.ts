// Mobile nav menu. The section links collapse behind a plain <button> (styled by Pico
// as an ordinary .outline button, so it needs none of the overrides a <details> would);
// this toggles the dropdown open/closed. No-ops on desktop (the button is display:none)
// and, since it only shows under .js, no-JS visitors keep the links inline instead.
(function () {
  const btn = document.querySelector(".nav-toggle");
  const right = document.querySelector(".nav-right");
  if (!btn || !right) return;

  // A const arrow (not a hoisted function declaration) so the null-guard above
  // narrows btn/right to non-null inside it.
  const setOpen = (open: boolean): void => {
    right.classList.toggle("nav-open", open);
    btn.setAttribute("aria-expanded", open ? "true" : "false");
    // Track the icon swap (hamburger -> X) so the label names the action the
    // button now performs, not a fixed "Menu".
    btn.setAttribute("aria-label", open ? "Close menu" : "Open menu");
  };

  btn.addEventListener("click", function () {
    setOpen(!right.classList.contains("nav-open"));
  });
  // Dismiss on an outside click, Escape, or after following a link in the menu.
  document.addEventListener("click", function (e: MouseEvent) {
    if (right.classList.contains("nav-open") && !right.contains(e.target as Node)) setOpen(false);
  });
  document.addEventListener("keydown", function (e: KeyboardEvent) {
    if (e.key === "Escape") setOpen(false);
  });
  right.addEventListener("click", function (e: Event) {
    if ((e.target as Element).closest(".nav-links a")) setOpen(false);
  });
})();
