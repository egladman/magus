// connection.ts - the status bar's connection control (#console-conn).
//
// It is a PF link button, not a clickable span: it jumps to the server-address setting, so it has to
// be a button to a keyboard and to a screen reader. Its visible text is its name; the sentence about
// the last probe is its description, and a separate visually hidden status region announces a change
// of state once, instead of the control announcing every poll.
//
// Kept apart from statusbar.ts because the apps' bundles import status.ts, and the bar's build
// fingerprint and server client are not theirs to carry.

import { isReadOnly, parseHash, wantsDemo } from "../lib/server";
import { getDefaultHost } from "../lib/settings";
import { statusIcon, type Status } from "../ui/status";
import type { ConnectionState } from "./status";

export function notConnectedHint(host: string): string {
  return host
    ? "Not connected to " + host + ". Click to change the server address."
    : "No server address configured. Click to set the server address.";
}

export const DEMO_CONNECTION_HINT = "Demo data is synthetic. Click to change the server address.";

// connectionStatus is the shape a state wears. The colour tracks the same states, but the icon and the
// word carry them too.
export function connectionStatus(state: string | undefined, health: string | undefined): Status {
  if (state === "connected") {
    if (health === "fail") return "danger";
    if (health === "warn") return "warning";
    return "success";
  }
  if (state === "demo") return "info";
  if (state === "connecting" || state === "reconnecting") return "warning";
  return "danger";
}

function statusWord(state: string | undefined, health: string | undefined): string {
  if (state === "connected") {
    if (health === "fail") return "connected, server reports a failure";
    if (health === "warn") return "connected, server degraded";
    return "connected";
  }
  if (state === "demo") return "demo data";
  if (state === "connecting" || state === "reconnecting") return "connecting";
  return "not connected";
}

export interface ConnectionPatch {
  label?: string;
  state?: ConnectionState;
  // null clears the health reading.
  health?: string | null;
  // The sentence a screen reader gets as the control's description; it is also the tooltip unless
  // `title` says otherwise.
  hint?: string;
  // A tooltip that differs from the description, such as the last probe's detail.
  title?: string;
}

// connectionParts finds the pieces of a connection control, or null for a node that is not one (a
// bare element in a test or an old page): callers then fall back to textContent.
function connectionParts(conn: HTMLElement): { mark: HTMLElement; label: HTMLElement } | null {
  const mark = conn.querySelector<HTMLElement>("[data-conn-mark]");
  const label = conn.querySelector<HTMLElement>("[data-conn-label]");
  return mark && label ? { mark, label } : null;
}

// writeConnection is the one place the control changes. State, label, health and hint go through it
// so the icon, the description and the live status can never disagree with the dataset.
export function writeConnection(conn: HTMLElement, patch: ConnectionPatch): void {
  if (patch.state !== undefined) conn.dataset.state = patch.state;
  if (patch.health === null) delete conn.dataset.health;
  else if (patch.health !== undefined) conn.dataset.health = patch.health;

  const parts = connectionParts(conn);
  if (patch.label !== undefined) {
    if (parts) parts.label.textContent = patch.label;
    else conn.textContent = patch.label;
  }
  if (patch.hint !== undefined) {
    conn.title = patch.hint;
    const hint = conn.parentElement?.querySelector<HTMLElement>("[data-conn-hint]");
    if (hint) hint.textContent = patch.hint;
    else conn.setAttribute("aria-label", patch.hint);
  }
  if (patch.title !== undefined) conn.title = patch.title;
  if (!parts) return;

  const status = connectionStatus(conn.dataset.state, conn.dataset.health);
  const key = status + ":" + (conn.dataset.state ?? "") + ":" + (conn.dataset.health ?? "");
  if (parts.mark.dataset.key === key) return;
  parts.mark.dataset.key = key;
  parts.mark.replaceChildren(statusIcon(status));
  // Announce a change of state, never a repeat: the live region is rewritten only when it differs.
  const live = conn.parentElement?.querySelector<HTMLElement>("[data-conn-status]");
  const word = statusWord(conn.dataset.state, conn.dataset.health);
  if (live && live.textContent !== word) live.textContent = word;
}

// connectionLabel is the control's visible text.
export function connectionLabel(conn: HTMLElement): string {
  return connectionParts(conn)?.label.textContent ?? conn.textContent ?? "";
}

// buildConnection returns the status bar's left cluster: the control, its hidden description and
// status, and the read-only tag a shared view carries.
export function buildConnection(): HTMLElement {
  const left = document.createElement("div");
  left.dataset.cluster = "";

  const conn = document.createElement("button");
  conn.type = "button";
  conn.id = "console-conn";
  conn.className = "pf-v6-c-button pf-m-link pf-m-inline";
  const mark = document.createElement("span");
  mark.className = "pf-v6-c-button__icon pf-m-start";
  mark.dataset.connMark = "";
  const label = document.createElement("span");
  label.className = "pf-v6-c-button__text";
  label.dataset.connLabel = "";
  conn.append(mark, label);
  conn.setAttribute("aria-describedby", "console-conn-hint");

  const hint = document.createElement("span");
  hint.id = "console-conn-hint";
  hint.className = "pf-v6-screen-reader";
  hint.dataset.connHint = "";
  const live = document.createElement("span");
  live.className = "pf-v6-screen-reader";
  live.setAttribute("role", "status");
  live.dataset.connStatus = "";

  left.append(conn, hint, live);

  // The fragment decides demo, the same authority the readiness pulse and the connect screen use. Safe
  // to decide at construction: demo is reachable only through the workspace menu, which sets #demo and
  // remounts every tab.
  const demoing = wantsDemo(parseHash());
  writeConnection(conn, {
    label: demoing ? "demo" : "not connected",
    state: demoing ? "demo" : "none",
    hint: demoing ? DEMO_CONNECTION_HINT : notConnectedHint(getDefaultHost()),
  });
  if (isReadOnly()) {
    const viewOnly = document.createElement("span");
    viewOnly.className = "pf-v6-c-label pf-m-compact";
    viewOnly.dataset.viewonly = "";
    viewOnly.title = "This is a read-only view shared over the network.";
    const text = document.createElement("span");
    text.className = "pf-v6-c-label__content";
    text.textContent = "view only";
    viewOnly.append(text);
    left.append(viewOnly);
  }
  return left;
}
