// keyboard-help.js - a "?" keyboard-shortcut overlay.
//
// Press "?" (Shift-/) to open a native <dialog> listing the site's shortcuts:
// / or Cmd-K to focus search, ? for this help, Esc to close. Uses <dialog>'s
// built-in focus trap + backdrop, so no bespoke a11y wiring. Absent-but-
// harmless without JS.

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
      '<article>' +
        '<header><h2>Keyboard shortcuts</h2>' +
        '<button type="button" aria-label="Close" class="shortcut-close">&times;</button></header>' +
        '<dl>' +
          '<dt><kbd>/</kbd> or <kbd>&#8984;K</kbd></dt><dd>Focus search</dd>' +
          '<dt><kbd>?</kbd></dt><dd>Show this help</dd>' +
          '<dt><kbd>Esc</kbd></dt><dd>Close overlay</dd>' +
        '</dl>' +
      '</article>';
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

  document.addEventListener("keydown", function (e: KeyboardEvent) {
    // Ignore key events inside inputs so authors typing "?" in the search box
    // don't open the overlay.
    const target = e.target as HTMLElement | null;
    if (/^(INPUT|TEXTAREA|SELECT)$/.test(target?.tagName || "")) return;
    if (e.key === "?" && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();
      const d = ensure();
      if (typeof d.showModal === "function") d.showModal();
      else d.setAttribute("open", "");
    }
  });
}
