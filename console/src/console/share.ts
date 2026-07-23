// share.ts - the "Share a read-only view" flow in the console shell.
//
// It POSTs the daemon's loopback /api/v1/share (with the same bearer the console
// already uses), then renders a small modal: a QR of the returned LAN URL and the
// URL as a click-to-copy line, so the viewer can either scan it on a phone or paste
// it into any device on the network (a tablet, a laptop, a TV browser). A plain
// disclosure states the same-network requirement, the lifetime, and that the link
// is read-only. The daemon does the real work (mint token, open the LAN listener);
// this module is presentation plus one authenticated fetch. Every failure is
// surfaced as a toast, never a silent no-op, so a missing LAN interface or an old
// daemon reads honestly.
//
// It is a loopback-console affordance only: a read-only viewer over the LAN never
// registers the command, and the daemon would reject the share trigger anyway (it
// is loopback + bearer guarded).

import { resolveDaemonHost, authHeaders, getLiveToken } from "../lib/daemon";
import { showToast } from "../lib/refresh-toast";
import { encodeToCanvas } from "../lib/qr";

// openShareDialog triggers a share and shows the QR modal, or toasts why it could
// not. Exposed as the console.share command's handler.
export async function openShareDialog(): Promise<void> {
  const host = resolveDaemonHost();
  if (!host) {
    showToast(
      "Share",
      "No daemon is connected, so there is nothing to share. Set the daemon address in Settings first.",
      "error",
    );
    return;
  }
  if (!getLiveToken()) {
    showToast(
      "Share",
      "The daemon needs an auth token to share. Open the console via a live link with a token.",
      "error",
    );
    return;
  }

  let res: Response;
  try {
    res = await fetch("http://" + host + "/api/v1/share", {
      method: "POST",
      headers: authHeaders(),
      cache: "no-store",
    });
  } catch {
    showToast(
      "Share",
      "Could not reach the daemon to start a share. Is it still running?",
      "error",
    );
    return;
  }
  if (!res.ok) {
    let msg = "Share failed (HTTP " + res.status + ").";
    try {
      const body = await res.json();
      if (body && typeof body.error === "string") msg = body.error;
    } catch {
      /* non-JSON error body: keep the generic message */
    }
    showToast("Share", msg, "error");
    return;
  }

  let data: { url?: string; expires_at?: string; superseded?: boolean };
  try {
    data = await res.json();
  } catch {
    showToast("Share", "The daemon returned an unreadable share response.", "error");
    return;
  }
  if (!data.url) {
    showToast("Share", "The daemon returned an empty share URL.", "error");
    return;
  }
  renderShareDialog(data.url, data.expires_at ?? "", data.superseded === true);
}

// renderShareDialog builds the modal DOM (no innerHTML - createElement throughout,
// matching the console's convention) and wires Escape / click-outside / Close.
function renderShareDialog(url: string, expiresAt: string, superseded: boolean): void {
  document.querySelector(".console-share-backdrop")?.remove();

  const backdrop = document.createElement("div");
  backdrop.className = "console-share-backdrop";

  const dialog = document.createElement("div");
  dialog.className = "console-share-dialog";
  dialog.setAttribute("role", "dialog");
  dialog.setAttribute("aria-modal", "true");
  dialog.setAttribute("aria-label", "Share a read-only view");

  const heading = document.createElement("h2");
  heading.className = "console-share-dialog__title";
  heading.textContent = "Share a read-only view";
  dialog.append(heading);

  if (superseded) {
    const revoked = document.createElement("p");
    revoked.className = "console-share-dialog__revoked";
    revoked.textContent = "Previous share link revoked.";
    dialog.append(revoked);
  }

  // The QR is always black-on-white with a light frame so it scans in either
  // theme; a themed low-contrast code would not decode reliably.
  const qrFrame = document.createElement("div");
  qrFrame.className = "console-share-dialog__qr";
  const canvas = document.createElement("canvas");
  canvas.setAttribute("aria-label", "QR code for the share link");
  try {
    encodeToCanvas(canvas, url, 240);
  } catch {
    // A payload too large for the encoder should never happen for a LAN URL, but
    // never let a QR failure hide the URL itself - the text link below still works.
    qrFrame.classList.add("console-share-dialog__qr--failed");
  }
  qrFrame.append(canvas);
  dialog.append(qrFrame);

  // Copy the link to the clipboard, shared by the URL line (click-to-copy) and the
  // Copy button so "scan it or paste it anywhere" is one gesture either way.
  const copyUrl = (): void => {
    navigator.clipboard?.writeText(url).then(
      () => showToast("Share", "Share link copied.", "ok"),
      () => showToast("Share", "Could not copy the link. Select and copy it manually.", "warn"),
    );
  };

  // The URL is a click-to-copy line (not just selectable text): scan the QR on a
  // phone, or copy the link and open it on any other device on the network.
  const link = document.createElement("button");
  link.type = "button";
  link.className = "console-share-dialog__url";
  link.textContent = url;
  link.title = "Click to copy";
  link.addEventListener("click", copyUrl);
  dialog.append(link);

  // The one disclosure line Eli asked for: network requirement + lifetime + read-only.
  const note = document.createElement("p");
  note.className = "console-share-dialog__note";
  note.textContent =
    "Any device on the same network can open this. The link works for 15 minutes and is read-only.";
  dialog.append(note);

  const expiry = formatExpiry(expiresAt);
  if (expiry) {
    const exp = document.createElement("p");
    exp.className = "console-share-dialog__expiry";
    exp.textContent = expiry;
    dialog.append(exp);
  }

  const actions = document.createElement("div");
  actions.className = "console-share-dialog__actions";
  const copyBtn = document.createElement("button");
  copyBtn.type = "button";
  copyBtn.className = "pf-v6-c-button pf-m-secondary";
  copyBtn.textContent = "Copy link";
  copyBtn.addEventListener("click", copyUrl);
  const closeBtn = document.createElement("button");
  closeBtn.type = "button";
  closeBtn.className = "pf-v6-c-button pf-m-primary";
  closeBtn.textContent = "Close";
  const close = (): void => {
    backdrop.remove();
    document.removeEventListener("keydown", onKey);
  };
  closeBtn.addEventListener("click", close);
  actions.append(copyBtn, closeBtn);
  dialog.append(actions);

  const onKey = (e: KeyboardEvent): void => {
    if (e.key === "Escape") close();
  };
  document.addEventListener("keydown", onKey);
  backdrop.addEventListener("click", (e) => {
    if (e.target === backdrop) close();
  });

  backdrop.append(dialog);
  document.body.append(backdrop);
  closeBtn.focus();
}

// formatExpiry turns an RFC 3339 expiry into a short local-time line, or "" when
// the timestamp is missing or unparseable (the disclosure line already states the
// lifetime, so a missing exact time is not fatal).
function formatExpiry(iso: string): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const time = new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return "Link expires at " + time + ".";
}
