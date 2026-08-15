// floating-actions.ts - the fixed bottom-right stack the floating page controls
// live in (back to top, keyboard shortcuts), plus the one scroll listener that
// drives all of them.
//
// Controls do not register their own scroll handlers. This module tracks the two
// positions the stack cares about and publishes them as classes on the container
// (.past-fold, .at-end); each control declares in CSS which of those it appears
// for. Adding a third control is then a stylesheet change, not another listener.

let container: HTMLElement | null = null;

// Returns the shared container, creating it on first call. Controls append
// themselves to it and are laid out bottom-up in append order, so the first
// caller owns the corner slot nearest the reader's thumb.
export function floatingActions(): HTMLElement {
  if (container) return container;

  const el = document.createElement("div");
  el.className = "floating-actions";
  document.body.appendChild(el);
  container = el;

  let ticking = false;
  function apply(): void {
    ticking = false;
    // 400px keeps the stack hidden on short pages that fit in the viewport.
    el.classList.toggle("past-fold", window.scrollY > 400);
    // 2px of slack: fractional zoom and subpixel layout leave the sum a hair
    // short of scrollHeight at the true bottom, so an equality test never fires.
    const bottom = window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 2;
    el.classList.toggle("at-end", bottom);
  }

  window.addEventListener(
    "scroll",
    function () {
      if (!ticking) {
        requestAnimationFrame(apply);
        ticking = true;
      }
    },
    { passive: true },
  );

  // Mermaid diagrams, highlighted code, and late-loading fonts all change the
  // page height after first paint. Without this, a reader already parked at the
  // bottom keeps .at-end after the content grows out from under them.
  if (typeof ResizeObserver === "function") {
    new ResizeObserver(function () {
      requestAnimationFrame(apply);
    }).observe(document.body);
  }

  apply();
  return el;
}
