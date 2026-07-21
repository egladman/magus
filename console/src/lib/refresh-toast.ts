// refresh-toast.ts - a small bottom-left "reload" prompt shared by service-worker-register
// (a newer version of the assets is available) and console-settings (a browser-side pref
// changed and takes effect on reload). Idempotent: a second call while a toast is already up
// is a no-op, so one Refresh button covers overlapping reasons to reload. Styled by .console-shell-toast
// in overrides.css.
//
// Every toast carries a SOURCE (the surface or feature that raised it) - required, not optional, so a new
// caller cannot forget it. A toast fires globally in the bottom-left corner, so it can appear while a
// different tab is active; the source chip tells you where it came from in the moment, and it rides into
// the notification history for the same reason. See lib/notifications.ts.
import { notify, type NotifyLink } from "./notifications";

export function showRefreshToast(source: string, message: string): void {
  if (document.querySelector(".console-shell-toast")) return;
  const toast = document.createElement("div");
  toast.className = "console-shell-toast";
  toast.setAttribute("role", "status");
  toast.append(sourceChip(source));

  const msg = document.createElement("span");
  msg.textContent = message;
  toast.appendChild(msg);

  const refresh = document.createElement("button");
  refresh.type = "button";
  refresh.textContent = "Refresh";
  refresh.addEventListener("click", () => {
    location.reload();
  });
  toast.appendChild(refresh);

  document.body.appendChild(toast);
  // Record it too, so a reload prompt dismissed (or reloaded past) is still in the history.
  notify({ source, message, kind: "ok" });
}

// Options for a transient toast: how long it lingers, an optional deep link rendered as an action, and
// an optional dedupe key forwarded to the notification history.
export interface ToastOptions {
  ms?: number;
  link?: NotifyLink | string;
  key?: string;
}

// showToast pops a transient, auto-dismissing toast in the same bottom-left slot as the reload prompt:
// a Settings save/apply confirmation ("ok"), a "warn" (a partial success worth reading, e.g. an import
// that dropped some keys), or an "error" explaining why something failed. A fresh call replaces any
// transient toast already up, so rapid calls do not stack. Errors and warnings linger longer than
// confirmations - they have to be read. Styled by .console-shell-toast in overrides.css.
//
// `source` (required) names where the toast came from and is rendered as a quiet chip; write the message
// to stand alone (what happened + what to do next), and let the chip carry the "where" rather than
// repeating it in prose. Every toast is ALSO recorded into the notification history (the title-bar bell's
// panel) so a toast you missed while it auto-dismissed is still there to read later. The toast's own
// timing is unchanged; recording is a side effect. An optional deep link is rendered as an action on
// BOTH the toast and the recorded entry. Only error-kind toasts light the bell's unseen-dot (see
// notifications.ts).
export function showToast(source: string, message: string, kind: "ok" | "warn" | "error" = "ok", opts: ToastOptions = {}): void {
  const ms = opts.ms ?? (kind === "ok" ? 2600 : 6000);
  document.querySelector(".console-shell-toast--transient")?.remove();
  const toast = document.createElement("div");
  toast.className = "console-shell-toast console-shell-toast--transient";
  toast.dataset.kind = kind;
  // Only a hard error is assertive; a warn is a passive status the operator can read at leisure.
  toast.setAttribute("role", kind === "error" ? "alert" : "status");
  toast.append(sourceChip(source));
  const msg = document.createElement("span");
  msg.textContent = message;
  toast.appendChild(msg);
  // A deep link becomes an action button on the transient toast; clicking navigates (location.assign).
  const link = normalizeLink(opts.link);
  if (link) {
    const action = document.createElement("button");
    action.type = "button";
    action.textContent = link.label;
    action.addEventListener("click", () => { if (link.run) void link.run(); else if (link.href) location.assign(link.href); });
    toast.appendChild(action);
  }
  document.body.appendChild(toast);
  setTimeout(() => toast.remove(), ms);

  // Record it in the history. Toasts are the transient face of a signal; the bell is its scrollback.
  notify({ source, message, kind, link: opts.link, key: opts.key });
}

// sourceChip builds the quiet, tag-like source label prepended to a toast (lower-case, not all-caps,
// muted) - matching the console's chip style and the same source shown on the history entry.
function sourceChip(source: string): HTMLElement {
  const chip = document.createElement("span");
  chip.className = "console-shell-toast__source";
  chip.textContent = source;
  return chip;
}

function normalizeLink(link: NotifyLink | string | undefined): NotifyLink | undefined {
  if (!link) return undefined;
  if (typeof link === "string") return link ? { label: "Open", href: link } : undefined;
  return link.href ? { label: link.label || "Open", href: link.href } : undefined;
}
