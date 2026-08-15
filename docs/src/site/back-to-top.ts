// back-to-top.ts - a floating "back to top" button that appears once the reader
// has scrolled past the fold, then smoothly scrolls back on click. A pure
// enhancement: with JS off, the button is never inserted.

import { floatingActions } from "./floating-actions.js";

export function initBackToTop(): void {
  if (typeof window === "undefined") return;

  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "back-to-top";
  btn.setAttribute("aria-label", "Back to top");
  btn.title = "Back to top";
  btn.setAttribute("data-tooltip", "Back to top");
  btn.setAttribute("data-placement", "left");
  // Lucide arrow-up-to-line, at the 18px/1.8 stroke the nav icon buttons use. The
  // bar is what distinguishes "jump to the top" from a plain arrow's "scroll up".
  btn.innerHTML =
    '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<path d="M5 3h14"></path><path d="m18 13-6-6-6 6"></path><path d="M12 7v14"></path></svg>';
  btn.addEventListener("click", function () {
    const reduce =
      window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    window.scrollTo({ top: 0, behavior: reduce ? "auto" : "smooth" });
  });

  // First into the stack, so this keeps the corner slot and never shifts when a
  // control above it appears. Visibility is CSS, off the container's .past-fold.
  floatingActions().appendChild(btn);
}
