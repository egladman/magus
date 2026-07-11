// anchors.ts - add a shareable link icon to each section heading.
//
// Every article heading already carries a slug id (from the markdown renderer).
// This appends a small chain-link anchor to each h2-h6 inside the article.
// Clicking it jumps to that section (via the plain href) and, through copyFeedback
// (lib/clipboard.js), copies the section's full URL, briefly swapping the link icon
// for a check to confirm. The icon stays hidden until its heading is hovered (see
// .heading-anchor in site.css); the href still works without the Clipboard API.
import { copyFeedback } from "./lib/clipboard.js";

(function () {
  const LINK =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"></path>' +
    '<path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"></path></svg>';

  document.querySelectorAll("article :is(h2, h3, h4, h5, h6)[id]").forEach((h) => {
    const a = document.createElement("a");
    a.className = "heading-anchor";
    a.href = "#" + h.id;
    a.setAttribute("aria-label", "Link to this section");
    a.setAttribute("title", "Copy link to this section");
    a.setAttribute("data-tooltip", "Copy link to this section");
    a.setAttribute("data-placement", "left");
    a.innerHTML = LINK;
    h.appendChild(a);

    // The href already updates the hash and scrolls; copyFeedback additionally
    // copies the section's absolute URL on click. Guarded so the anchor still
    // renders and navigates if the shared helper isn't present.
    if (copyFeedback) {
      copyFeedback({
        el: a,
        getText: () => location.origin + location.pathname + location.search + "#" + h.id,
        restIcon: LINK,
        restLabel: "Link to this section",
        doneLabel: "Link copied",
      });
    }
  });
})();
