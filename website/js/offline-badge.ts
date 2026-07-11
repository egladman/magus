// offline-badge.ts - shows "offline - everything on this page is local" on the
// graph and playground pages when navigator.onLine is false, clearing it on the
// "online" event. Guards on its own #offline-badge element, so importing this
// unconditionally from main.js (every page) is a no-op everywhere else - the
// same pattern every other main.js module already follows.
//
// Distinct from graph-explorer.js's live-badge/snapshot-badge (data provenance:
// where the graph came from). This is network state, not data state - both can
// be true at once (an offline snapshot is the normal case for `magus graph open`
// without --serve), so they are separate elements, never merged into one.

(function () {
  if (typeof window === "undefined") return;
  const badge = document.getElementById("offline-badge");
  if (!badge) return;

  // A const arrow (not a hoisted function declaration) so the null-guard above
  // narrows badge to non-null inside it.
  const apply = (): void => {
    badge.hidden = navigator.onLine;
  };

  window.addEventListener("online", apply);
  window.addEventListener("offline", apply);
  apply();
})();
