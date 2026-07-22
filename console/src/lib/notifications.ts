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

// An action rendered as a button on both the transient toast and the history entry. It is EITHER a deep
// link (`href`, clicked -> location.assign: a same-app fragment updates in place, an app-relative page
// navigates there) OR a callback (`run`, clicked -> invoked). `run` is for a shell-originated action that
// is not a navigation - e.g. "Revoke share" calling TokenService. It cannot cross the NOTIFY_EVENT bundle
// boundary (a function does not serialize), so only a caller holding the store directly (the shell) may
// set it; cross-bundle callers use `href`.
export interface NotifyLink {
  label: string;
  href?: string;
  run?: () => void | Promise<void>;
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
  // `important` opts an entry into the BELL tier (lights the unseen-dot until the panel is opened),
  // decoupled from `kind` (severity/color). It defaults to `kind === "error"`, so every existing caller
  // is unchanged: a failure still rings, an ok/warn still records silently. It exists so a signal that is
  // NOT a failure but still needs a human - a device connecting to your share token, storage crossing a
  // threshold, an author-declared magus:alert: - can ring the bell while keeping a warn/ok color.
  important?: boolean;
}

// One recorded notification. `important` marks the bell tier; `seen` gates the unseen-dot and is only
// ever false for important entries (history-tier entries are recorded already-seen, so they never light
// the dot).
export interface Notification {
  id: string;
  source: string;
  kind: NotifyKind;
  message: string;
  link?: NotifyLink;
  at: number;
  important: boolean;
  seen: boolean;
}

export interface NotificationStore {
  // notify admits an input, returning the created Notification, or null when it was deduped away.
  notify(input: NotifyInput): Notification | null;
  list(): Notification[]; // newest first
  unseenCount(): number; // important, not yet seen
  markAllSeen(): void;
  dismiss(id: string): void;
  // dismissOlderThan drops every entry older than maxAgeMs (measured from `now`, injectable for tests),
  // leaving the recent tail. It backs the granular "dismiss older than 1h/3h/6h" control, a lighter touch
  // than clear() when a burst of stale notifications has piled up but the recent ones still matter.
  dismissOlderThan(maxAgeMs: number, now?: number): void;
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
  if (link.run) return { label: link.label || "Open", run: link.run };
  return link.href ? { label: link.label || "Open", href: link.href } : undefined;
}

export function createNotificationStore(): NotificationStore {
  let items: Notification[] = []; // newest first
  const keys = new Set<string>(); // every dedupe key admitted this session
  const listeners = new Set<() => void>();
  let seq = 0;

  const emit = (): void => {
    for (const fn of listeners) fn();
  };

  return {
    notify(input: NotifyInput): Notification | null {
      if (input.key && keys.has(input.key)) return null; // same event, already recorded
      if (input.key) keys.add(input.key);
      const kind = input.kind ?? "ok";
      const important = input.important ?? kind === "error";
      const n: Notification = {
        id: "n" + ++seq,
        source: input.source,
        kind,
        message: input.message,
        link: normalizeLink(input.link),
        at: input.at ?? Date.now(),
        important,
        // Only the bell tier (important) starts unseen; history-tier entries are recorded already-seen so
        // they never light the dot.
        seen: !important,
      };
      items.unshift(n);
      if (items.length > MAX_HISTORY) items = items.slice(0, MAX_HISTORY);
      emit();
      return n;
    },
    list(): Notification[] {
      return items.slice();
    },
    unseenCount(): number {
      return items.reduce((acc, n) => acc + (n.important && !n.seen ? 1 : 0), 0);
    },
    markAllSeen(): void {
      let changed = false;
      for (const n of items)
        if (!n.seen) {
          n.seen = true;
          changed = true;
        }
      if (changed) emit();
    },
    dismiss(id: string): void {
      const before = items.length;
      items = items.filter((n) => n.id !== id);
      if (items.length !== before) emit();
    },
    dismissOlderThan(maxAgeMs: number, now: number = Date.now()): void {
      const cutoff = now - maxAgeMs;
      const before = items.length;
      items = items.filter((n) => n.at >= cutoff);
      if (items.length !== before) emit();
    },
    clear(): void {
      if (items.length === 0) return;
      items = [];
      emit();
    },
    subscribe(fn: () => void): () => void {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
  };
}

// AUTHOR MARKER SEAM. A magusfile author declares a notification from inside a build by printing a marker
// line - `magus:alert:<message>` for the bell tier, `magus:notice:<message>` for the history tier. These
// ride the captured output stream verbatim (the daemon does no push plumbing for them), so the console
// matches them FRONTEND-side wherever it already reads that stream. matchAuthorMarker turns one output
// line into a NotifyInput, or null when the line carries no marker. It is pure (no DOM, no store) so the
// matching is unit-tested here and the caller owns only the where-and-dedupe. The message is trimmed; an
// empty message after the prefix is not a marker (so a bare "magus:alert:" does not raise a blank entry).
const ALERT_PREFIX = "magus:alert:";
const NOTICE_PREFIX = "magus:notice:";
export function matchAuthorMarker(line: string, source = "Build"): NotifyInput | null {
  const text = line.trimStart();
  const hit = (prefix: string): string | null => {
    if (!text.startsWith(prefix)) return null;
    const msg = text.slice(prefix.length).trim();
    return msg.length > 0 ? msg : null;
  };
  const alert = hit(ALERT_PREFIX);
  if (alert) return { source, kind: "warn", important: true, message: alert };
  const notice = hit(NOTICE_PREFIX);
  if (notice) return { source, kind: "ok", important: false, message: notice };
  return null;
}

// STORAGE ALERTS. Two storage stores can grow until they hurt: the browser's localStorage (the console's
// own persisted settings/tokens/workspace) and the daemon's on-disk cache. Both are silent until they
// bite, so the console warns the operator ONCE when either crosses a threshold - a history-tier warn that
// rings the bell (important) so it is not missed, but is not styled as a failure. The thresholds are
// arbitrary-but-sane: a browser localStorage quota is ~5 MB, so 4 MB is "getting full"; the daemon cache
// warns at 85% of its configured cap, or at an absolute 2 GiB when uncapped.
export const LOCALSTORAGE_WARN_BYTES = 4 * 1024 * 1024;
export const DAEMON_CACHE_WARN_FRACTION = 0.85;
export const DAEMON_CACHE_WARN_ABS_BYTES = 2 * 1024 * 1024 * 1024;

// A minimal Storage view (localStorage satisfies it) so estimateStorageBytes is pure and testable.
export interface StorageLike {
  length: number;
  key(i: number): string | null;
  getItem(k: string): string | null;
}

// estimateStorageBytes approximates a Web Storage area's footprint: the sum over every entry of
// (key length + value length), counted as UTF-16 code units x2 bytes, which is how browsers charge the
// quota. It is an estimate (surrogate pairs, engine overhead) but good enough to decide "getting full".
export function estimateStorageBytes(store: StorageLike): number {
  let bytes = 0;
  for (let i = 0; i < store.length; i++) {
    const k = store.key(i);
    if (k === null) continue;
    const v = store.getItem(k) ?? "";
    bytes += (k.length + v.length) * 2;
  }
  return bytes;
}

// humanBytes renders a byte count as a compact human size (MB/GB) for a message. Plain ASCII.
export function humanBytes(bytes: number): string {
  if (bytes >= 1024 * 1024 * 1024) return (bytes / (1024 * 1024 * 1024)).toFixed(1) + " GB";
  if (bytes >= 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + " MB";
  if (bytes >= 1024) return Math.round(bytes / 1024) + " KB";
  return bytes + " B";
}

// daemonCacheOverThreshold decides whether the daemon cache warrants a warning: over 85% of its cap when
// capped (capBytes > 0), else over the absolute fallback. Returns false for a zero/unknown size.
export function daemonCacheOverThreshold(sizeBytes: number, capBytes: number): boolean {
  if (sizeBytes <= 0) return false;
  if (capBytes > 0) return sizeBytes >= capBytes * DAEMON_CACHE_WARN_FRACTION;
  return sizeBytes >= DAEMON_CACHE_WARN_ABS_BYTES;
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
  return (
    '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<path d="M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.7 21a2 2 0 0 1-3.4 0"/></svg>'
  );
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
    if (detail && typeof detail.message === "string" && typeof detail.source === "string")
      store.notify(detail);
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
  bell.innerHTML =
    '<span class="pf-v6-c-button__icon">' +
    svgBell() +
    '</span><span class="console-shell-notify__dot" aria-hidden="true"></span>';
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

  // The header's right-side actions: a "Dismiss older" menu (1h/3h/6h - the granular filter for a pile of
  // stale entries) beside the heavy-handed "Clear all". Under volume, clearing everything throws away the
  // recent ones you still care about; the age filter is the lighter touch. The menu reuses the launcher
  // kebab idiom (button + hidden menu, outside/Escape dismiss) rather than a native <select> so it matches
  // the console's other popovers.
  const actionsGroup = document.createElement("div");
  actionsGroup.className = "console-shell-notify__actions";

  const olderWrap = document.createElement("div");
  olderWrap.className = "console-shell-notify__older";
  const olderBtn = document.createElement("button");
  olderBtn.className = "pf-v6-c-button pf-m-link pf-m-inline console-shell-notify__older-btn";
  olderBtn.type = "button";
  olderBtn.setAttribute("aria-haspopup", "menu");
  olderBtn.setAttribute("aria-expanded", "false");
  olderBtn.textContent = "Dismiss older";
  const olderMenu = document.createElement("div");
  olderMenu.className = "console-shell-notify__older-menu";
  olderMenu.setAttribute("role", "menu");
  olderMenu.hidden = true;
  const HOUR_MS = 60 * 60 * 1000;
  let olderOpen = false;
  const setOlder = (v: boolean): void => {
    olderOpen = v;
    olderMenu.hidden = !v;
    olderBtn.setAttribute("aria-expanded", v ? "true" : "false");
  };
  for (const [label, hours] of [
    ["Older than 1 hour", 1],
    ["Older than 3 hours", 3],
    ["Older than 6 hours", 6],
  ] as const) {
    const mi = document.createElement("button");
    mi.className = "console-shell-notify__older-item";
    mi.type = "button";
    mi.setAttribute("role", "menuitem");
    mi.textContent = label;
    mi.addEventListener("click", () => {
      store.dismissOlderThan(hours * HOUR_MS);
      setOlder(false);
    });
    olderMenu.append(mi);
  }
  olderBtn.addEventListener("click", (e) => {
    e.stopPropagation();
    setOlder(!olderOpen);
  });
  olderMenu.addEventListener("click", (e) => e.stopPropagation());
  document.addEventListener("pointerdown", (e) => {
    if (olderOpen && e.target instanceof Node && !olderWrap.contains(e.target)) setOlder(false);
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && olderOpen) {
      e.stopPropagation();
      setOlder(false);
      olderBtn.focus();
    }
  });
  olderWrap.append(olderBtn, olderMenu);

  const clearBtn = document.createElement("button");
  clearBtn.className = "pf-v6-c-button pf-m-link pf-m-inline console-shell-notify__clear";
  clearBtn.type = "button";
  clearBtn.textContent = "Clear all";
  clearBtn.addEventListener("click", () => store.clear());
  actionsGroup.append(olderWrap, clearBtn);
  head.append(title, actionsGroup);

  const listEl = document.createElement("div");
  listEl.className = "console-shell-notify__list";
  listEl.setAttribute("role", "list");

  panel.append(head, listEl);
  // Anchor the panel in the title-bar control group so it drops directly under the bell (positioned in
  // overrides.css, like the Applications menu). Fall back to the body if the control group is absent.
  (actions ?? document.body).append(panel);

  const svgClose =
    '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>';

  const renderList = (): void => {
    const items = store.list();
    listEl.replaceChildren();
    if (items.length === 0) {
      const empty = document.createElement("p");
      empty.className = "console-shell-notify__empty";
      empty.textContent =
        "Nothing to report. Failures, sandbox denials, and daemon health changes show up here.";
      listEl.append(empty);
      clearBtn.hidden = true;
      olderWrap.hidden = true;
      setOlder(false);
      return;
    }
    clearBtn.hidden = false;
    olderWrap.hidden = false;
    const now = Date.now();
    for (const n of items) {
      const item = document.createElement("div");
      item.className = "console-shell-notify__item";
      item.dataset.kind = n.kind;
      item.setAttribute("role", "listitem");
      if (n.important && !n.seen) item.dataset.unseen = "";

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
        const nlink = n.link;
        link.addEventListener("click", () => {
          if (nlink.run) {
            link.disabled = true;
            void Promise.resolve(nlink.run()).finally(() => {
              link.disabled = false;
            });
          } else if (nlink.href) location.assign(nlink.href);
        });
        meta.append(link);
      }
      body.append(meta);

      const dismiss = document.createElement("button");
      dismiss.className = "pf-v6-c-button pf-m-plain console-shell-notify__item-dismiss";
      dismiss.type = "button";
      dismiss.setAttribute("aria-label", "Dismiss this notification");
      dismiss.title = "Dismiss";
      dismiss.innerHTML = '<span class="pf-v6-c-button__icon">' + svgClose + "</span>";
      dismiss.addEventListener("click", () => store.dismiss(n.id));

      item.append(dot, body, dismiss);
      listEl.append(item);
    }
  };

  const renderBell = (): void => {
    const unseen = store.unseenCount();
    if (unseen > 0) {
      bell.dataset.unseen = "";
      bell.title = "Notifications (" + unseen + " unread)";
    } else {
      delete bell.dataset.unseen;
      bell.title = "Notifications";
    }
  };

  store.subscribe(() => {
    renderBell();
    if (!panel.hidden) renderList();
  });
  renderBell();

  // Reuse the Reference panel's pop-out mechanics: Escape / outside-click dismiss, focus into the panel
  // on open and back to the bell on close, aria wiring. Opening marks everything seen (clears the dot)
  // and paints the current list.
  const toggle = wireDrawerToggle({
    trigger: bell,
    panel,
    focusTarget: () => (clearBtn.hidden ? panel : clearBtn),
    onOpen: () => {
      renderList();
      store.markAllSeen();
    },
  });

  let seeded = false;
  const seedDemo = (): void => {
    if (seeded) return;
    seeded = true;
    // History tier only - demo data must not light the bell. A plausible slice of the demo workspace's
    // recent activity so the panel reads as lived-in offline.
    const now = Date.now();
    store.notify({
      source: "Dashboard",
      kind: "ok",
      message: "Reconnected to the daemon at 127.0.0.1:7391.",
      at: now - 6 * 60_000,
      key: "demo:reconnect",
    });
    store.notify({
      source: "Log Viewer",
      kind: "warn",
      message: "svc/api:build finished with warnings (2 deprecations).",
      at: now - 4 * 60_000,
      key: "demo:build-warn",
    });
    store.notify({
      source: "Settings",
      kind: "ok",
      message: "Console settings applied.",
      at: now - 90_000,
      key: "demo:settings",
    });
  };

  return {
    store,
    open: toggle.open,
    close: toggle.close,
    toggle: toggle.toggle,
    seedDemo,
  };
}
