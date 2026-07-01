// clipboard.js - shared "copy to clipboard, then confirm on the control" affordance.
//
// Both the code-block copy buttons (copy.js) and the section-link anchors
// (anchors.js) flash the same confirmation: swap the control's icon to a check,
// mark it .copied, update its aria-label, and revert after 2s via a per-element
// timer. That logic (and the check icon) lived inline in both files and had drifted;
// it now lives here once. Exposes window.copyFeedback and is loaded before its
// callers so the global exists when they run.
(function () {
  // The confirmation icon every copy control shares. No width/height attributes:
  // each caller's CSS sizes svgs within it (.code-copy svg, .heading-anchor svg),
  // so one icon string fits both the 16px button and the em-sized anchor.
  var CHECK =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<path d="M20 6 9 17l-5-5"></path></svg>';

  // copyFeedback wires one control (a button or anchor). opts:
  //   el        the element to flash
  //   getText   () => string to copy, read lazily at click time
  //   restIcon  innerHTML for the idle state (the caller's own icon)
  //   restLabel aria-label for the idle state
  //   doneLabel aria-label after a successful copy
  //   failLabel aria-label after a failed copy (optional; omit to leave the label)
  // Returns false without wiring anything when the Clipboard API is unavailable,
  // so a caller can fall back to (or skip) the control as it sees fit.
  window.copyFeedback = function (opts) {
    if (!navigator.clipboard) return false;
    var el = opts.el, timer = null;
    function reset() {
      el.innerHTML = opts.restIcon;
      el.classList.remove("copied");
      el.setAttribute("aria-label", opts.restLabel);
    }
    // Both outcomes revert through reset() after 2s so the success and failure
    // paths cannot drift apart (the old inline failure path forgot to restore).
    function flash(ok) {
      if (ok) {
        el.innerHTML = CHECK;
        el.classList.add("copied");
        el.setAttribute("aria-label", opts.doneLabel);
      } else if (opts.failLabel) {
        el.setAttribute("aria-label", opts.failLabel);
      }
      if (timer) clearTimeout(timer);
      timer = setTimeout(reset, 2000);
    }
    el.addEventListener("click", function () {
      navigator.clipboard.writeText(opts.getText()).then(
        function () { flash(true); },
        function () { flash(false); }
      );
    });
    return true;
  };
})();
