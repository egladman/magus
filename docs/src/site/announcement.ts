// announcement.js - reveal (and dismiss) the release-announcement bar per version.
//
// The bar (rendered by announcementBar() in the magusfile) carries the release
// version in data-version. It is HIDDEN by default under .js (see site.css); this
// script REVEALS it only when the reader has not dismissed the current version, so a
// returning dismisser never sees it flash in and back out. On close we remember the
// version in localStorage and re-hide - until a newer release changes the version,
// when the bar reveals again. No-ops when the bar is absent (no release shipped); when
// localStorage is unavailable the bar simply always reveals.
//
// The bar also EXPIRES: it reveals only while the release is recent (data-released,
// within ANNOUNCE_DAYS). An announcement that never goes away stops being an
// announcement, and the version-based dismissal alone could not solve that - a reader
// who never clicks the close button saw the same bar until the next release, however
// many months that took. An unreadable or absent date reveals nothing: for a
// decorative banner, "cannot tell how old this is" should fail quiet.

/** Days after a release during which the announcement bar still shows. */
const ANNOUNCE_DAYS = 7;

function isRecent(released: string): boolean {
  const at = Date.parse(released);
  if (Number.isNaN(at)) return false;
  // A future date (the reader's clock is behind, or a timezone edge) counts as recent
  // rather than as infinitely old, so skew hides the bar late instead of never showing it.
  return Date.now() - at < ANNOUNCE_DAYS * 24 * 60 * 60 * 1000;
}

export function initAnnouncement(): void {
  const bar = document.getElementById("announcement-bar");
  if (!bar) return;

  const version = bar.getAttribute("data-version") || "";
  const KEY = "announcement-dismissed";

  let dismissed = false;
  try {
    dismissed = localStorage.getItem(KEY) === version;
  } catch {}
  // Reveal only when this version is undismissed. Hidden-by-default + opt-in reveal is
  // the inverse of the old "show then hide", so nothing paints that will be taken away.
  const recent = isRecent(bar.getAttribute("data-released") || "");
  if (!dismissed && recent) bar.classList.add("is-shown");

  const close = bar.querySelector(".announcement-close");
  if (close) {
    close.addEventListener("click", function () {
      bar.classList.remove("is-shown");
      try {
        localStorage.setItem(KEY, version);
      } catch {}
    });
  }
}
