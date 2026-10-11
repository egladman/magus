// time.ts - the one way the render apps write an instant. A run list, the Runs page, the Activity
// index and an event's head used to format time three ways (relative, a bare HH:MM:SS, and "12s"),
// so the same moment read differently one tab over. This module has no DOM dependency except
// timeEl, so the formatting is unit-tested in node.

const pad = (n: number): string => (n < 10 ? "0" + n : String(n));

// absTime is a dated wall-clock instant: "Oct 8, 14:02". The date is in it because a clock time
// alone is ambiguous on any run older than a day.
export function absTime(ms: number): string {
  const d = new Date(ms);
  const month = d.toLocaleString("en-US", { month: "short" });
  return month + " " + d.getDate() + ", " + pad(d.getHours()) + ":" + pad(d.getMinutes());
}

// relTime writes an instant as "how long ago" while that is a useful reading, and as a dated
// absolute time once it is not. An hour is the line: "3h ago" asks the reader to do arithmetic to
// find when, where "Oct 8, 14:02" is already the answer. now is injected so the function is pure.
export function relTime(ms: number, now: number): string {
  const sec = Math.max(0, Math.round((now - ms) / 1000));
  if (sec < 60) return sec + "s ago";
  const min = Math.floor(sec / 60);
  if (min < 60) return min + "m ago";
  return absTime(ms);
}

// timeEl is a <time> element carrying the instant it was computed from. data-time lets
// tickRelativeTimes rewrite the text in place as it ages; datetime is the machine-readable form
// and the title the full instant, so a relative reading can be checked against the clock.
export function timeEl(ms: number, now: number, className?: string): HTMLTimeElement {
  const el = document.createElement("time");
  if (className) el.className = className;
  el.dateTime = new Date(ms).toISOString();
  el.dataset.time = String(ms);
  el.title = new Date(ms).toLocaleString();
  el.textContent = relTime(ms, now);
  return el;
}
