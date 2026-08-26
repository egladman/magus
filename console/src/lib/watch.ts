// watch.ts - the shell-side notification watchers. These are the notifications the console cannot derive
// from a surface it happens to have open: they must be observed at the SHELL so they fire whether or not
// you are looking (the "unwatched" half of the admission doctrine). Three daemon-dependent watchers poll
// on a slow ticker over the console's existing authenticated transport - no new backend push:
//   - share-connect: a device first exercising the share token records a TOKEN_LIFECYCLE "share.open"
//     trail event; surfaced as a BELL-tier notification with a one-click "Revoke share" action.
//   - daemon storage: the daemon cache crossing its size threshold; a warn that rings once.
//   - review merged: a review you took part in has landed, so its conversation can be kept. The forge
//     is asked by the check-review JOB on the maintenance schedule, not here; this reads what it
//     recorded, so a merge is noticed even when the console was shut when it happened.
// A third watcher (localStorage size) needs no daemon and runs on mount. All are best-effort: an
// unreachable daemon just means the poll no-ops until the next tick.

import { createClient } from "@connectrpc/connect";
import { ActivityService, Kind } from "../gen/magus/activity/v1alpha1/activity_pb";
import { StatusService } from "../gen/magus/status/v1alpha1/status_pb";
import { TokenService, TokenScope } from "../gen/magus/token/v1alpha1/token_pb";
import { createDaemonTransport, getLiveToken, resolveDaemonHost } from "./daemon";
import { showToast } from "./refresh-toast";
import { mergedNotice } from "./review-notice";
import {
  type NotificationStore,
  estimateStorageBytes,
  humanBytes,
  daemonCacheOverThreshold,
  LOCALSTORAGE_WARN_BYTES,
} from "./notifications";

const POLL_MS = 30_000;

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

// pollReviewMerged surfaces that a review you took part in has landed, so its conversation can be kept
// before it becomes only a page on somebody else's website.
//
// It READS the trail; it does not do the watching. The check-review JOB asks the forge on the
// maintenance schedule and records what it found, which is what lets the merge be noticed while the
// console is shut - and the console is optional, so a watcher that only runs with a tab open would
// miss most merges. This is the same shape pollShareConnect uses for share.open: the daemon records,
// the console reads.
//
// HISTORY tier, deliberately. The admission doctrine in notifications.ts gives the bell to things that
// change what you can TRUST about the workspace; a merge changes nothing you were relying on. It is
// worth recording and not worth interrupting for, which is exactly what the silent tier is.
async function pollReviewMerged(host: string, store: NotificationStore): Promise<void> {
  const activity = createClient(ActivityService, createDaemonTransport(host, getLiveToken()));
  const resp = await activity.listActivityEvents({
    pageSize: 5,
    filter: { kinds: [Kind.JOB], actions: ["review.merged"], actors: [] },
  });
  for (const ev of resp.events) {
    if (ev.action !== "review.merged") continue;
    // The job writes "<repo>: <count>", the two things the sentence needs. A row it cannot read is
    // skipped rather than rendered as a half-sentence.
    const [repo, said] = (ev.preview || "").split(": ");
    const message = mergedNotice({ repo, state: "merged" }, Number(said));
    if (!message) continue;
    // Keyed by the event rather than by the review, so a later merge on the same repo is a new
    // record and re-seeing this one across polls is not.
    store.notify({
      source: "Diff",
      kind: "ok",
      key: "review.merged:" + (ev.time ? Number(ev.time.seconds) : 0) + ":" + (ev.preview || ""),
      message,
    });
  }
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
