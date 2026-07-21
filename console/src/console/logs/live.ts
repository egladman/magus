// live.ts - live streaming (#port=<port>&token=, or the daemon-origin/shared console). A run started
// with `--live` prints a link to an ephemeral 127.0.0.1 SSE server. The viewer connects (fetch-based
// SSE + bearer token, mirroring the graph explorer's live client), decodes each frame as a protobuf
// Event, appends it, re-renders on a frame tick, and auto-scrolls unless the reader pins the view with
// Pause. Datadog-style live tail. The live buffer (state.liveEvents / liveInvocation) is also reused by
// the #demo reveal, so scheduleLiveRender is the shared "one re-render per frame" path.

import { fromBinary } from "@bufbuild/protobuf";
import { EventSchema, Kind, Status } from "../../gen/magus/viewer/v1/viewer_pb";
import { consumeLiveToken, getLiveToken, fetchSSE, logsLink } from "../../lib/daemon";
import { notify, matchAuthorMarker } from "../../lib/notifications";
import type { ViewerParams } from "./fragment";
import { base64ToBytes } from "./fragment";
import { state, waterfallSource } from "./state";
import { el, emptyEl, scrollEl, setBtnLabel, setRefIdentity } from "./dom";
import { buildModelMulti } from "./model";
import { render, updateTimelineControl } from "./render";

// The host of the current live stream, stashed so a FAIL notification can deep-link back to the failing
// ref. A resolved daemon host (loopback, or the shared-mode same-origin LAN host), or null before any
// live connect.
let liveNotifyHost: string | null = null;

// connectLive attaches to a resolved daemon host (from daemonAttach - loopback, or the shared/daemon-
// origin console), never a raw fragment string.
export function connectLive(host: string, params: ViewerParams): void {
  liveNotifyHost = host;
  consumeLiveToken(params); // stash the token, strip it from the URL so it never persists
  const token = getLiveToken();
  state.liveEvents = [];
  state.liveInvocation = null;
  state.livePaused = false;
  if (emptyEl) emptyEl.hidden = true;
  setRefIdentity("live", false);
  const pauseBtn = el("pause-btn");
  if (pauseBtn) {
    (pauseBtn as HTMLButtonElement).disabled = false;
    setBtnLabel(pauseBtn, "Pause");
    pauseBtn.setAttribute("aria-pressed", "false");
  }
  setLiveStatus("connecting");

  state.liveAbort = new AbortController();
  const headers: Record<string, string> = token ? { Authorization: "Bearer " + token } : {};
  fetchSSE(
    "http://" + host + "/events",
    headers,
    onLiveEvent,
    (e) => setLiveStatus(/stream ended|done/i.test((e && e.message) || "") ? "done" : "disconnected"),
    state.liveAbort.signal,
    () => setLiveStatus("streaming"),
  );
}

function onLiveEvent(type: string, data: string): void {
  if (type === "done") {
    setLiveStatus("done");
    if (state.liveAbort) state.liveAbort.abort();
    return;
  }
  if (!data) return;
  let ev;
  try {
    ev = fromBinary(EventSchema, base64ToBytes(data));
  } catch (_) {
    return; // ignore an undecodable frame rather than tearing down the stream
  }
  if (ev.kind === Kind.STARTED && ev.command) state.liveInvocation = { command: ev.command };
  // A target that FAILED in the live stream is a bell-tier event: keyed on its output ref and shared
  // with the dashboard's failure detection (both use "fail:<ref>"), so a failure observed by either
  // surface records exactly ONE notification. This path is live-only (the #demo reveal does not run
  // onLiveEvent), so the demo never lights the bell. The failing output is right here in the stream, so
  // it deep-links back to that ref for when the reader had this tab backgrounded.
  if (ev.kind === Kind.RESULT && ev.status === Status.FAIL && ev.ref) {
    const label = ev.target || ev.ref;
    const link = liveNotifyHost
      ? { label: "Open in log viewer", href: logsLink(liveNotifyHost, { ref: ev.ref }) }
      : undefined;
    notify({ source: "Log Viewer", kind: "error", key: "fail:" + ev.ref, message: label + " failed.", link });
  }
  // Author-declared markers: a build that prints `magus:alert:`/`magus:notice:` raises a bell/history
  // notification, matched frontend-side off the verbatim output line (no backend push). Keyed on the
  // marker text so a stream reconnect that replays the same backlog line does not re-fire it; two genuinely
  // distinct alerts with identical text collapsing to one is the accepted, lesser evil.
  if (ev.kind === Kind.OUTPUT && ev.text) {
    const marker = matchAuthorMarker(ev.text);
    if (marker) notify({ ...marker, key: (marker.important ? "alert:" : "notice:") + marker.message });
  }
  state.liveEvents.push(ev);
  scheduleLiveRender();
}

// scheduleLiveRender coalesces a burst of events into one re-render per frame: rebuild the
// model from all events so far and render, auto-scrolling to the tail unless paused.
export function scheduleLiveRender(): void {
  if (state.liveRenderQueued) return;
  state.liveRenderQueued = true;
  requestAnimationFrame(() => {
    state.liveRenderQueued = false;
    const built = buildModelMulti(waterfallSource());
    state.model = { sections: built.sections, titled: built.titled };
    state.rawLines = built.rawLines;
    state.rawText = built.rawLines.join("\n");
    render();
    setLiveStatus(state.liveAbort && state.liveAbort.signal.aborted ? "done" : "streaming");
    updateTimelineControl();
    const foldBtn = el("fold-all-btn");
    if (foldBtn) (foldBtn as HTMLButtonElement).disabled = state.timeline || state.model.titled === 0 || !state.pretty;
    const copyBtn = el("copy-all-btn");
    if (copyBtn) (copyBtn as HTMLButtonElement).disabled = false;
    const shareBtn = el("share-btn");
    if (shareBtn) (shareBtn as HTMLButtonElement).disabled = false;
    if (!state.livePaused && scrollEl) scrollEl.scrollTop = scrollEl.scrollHeight;
  });
}

export function setLiveStatus(linkState: string): void {
  // Drive the shared console status bar's connection dot (the same element the dashboard uses), so
  // the log viewer reads the same as its sibling apps. A live stream is "connected" (green) with the
  // event count; a finished stream is "done" (still green - it completed cleanly); connecting/
  // disconnected map to those states. A statically loaded log never calls this, so the dot stays at
  // its default "not connected", which is accurate (no live daemon link).
  const conn = document.getElementById("console-conn");
  if (!conn) return;
  if (linkState === "streaming") {
    conn.textContent = "connected"; conn.dataset.state = "connected"; delete conn.dataset.health;
  } else if (linkState === "connecting") {
    conn.textContent = "connecting..."; conn.dataset.state = "connecting"; delete conn.dataset.health;
  } else if (linkState === "done") {
    conn.textContent = "done"; conn.dataset.state = "connected"; delete conn.dataset.health;
  } else if (linkState === "disconnected") {
    conn.textContent = "disconnected"; conn.dataset.state = "disconnected"; delete conn.dataset.health;
  }
  // The event count sits on the FAR RIGHT of the bar (its own item, like observing-since), not
  // appended to the connection state.
  const count = document.getElementById("console-count");
  if (count) {
    const n = state.liveEvents.length;
    count.textContent = n ? n + " events" : "";
    count.hidden = !n;
  }
}
