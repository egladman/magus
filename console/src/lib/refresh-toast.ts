// refresh-toast.ts - the toasts the console raises for itself: a "reload" prompt shared by
// service-worker-register (a newer version of the assets is available) and console-settings (a
// browser-side pref changed and takes effect on reload), a countdown to a reload, and showToast for
// everything else. All of them are PatternFly Alerts in the toast group (lib/toast.ts).
//
// Every toast carries a SOURCE (the app or feature that raised it) - required, not optional, so a new
// caller cannot forget it. A toast fires globally, so it can appear while a different tab is active;
// the source is the alert's title and rides into the notification history for the same reason. See
// lib/notifications.ts.
import { notify, type NotifyLink } from "./notifications";
import { pushToast, renderTransientToast } from "./toast";

const REFRESH_KEY = "console-refresh-prompt";
const COUNTDOWN_KEY = "console-refresh-countdown";

// showRefreshToast asks the reader to reload and waits. Idempotent: a second call while the prompt is
// up is a no-op, so one Refresh button covers overlapping reasons to reload. It stays until closed.
export function showRefreshToast(source: string, message: string): void {
  if (document.querySelector(`[data-toast-key="${REFRESH_KEY}"]`)) return;
  pushToast({
    source,
    message,
    kind: "info",
    ms: 0,
    key: REFRESH_KEY,
    actions: [{ label: "Refresh", run: () => location.reload() }],
  });
  // Record it too, so a reload prompt dismissed (or reloaded past) is still in the history.
  notify({ source, message, kind: "ok" });
}

// showCountdownToast announces something that is ABOUT TO HAPPEN, counts down to it, and then does
// it - as opposed to showRefreshToast above, which asks and waits.
//
// The two exist because there are two situations and only one of them has a person in it. At a
// keyboard, a page that reloads itself out from under you is hostile: it can lose a scroll position,
// a half-read log, a filter you just typed, so asking is right. On an unattended display - Big
// Picture on a TV or a spare monitor - "click to refresh" is a prompt nobody will ever click, and
// the screen simply sits on a stale build indefinitely, which is the failure this replaces.
//
// It still ANNOUNCES rather than acting silently: anyone who happens to be looking gets told what
// is about to happen and roughly when, and Cancel is there for the case where someone is in fact
// standing at the screen. Closing the toast cancels too. Cancelling is remembered by the caller,
// not here.
//
// Returns a canceler so the caller can call the whole thing off (leaving the mode, say).
export function showCountdownToast(
  source: string,
  message: (secondsLeft: number) => string,
  seconds: number,
  onElapsed: () => void,
): () => void {
  // A countdown already up is closed first, which stops its timer through onDismiss.
  document
    .querySelector<HTMLElement>(`[data-toast-key="${COUNTDOWN_KEY}"] .pf-v6-c-alert__action button`)
    ?.click();
  let left = Math.max(1, Math.round(seconds));
  let timer = 0;
  const halt = (): void => {
    if (timer) window.clearInterval(timer);
    timer = 0;
  };
  const toast = pushToast({
    source,
    message: message(left),
    kind: "info",
    ms: 0,
    key: COUNTDOWN_KEY,
    actions: [{ label: "Cancel", run: () => toast.dismiss() }],
    onDismiss: halt,
  });

  // Tick the text in place rather than re-calling showToast each second: that would replace the
  // element (losing the Cancel button mid-press) and record a fresh notification-history entry for
  // every tick, turning one event into ten.
  timer = window.setInterval(() => {
    left -= 1;
    if (left <= 0) {
      toast.dismiss();
      onElapsed();
      return;
    }
    toast.setMessage(message(left));
  }, 1000);

  // Recorded ONCE, at announcement time, so the history says a refresh was scheduled even though
  // the reload that follows wipes the page.
  notify({ source, message: message(left), kind: "ok" });
  return () => toast.dismiss();
}

// Options for a toast: how long it lingers, an optional deep link rendered as an action, and
// an optional dedupe key forwarded to the notification history.
export interface ToastOptions {
  ms?: number;
  link?: NotifyLink | string;
  key?: string;
}

// showToast pops a toast on top of the stack: a Settings save/apply confirmation ("ok"), a "warn" (a
// partial success worth reading, e.g. an import that dropped some keys), or an "error" explaining why
// something failed. It dismisses itself after 8s, unless the pointer rests on it or focus is inside,
// and always has a close button.
//
// `source` (required) names where the toast came from and is the alert's title; write the message to
// stand alone (what happened + what to do next), and let the title carry the "where" rather than
// repeating it in prose. Every toast is ALSO recorded into the notification history (the title-bar bell's
// panel) so a toast you missed while it auto-dismissed is still there to read later. The toast's own
// timing is unchanged; recording is a side effect. An optional deep link is rendered as an action on
// BOTH the toast and the recorded entry. Only error-kind toasts light the bell's unseen-dot (see
// notifications.ts).
export function showToast(
  source: string,
  message: string,
  kind: "ok" | "warn" | "error" = "ok",
  opts: ToastOptions = {},
): void {
  const link = typeof opts.link === "string" ? { label: "Open", href: opts.link } : opts.link;
  renderTransientToast(source, message, kind, link, opts.ms);
  // Record it in the history. Toasts are the transient face of a signal; the bell is its scrollback.
  notify({ source, message, kind, link: opts.link, key: opts.key });
}
