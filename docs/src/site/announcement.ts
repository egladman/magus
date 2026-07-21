// announcement.js - reveal (and dismiss) the release-announcement bar per version.
//
// The bar (rendered by announcementBar() in the magusfile) carries the release
// version in data-version. It is HIDDEN by default under .js (see site.css); this
// script REVEALS it only when the reader has not dismissed the current version, so a
// returning dismisser never sees it flash in and back out. On close we remember the
// version in localStorage and re-hide - until a newer release changes the version,
// when the bar reveals again. No-ops when the bar is absent (no release shipped); when
// localStorage is unavailable the bar simply always reveals.

export function initAnnouncement(): void {
  const bar = document.getElementById("announcement-bar");
  if (!bar) return;

  const version = bar.getAttribute("data-version") || "";
  const KEY = "announcement-dismissed";

  let dismissed = false;
  try { dismissed = localStorage.getItem(KEY) === version; } catch {}
  // Reveal only when this version is undismissed. Hidden-by-default + opt-in reveal is
  // the inverse of the old "show then hide", so nothing paints that will be taken away.
  if (!dismissed) bar.classList.add("is-shown");

  const close = bar.querySelector(".announcement-close");
  if (close) {
    close.addEventListener("click", function () {
      bar.classList.remove("is-shown");
      try { localStorage.setItem(KEY, version); } catch {}
    });
  }
}
