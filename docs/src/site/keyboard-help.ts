// keyboard-help.js - a "?" keyboard-shortcut overlay.
//
// Press "?" (Shift-/) to open a native <dialog> listing the site's shortcuts:
// / or Cmd-K to focus search, ? for this help, Esc to close. Uses <dialog>'s
// built-in focus trap + backdrop, so no bespoke a11y wiring. Absent-but-
// harmless without JS.
//
// A keyboard shortcut is the only way in for readers who already know it exists,
// which is nobody on a first visit, so the overlay also gets a button in the
// floating stack - shown at the foot of the page, where a reader who has run out
// of content is the most likely to want somewhere else to go.

import { floatingActions } from "./floating-actions.js";

export function initKeyboardHelp(): void {
  if (typeof document === "undefined") return;

  // Build the dialog once, inserted lazily on first open so pages that never
  // hit it stay lighter.
  let dialog: HTMLDialogElement | null = null;
  function ensure(): HTMLDialogElement {
    if (dialog) return dialog;
    const d = document.createElement("dialog");
    d.className = "shortcut-help";
    d.innerHTML =
      "<article>" +
      // Pico's own dismiss control: `.close` inside a dialog article draws the
      // --pico-icon-close glyph, centered, dimmed until hover. No markup or CSS
      // of ours to keep centered.
      "<header><h2>Keyboard shortcuts</h2>" +
      '<button type="button" aria-label="Close" class="shortcut-close close"></button></header>' +
      "<dl>" +
      "<dt><kbd>/</kbd> or <kbd>&#8984;K</kbd></dt><dd>Focus search</dd>" +
      "<dt><kbd>?</kbd></dt><dd>Show this help</dd>" +
      "<dt><kbd>Esc</kbd></dt><dd>Close overlay</dd>" +
      "</dl>" +
      "</article>";
    document.body.appendChild(d);
    d.querySelector(".shortcut-close")?.addEventListener("click", function () {
      d.close();
    });
    // Click-outside closes: <dialog> renders a full-viewport backdrop; a click
    // on the dialog itself (not its inner article) means the backdrop was hit.
    d.addEventListener("click", function (e) {
      if (e.target === d) d.close();
    });
    dialog = d;
    return d;
  }

  function open(): void {
    const d = ensure();
    if (typeof d.showModal === "function") d.showModal();
    else d.setAttribute("open", "");
  }

  document.addEventListener("keydown", function (e: KeyboardEvent) {
    // Ignore key events inside inputs so authors typing "?" in the search box
    // don't open the overlay.
    const target = e.target as HTMLElement | null;
    if (/^(INPUT|TEXTAREA|SELECT)$/.test(target?.tagName || "")) return;
    if (e.key === "?" && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      open();
    }
  });

  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "keyboard-help-trigger";
  btn.setAttribute("aria-label", "Keyboard shortcuts");
  btn.title = "Keyboard shortcuts";
  btn.setAttribute("data-tooltip", "Keyboard shortcuts");
  btn.setAttribute("data-placement", "left");
  // Lucide keyboard, matching the 18px/1.8 stroke of the rest of the icon set.
  btn.innerHTML =
    '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<rect width="20" height="16" x="2" y="4" rx="2"></rect>' +
    '<path d="M6 8h.01"></path><path d="M10 8h.01"></path><path d="M14 8h.01"></path>' +
    '<path d="M18 8h.01"></path><path d="M8 12h.01"></path><path d="M12 12h.01"></path>' +
    '<path d="M16 12h.01"></path><path d="M7 16h10"></path></svg>';
  btn.addEventListener("click", open);
  floatingActions().appendChild(btn);
}
