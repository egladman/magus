// watch.ts - the shell-side notification watchers. These are the notifications the console cannot derive
// from a surface it happens to have open: they must be observed at the SHELL so they fire whether or not
// you are looking (the "unwatched" half of the admission doctrine). Three daemon-dependent watchers poll
// on a slow ticker over the console's existing authenticated transport - no new backend push:
//   - share-connect: a device first exercising the share token records a TOKEN_LIFECYCLE "share.open"
//     trail event; surfaced as a BELL-tier notification with a one-click "Revoke share" action.
//   - daemon storage: the daemon cache crossing its size threshold; a warn that rings once.
//   - review merged: a review you took part in has landed, so its conversation can be kept. The only
//     watcher that reaches past the daemon to a forge, which is why it has its own slower clock.
// A third watcher (localStorage size) needs no daemon and runs on mount. All are best-effort: an
// unreachable daemon just means the poll no-ops until the next tick.

import { createClient } from "@connectrpc/connect";
import { ActivityService, Kind } from "../gen/magus/activity/v1alpha1/activity_pb";
import { StatusService } from "../gen/magus/status/v1alpha1/status_pb";
import { TokenService, TokenScope } from "../gen/magus/token/v1alpha1/token_pb";
import { authHeaders, createDaemonTransport, getLiveToken, resolveDaemonHost } from "./daemon";
import { showToast } from "./refresh-toast";
import { mergedNotice, type MergedReview } from "./review-notice";
import {
  type NotificationStore,
  estimateStorageBytes,
  humanBytes,
  daemonCacheOverThreshold,
  LOCALSTORAGE_WARN_BYTES,
} from "./notifications";

const POLL_MS = 30_000;

// The merge check reaches the FORGE through the daemon, unlike every other watcher here, so it runs on
// its own far slower clock. A pull request merges once; learning about it a few minutes later costs the
// reader nothing, and asking every 30 seconds would spend an API rate limit all day to find that out.
const REVIEW_POLL_MS = 5 * 60_000;
let reviewMergedCheckedMs = 0;
let reviewMergedReported = false;

// checkLocalStorageAlert warns once when the console's own localStorage footprint nears the browser quota.
// Runs on mount regardless of daemon connectivity - it is the console's storage, not the daemon's.
export function checkLocalStorageAlert(
  store: NotificationStore,
  area: Pick<Storage, "length" | "key" | "getItem"> = localStorage,
): void {
  let bytes = 0;
  try {
    bytes = estimateStorageBytes(area);
  } catch {
    return;
  }
  if (bytes < LOCALSTORAGE_WARN_BYTES) return;
  store.notify({
    source: "Console",
    kind: "warn",
    important: true,
    key: "storage:local",
    message:
      "The console's local storage is large (" +
      humanBytes(bytes) +
      "). Consider clearing old saved data if it keeps growing.",
  });
}

// revokeActiveShareToken lists the daemon's tokens, finds the active share token, and revokes it - which
// also closes the phone-share LAN listener server-side. It reuses the TokenService the Settings token
// section speaks to; the notification's action button calls this.
async function revokeActiveShareToken(host: string): Promise<void> {
  const tokens = createClient(TokenService, createDaemonTransport(host, getLiveToken()));
  try {
    const resp = await tokens.listTokens({});
    const share = resp.tokens.find((t) => t.scope === TokenScope.SHARE_READ);
    if (!share) {
      showToast("Share", "No active share token to revoke.");
      return;
    }
    await tokens.revokeToken({ name: share.identifier });
    showToast("Share", "Revoked the share token; the share listener is closed.");
  } catch (e) {
    showToast(
      "Share",
      "Could not revoke the share token: " + (e instanceof Error ? e.message : String(e)),
      "error",
    );
  }
}

// pollShareConnect raises a bell notification for each share.open event newer than the watch baseline - a
// device that connected SINCE the console opened, so old history is not resurfaced as a fresh alert. The
// store dedupes by key, so re-seeing the same event across polls does not re-fire.
async function pollShareConnect(
  host: string,
  store: NotificationStore,
  baselineMs: number,
): Promise<void> {
  const activity = createClient(ActivityService, createDaemonTransport(host, getLiveToken()));
  const resp = await activity.listActivityEvents({
    pageSize: 20,
    filter: { kinds: [Kind.TOKEN_LIFECYCLE], actions: ["share.open"], actors: [] },
  });
  for (const ev of resp.events) {
    if (ev.action !== "share.open") continue;
    const ms = ev.time ? Number(ev.time.seconds) * 1000 : 0;
    if (ms < baselineMs) continue;
    store.notify({
      source: "Share",
      kind: "warn",
      important: true,
      key: "share.open:" + ms + ":" + (ev.preview || ""),
      message:
        "A device opened your share link" +
        (ev.preview ? " (" + ev.preview + ")" : "") +
        ". Revoke the share if this was not you.",
      link: { label: "Revoke share", run: () => revokeActiveShareToken(host) },
    });
  }
}

// pollDaemonStorage warns once when the daemon cache crosses its threshold (85% of a configured cap, or an
// absolute fallback when uncapped), read off the live status snapshot the dashboard already consumes.
async function pollDaemonStorage(host: string, store: NotificationStore): Promise<void> {
  const status = createClient(StatusService, createDaemonTransport(host, getLiveToken()));
  const resp = await status.getStatus({});
  const cache = resp.status?.pool?.cache;
  if (!cache) return;
  const size = Number(cache.sizeBytes);
  const capBytes = cache.sizeCapMb * 1024 * 1024;
  if (!daemonCacheOverThreshold(size, capBytes)) return;
  store.notify({
    source: "Dashboard",
    kind: "warn",
    important: true,
    key: "storage:daemon",
    message:
      "The daemon cache is large (" +
      humanBytes(size) +
      (capBytes > 0 ? " of a " + humanBytes(capBytes) + " cap" : "") +
      "). Run the clear-cache job from Activity or rotate logs to reclaim space.",
  });
}

// pollReviewMerged reports that a review you took part in has landed, so its conversation can be kept
// before it becomes only a page on somebody else's website.
//
// HISTORY tier, deliberately. The admission doctrine in notifications.ts gives the bell to things that
// change what you can TRUST about the workspace; a merge changes nothing you were relying on. It is
// worth recording and not worth interrupting for, which is exactly what the silent tier is.
//
// Three gates, and each is load-bearing:
//
//   - a review SESSION must exist. Opening a review is the opt-in, and without this gate magus would
//     talk to a forge about branches you never looked at.
//   - it runs at REVIEW_POLL_MS, not the shell's 30s. Every other watcher here asks the local daemon
//     and costs nothing; this one reaches the forge through it, and a 30-second poll would spend a
//     rate limit all day to learn something that changes once.
//   - it stops after reporting. The notification store dedupes by key, but the CALL is the cost here,
//     not the record.
async function pollReviewMerged(host: string, store: NotificationStore): Promise<void> {
  if (reviewMergedReported) return;
  const now = Date.now();
  if (now - reviewMergedCheckedMs < REVIEW_POLL_MS) return;
  reviewMergedCheckedMs = now;

  // The session first, because it is local and it is what decides whether the forge is asked at all.
  const session = await fetch(`http://${host}/api/v1/diff/session`, { headers: authHeaders() });
  if (!session.ok) return;
  const mine = ((await session.json()) as { comments?: unknown[] }).comments ?? [];

  const res = await fetch(`http://${host}/api/v1/diff/review`, { headers: authHeaders() });
  if (!res.ok) return;
  const review = (await res.json()) as MergedReview & { id: string; threads?: unknown[] };
  // The same sentence the Diff surface offers, decided by the same rule. Two copies of "is this
  // worth saying" would drift the first time either was tuned.
  const message = mergedNotice(review, (review.threads?.length ?? 0) + mine.length);
  if (!message) return;

  reviewMergedReported = true;
  store.notify({ source: "Diff", kind: "ok", key: "review.merged:" + review.id, message });
}

// startShellWatch begins the daemon-dependent watchers on a slow ticker and returns a stop function. It
// resolves the daemon host per tick (an attach can happen after boot) and no-ops when none is resolved.
export function startShellWatch(store: NotificationStore): () => void {
  const baselineMs = Date.now();
  let stopped = false;
  const tick = async (): Promise<void> => {
    if (stopped) return;
    const host = resolveDaemonHost();
    if (!host) return;
    // Each watcher is independent and best-effort: one failing (or the daemon being unreachable) must not
    // stop the other or tear down the ticker.
    await Promise.allSettled([
      pollShareConnect(host, store, baselineMs),
      pollDaemonStorage(host, store),
      pollReviewMerged(host, store),
    ]);
  };
  const timer = setInterval(() => void tick(), POLL_MS);
  void tick();
  return () => {
    stopped = true;
    clearInterval(timer);
  };
}
