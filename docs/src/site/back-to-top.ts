// back-to-top.ts - a floating "back to top" button that appears once the reader
// has scrolled past the fold, then smoothly scrolls back on click. Pairs nicely
// with the mobile TOC bottom-sheet (both sit in the bottom-right / bottom safe
// area) but is a pure enhancement: with JS off, the button is never inserted.

export function initBackToTop(): void {
  if (typeof window === "undefined") return;

  // One button, kept out of the DOM until it's needed.
  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "back-to-top";
  btn.setAttribute("aria-label", "Back to top");
  btn.title = "Back to top";
  btn.setAttribute("data-tooltip", "Back to top");
  btn.setAttribute("data-placement", "left");
  btn.innerHTML =
    '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<path d="M12 19V5"></path><path d="m5 12 7-7 7 7"></path></svg>';
  btn.addEventListener("click", function () {
    const reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    window.scrollTo({ top: 0, behavior: reduce ? "auto" : "smooth" });
  });
  document.body.appendChild(btn);

  // Show/hide off the same passive scroll listener: cheap, coalesced by rAF.
  let ticking = false;
  function apply(): void {
    ticking = false;
    // 400px keeps the button hidden on short pages that fit in the viewport.
    if (window.scrollY > 400) btn.classList.add("visible");
    else btn.classList.remove("visible");
  }
  window.addEventListener("scroll", function () {
    if (!ticking) { requestAnimationFrame(apply); ticking = true; }
  }, { passive: true });
  apply();
}
