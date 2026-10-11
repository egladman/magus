// scroll.ts - what a scroller needs to be usable without a pointer. Kept apart from the tiles so
// the Jobs view can use it without importing the board.

// scrollRegion makes a scroller reachable and named. An element that scrolls but cannot take focus
// leaves a keyboard reader with no way to move it, and an unnamed one is announced as nothing.
export function scrollRegion(el: HTMLElement, label: string): void {
  el.tabIndex = 0;
  el.setAttribute("role", "region");
  el.setAttribute("aria-label", label);
}
