// relative-time.ts - render the provenance timestamps as relative ("3 days ago").
//
// The "Last updated" line and every "Recent changes on this page" row carry a
// <time datetime="..."> with an absolute rendered date. We swap the visible text
// for a relative phrase and move the absolute form into the title, so hovering
// still shows the exact date. Done client-side ON PURPOSE: a build-time relative
// date would change every day and trip the render's drift gate. No-ops where there
// is no provenance strip.

export function initRelativeTime(): void {
  const times = document.querySelectorAll(".page-provenance time[datetime]");
  if (!times.length) return;

  const MIN = 60, HOUR = 3600, DAY = 86400, WEEK = 604800, MONTH = 2629800, YEAR = 31557600;

  function relative(then: number, now: number): string {
    const s = Math.round((now - then) / 1000);
    if (s < 45) return "just now";
    if (s < 90) return "a minute ago";
    if (s < HOUR) return Math.round(s / MIN) + " minutes ago";
    if (s < 90 * MIN) return "an hour ago";
    if (s < DAY) return Math.round(s / HOUR) + " hours ago";
    if (s < 2 * DAY) return "yesterday";
    if (s < WEEK) return Math.round(s / DAY) + " days ago";
    if (s < 2 * WEEK) return "a week ago";
    if (s < MONTH) return Math.round(s / WEEK) + " weeks ago";
    if (s < 2 * MONTH) return "a month ago";
    if (s < YEAR) return Math.round(s / MONTH) + " months ago";
    if (s < 2 * YEAR) return "a year ago";
    return Math.round(s / YEAR) + " years ago";
  }

  const now = Date.now();
  times.forEach((t) => {
    const iso = t.getAttribute("datetime");
    if (!iso) return;
    const d = new Date(iso);
    if (isNaN(d.getTime())) return;
    // Keep the absolute date reachable on hover; don't clobber an existing title.
    if (!t.getAttribute("title")) t.setAttribute("title", t.textContent ?? "");
    t.textContent = relative(d.getTime(), now);
  });
}
