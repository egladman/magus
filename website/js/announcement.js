// announcement.js - dismiss the release-announcement bar per version.
//
// The bar (rendered by announcementBar() in the magusfile) carries the release
// version in data-version. Once the reader closes it, we remember that version in
// localStorage and keep it hidden - until a newer release changes the version, when
// the bar reappears. No-ops when the bar is absent (no release shipped) or when
// localStorage is unavailable (the bar just stays until navigation).

(function () {
  var bar = document.getElementById("announcement-bar");
  if (!bar) return;

  var version = bar.getAttribute("data-version") || "";
  var KEY = "announcement-dismissed";

  try {
    if (localStorage.getItem(KEY) === version) {
      bar.hidden = true;
      return;
    }
  } catch (e) {}

  var close = bar.querySelector(".announcement-close");
  if (close) {
    close.addEventListener("click", function () {
      bar.hidden = true;
      try { localStorage.setItem(KEY, version); } catch (e) {}
    });
  }
})();
