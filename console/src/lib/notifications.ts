// notifications.ts - the console's notification center: a per-session history of noteworthy events
// with a title-bar bell (a red unseen-dot when an IMPORTANT one is waiting) and a pop-out history
// panel. In-memory ONLY: nothing is persisted, by explicit decision - a notification is a signal about
// THIS session's daemon, not a durable record (the activity trail and the dashboard are the durable,
// pull surfaces).
//
// ADMISSION DOCTRINE (the reason this module is deliberately small). A notification is PUSH - it
// interrupts. It earns that only when all three hold:
//   1. it needs a human decision or action,
//   2. it changes what you can trust about the workspace, and
//   3. it is not already on the surface you are looking at.
// Anything that fails one of those is PULL, and belongs on the dashboard or the activity trail, not
// here. Do NOT add notifications that merely report progress or restate what a surface already shows.
//
// TWO TIERS. The BELL tier is error-kind and lights the unseen-dot; it is reserved for the five things
// that are genuinely "stop and look":
//   - an unwatched run/target failure   -> deep-link: the log viewer at the failing ref
//   - a sandbox denial                  -> deep-link: the activity trail
//   - daemon health degraded/down       -> deep-link: the dashboard
//   - a drift / CheckClean violation    -> deep-link: the run output
//   - a target turning newly volatile   -> deep-link: the insight lens
// The HISTORY tier is ok/warn-kind and records SILENTLY (no dot): job completions, reconnects, a share
// created or expired, settings applied. It is there when you go looking, and never interrupts.
//
// Every toast (lib/refresh-toast.ts) is recorded here as a side effect, so the panel doubles as a
// scrollback of the transient toasts you may have missed. Toasts keep their own auto-dismiss timing;
// this only remembers them.
//
// CROSS-BUNDLE WIRING. Each console surface (logs, dashboard, activity, ...) is built as its OWN esbuild
// bundle, dynamically imported by URL, so a module-level singleton here would be a DIFFERENT object in
// each bundle. The shell therefore owns the one real store; every other bundle raises a notification by
// dispatching the NOTIFY_EVENT CustomEvent on `document` (see `notify`), which the shell's single
// listener funnels into that store. This mirrors how the surfaces already talk to the shell through the
// shared DOM (the #console-conn status dot, dispatchCommand) rather than through shared module state.

import { wireDrawerToggle } from "../ui/ref-drawer";

export type NotifyKind = "ok" | "warn" | "error";

// A deep link rendered as an action on both the transient toast and the history entry. Clicking it
// navigates (location.assign): a same-app fragment URL updates the view in place, an app-relative page
// URL (e.g. the log viewer at a ref) navigates there.
export interface NotifyLink {
  label: string;
  href: string;
}

// The caller-facing shape. `source` is REQUIRED (not optional-with-empty) so a new caller cannot forget
// to say WHERE a signal came from: it names the surface or feature that raised it ("Settings", "Log
// Viewer", "Dashboard", "Activity Trail", "Share") and is rendered as a quiet chip on both the transient
// toast and the history entry - a toast fires globally, so its origin matters in the moment too, not just
// in scrollback. `kind` defaults to "ok" (history tier). `link` may be a bare href string (labelled
// "Open") or a full {label, href}. `key` is the dedupe key: a notification whose key was already admitted
// this session is dropped, so a surface that re-detects the same event on every poll or re-render
// notifies only on the transition (see the store's dedupe). `at` is injectable for tests.
export interface NotifyInput {
  source: string;
  message: string;
  kind?: NotifyKind;
  link?: NotifyLink | string;
  key?: string;
  at?: number;
}

// One recorded notification. `seen` gates the bell's unseen-dot and is only ever false for error-kind
// entries (ok/warn are recorded already-seen, so they never light the dot).
export interface Notification {
  id: string;
  source: string;
  kind: NotifyKind;
  message: string;
  link?: NotifyLink;
  at: number;
  seen: boolean;
}

export interface NotificationStore {
  // notify admits an input, returning the created Notification, or null when it was deduped away.
  notify(input: NotifyInput): Notification | null;
  list(): Notification[]; // newest first
  unseenCount(): number;  // error-kind, not yet seen
  markAllSeen(): void;
  dismiss(id: string): void;
  clear(): void;
  subscribe(fn: () => void): () => void;
}

// The event a non-shell bundle dispatches to reach the shell's store; its detail is a NotifyInput.
export const NOTIFY_EVENT = "magus:notify";

// A hard cap so a long session cannot grow the history unbounded. Newest-first, so the oldest fall off
// the tail. Dedupe keys are NOT forgotten when an entry is trimmed (a trimmed transition still should
// not re-fire), which is fine: the key set is bounded by the number of distinct events in a session.
const MAX_HISTORY = 200;

function normalizeLink(link: NotifyLink | string | undefined): NotifyLink | undefined {
  if (!link) return undefined;
  if (typeof link === "string") return link ? { label: "Open", href: link } : undefined;
  return link.href ? { label: link.label || "Open", href: link.href } : undefined;
}

export function createNotificationStore(): NotificationStore {
  let items: Notification[] = []; // newest first
  const keys = new Set<string>(); // every dedupe key admitted this session
  const listeners = new Set<() => void>();
  let seq = 0;

  const emit = (): void => { for (const fn of listeners) fn(); };

  return {
    notify(input: NotifyInput): Notification | null {
      if (input.key && keys.has(input.key)) return null; // same event, already recorded
      if (input.key) keys.add(input.key);
      const kind = input.kind ?? "ok";
      const n: Notification = {
        id: "n" + (++seq),
        source: input.source,
        kind,
        message: input.message,
        link: normalizeLink(input.link),
        at: input.at ?? Date.now(),
        // Only the bell tier (error) starts unseen; history-tier entries are recorded already-seen so
        // they never light the dot.
        seen: kind !== "error",
      };
      items.unshift(n);
      if (items.length > MAX_HISTORY) items = items.slice(0, MAX_HISTORY);
      emit();
      return n;
    },
    list(): Notification[] { return items.slice(); },
    unseenCount(): number { return items.reduce((acc, n) => acc + (n.kind === "error" && !n.seen ? 1 : 0), 0); },
    markAllSeen(): void {
      let changed = false;
      for (const n of items) if (!n.seen) { n.seen = true; changed = true; }
      if (changed) emit();
    },
    dismiss(id: string): void {
      const before = items.length;
      items = items.filter((n) => n.id !== id);
      if (items.length !== before) emit();
    },
    clear(): void {
      if (items.length === 0) return;
      items = [];
      emit();
    },
    subscribe(fn: () => void): () => void { listeners.add(fn); return () => listeners.delete(fn); },
  };
}

// notify raises a notification from ANY bundle. It dispatches NOTIFY_EVENT on document; the shell's
// single listener (installed by mountNotificationCenter) records it into the one store. Calling this
// from the shell bundle works too - the listener is on document, which every bundle shares.
export function notify(input: NotifyInput): void {
  if (typeof document === "undefined") return;
  document.dispatchEvent(new CustomEvent<NotifyInput>(NOTIFY_EVENT, { detail: input }));
}

// ---- the bell + history panel (shell-only) ---------------------------------

export interface NotificationCenter {
  store: NotificationStore;
  open(): void;
  close(): void;
  toggle(): void;
  // seedDemo drops a few history-tier entries so the panel is not empty in the daemon-free demo. It is
  // the ONE sanctioned exception to "do not notify in demo": demo data must not light the bell, so
  // these are ok/warn tier only. Idempotent - a second call is a no-op.
  seedDemo(): void;
}

function svgBell(): string {
  return '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<path d="M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.7 21a2 2 0 0 1-3.4 0"/></svg>';
}

function relTime(at: number, now: number): string {
  const secs = Math.max(0, Math.round((now - at) / 1000));
  if (secs < 60) return secs + "s ago";
  const mins = Math.round(secs / 60);
  if (mins < 60) return mins + "m ago";
  const hrs = Math.round(mins / 60);
  if (hrs < 24) return hrs + "h ago";
  return Math.round(hrs / 24) + "d ago";
}

// mountNotificationCenter builds the title-bar bell and its pop-out history panel, installs the
// document listener that records notifications from every bundle, and returns handles the shell uses to
// register a palette command and seed the demo. No-ops (returning a detached, still-usable store) when
// the title-bar control group is absent.
export function mountNotificationCenter(): NotificationCenter {
  const store = createNotificationStore();

  // Record every notification raised anywhere (surfaces + toasts) into the one store.
  document.addEventListener(NOTIFY_EVENT, (e) => {
    const detail = (e as CustomEvent<NotifyInput>).detail;
    if (detail && typeof detail.message === "string" && typeof detail.source === "string") store.notify(detail);
  });

  const actions = document.getElementById("console-actions");

  // The bell button, inserted just before the settings gear so it sits in the same plain-icon control
  // group as its neighbours. A red dot rides the corner when an unseen error-tier notification waits.
  const bell = document.createElement("button");
  bell.id = "console-notifybtn";
  bell.className = "pf-v6-c-button pf-m-plain";
  bell.type = "button";
  bell.setAttribute("aria-haspopup", "dialog");
  bell.setAttribute("aria-controls", "console-notifypanel");
  bell.setAttribute("aria-expanded", "false");
  bell.setAttribute("aria-label", "Notifications");
  bell.title = "Notifications";
  bell.innerHTML = '<span class="pf-v6-c-button__icon">' + svgBell() + '</span><span class="console-shell-notify__dot" aria-hidden="true"></span>';
  if (actions) {
    const gear = document.getElementById("settings-btn");
    actions.insertBefore(bell, gear); // before the gear; insertBefore(node, null) appends if gear absent
  }

  // The history panel: a right-docked overlay pop-out (positioned in overrides.css). It reuses the
  // Reference panel's pop-out mechanics via wireDrawerToggle rather than inventing a second idiom.
  const panel = document.createElement("div");
  panel.id = "console-notifypanel";
  panel.className = "console-shell-notify";
  panel.setAttribute("role", "dialog");
  panel.setAttribute("aria-label", "Notifications");
  panel.hidden = true;

  const head = document.createElement("div");
  head.className = "console-shell-notify__head";
  const title = document.createElement("span");
  title.className = "console-shell-notify__title";
  title.textContent = "Notifications";
  const clearBtn = document.createElement("button");
  clearBtn.className = "pf-v6-c-button pf-m-link pf-m-inline console-shell-notify__clear";
  clearBtn.type = "button";
  clearBtn.textContent = "Clear all";
  clearBtn.addEventListener("click", () => store.clear());
  head.append(title, clearBtn);

  const listEl = document.createElement("div");
  listEl.className = "console-shell-notify__list";
  listEl.setAttribute("role", "list");

  panel.append(head, listEl);
  // Anchor the panel in the title-bar control group so it drops directly under the bell (positioned in
  // overrides.css, like the Applications menu). Fall back to the body if the control group is absent.
  (actions ?? document.body).append(panel);

  const svgClose = '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>';

  const renderList = (): void => {
    const items = store.list();
    listEl.replaceChildren();
    if (items.length === 0) {
      const empty = document.createElement("p");
      empty.className = "console-shell-notify__empty";
      empty.textContent = "Nothing to report. Failures, sandbox denials, and daemon health changes show up here.";
      listEl.append(empty);
      clearBtn.hidden = true;
      return;
    }
    clearBtn.hidden = false;
    const now = Date.now();
    for (const n of items) {
      const item = document.createElement("div");
      item.className = "console-shell-notify__item";
      item.dataset.kind = n.kind;
      item.setAttribute("role", "listitem");
      if (n.kind === "error" && !n.seen) item.dataset.unseen = "";

      const dot = document.createElement("span");
      dot.className = "console-shell-notify__item-dot";
      dot.setAttribute("aria-hidden", "true");

      const body = document.createElement("div");
      body.className = "console-shell-notify__item-body";
      const msg = document.createElement("p");
      msg.className = "console-shell-notify__item-msg";
      msg.textContent = n.message;
      body.append(msg);

      const meta = document.createElement("div");
      meta.className = "console-shell-notify__item-meta";
      // The source chip: a quiet tag naming the surface/feature that raised this, so the entry stands on
      // its own without the message having to restate where it came from.
      const source = document.createElement("span");
      source.className = "console-shell-notify__item-source";
      source.textContent = n.source;
      meta.append(source);
      const time = document.createElement("span");
      time.className = "console-shell-notify__item-time";
      time.textContent = relTime(n.at, now);
      meta.append(time);
      if (n.link) {
        const link = document.createElement("button");
        link.className = "pf-v6-c-button pf-m-link pf-m-inline console-shell-notify__item-link";
        link.type = "button";
        link.textContent = n.link.label;
        const href = n.link.href;
        link.addEventListener("click", () => { location.assign(href); });
        meta.append(link);
      }
      body.append(meta);

      const dismiss = document.createElement("button");
      dismiss.className = "pf-v6-c-button pf-m-plain console-shell-notify__item-dismiss";
      dismiss.type = "button";
      dismiss.setAttribute("aria-label", "Dismiss this notification");
      dismiss.title = "Dismiss";
      dismiss.innerHTML = '<span class="pf-v6-c-button__icon">' + svgClose + '</span>';
      dismiss.addEventListener("click", () => store.dismiss(n.id));

      item.append(dot, body, dismiss);
      listEl.append(item);
    }
  };

  const renderBell = (): void => {
    const unseen = store.unseenCount();
    if (unseen > 0) { bell.dataset.unseen = ""; bell.title = "Notifications (" + unseen + " unread)"; }
    else { delete bell.dataset.unseen; bell.title = "Notifications"; }
  };

  store.subscribe(() => { renderBell(); if (!panel.hidden) renderList(); });
  renderBell();

  // Reuse the Reference panel's pop-out mechanics: Escape / outside-click dismiss, focus into the panel
  // on open and back to the bell on close, aria wiring. Opening marks everything seen (clears the dot)
  // and paints the current list.
  const toggle = wireDrawerToggle({
    trigger: bell,
    panel,
    focusTarget: () => clearBtn.hidden ? panel : clearBtn,
    onOpen: () => { renderList(); store.markAllSeen(); },
  });

  let seeded = false;
  const seedDemo = (): void => {
    if (seeded) return;
    seeded = true;
    // History tier only - demo data must not light the bell. A plausible slice of the demo workspace's
    // recent activity so the panel reads as lived-in offline.
    const now = Date.now();
    store.notify({ source: "Dashboard", kind: "ok", message: "Reconnected to the daemon at 127.0.0.1:7391.", at: now - 6 * 60_000, key: "demo:reconnect" });
    store.notify({ source: "Log Viewer", kind: "warn", message: "svc/api:build finished with warnings (2 deprecations).", at: now - 4 * 60_000, key: "demo:build-warn" });
    store.notify({ source: "Settings", kind: "ok", message: "Console settings applied.", at: now - 90_000, key: "demo:settings" });
  };

  return {
    store,
    open: toggle.open,
    close: toggle.close,
    toggle: toggle.toggle,
    seedDemo,
  };
}
