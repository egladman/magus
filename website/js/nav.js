// Mobile nav menu. The section links collapse behind a plain <button> (styled by Pico
// as an ordinary .outline button, so it needs none of the overrides a <details> would);
// this toggles the dropdown open/closed. No-ops on desktop (the button is display:none)
// and, since it only shows under .js, no-JS visitors keep the links inline instead.
(function () {
  var btn = document.querySelector(".nav-toggle");
  var right = document.querySelector(".nav-right");
  if (!btn || !right) return;

  function setOpen(open) {
    right.classList.toggle("nav-open", open);
    btn.setAttribute("aria-expanded", open ? "true" : "false");
  }

  btn.addEventListener("click", function () {
    setOpen(!right.classList.contains("nav-open"));
  });
  // Dismiss on an outside click, Escape, or after following a link in the menu.
  document.addEventListener("click", function (e) {
    if (right.classList.contains("nav-open") && !right.contains(e.target)) setOpen(false);
  });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") setOpen(false);
  });
  right.addEventListener("click", function (e) {
    if (e.target.closest(".nav-links a")) setOpen(false);
  });
})();
