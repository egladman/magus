// share.ts - the "Share a read-only view" pop-out panel in the console shell.
//
// A right-docked side panel (the notification-center / reference-drawer idiom), not a
// centered modal: it has the room to pick a duration, then show the QR and link
// without squishing. The status-bar share button toggles it; the panel dismisses on
// Escape or an outside click.
//
// Two phases in one panel body:
//   1. Pick a lifetime (a quick phone glance vs. an all-day display on a TV), then
//      Generate. Nothing is minted until Generate - so the operator calibrates before
//      a token and QR exist.
//   2. The minted link: a QR to scan on a phone, the URL as a click-to-copy line to
//      paste onto any other device on the network, and when it expires. "New link"
//      returns to the picker (a fresh mint supersedes the old token).
//
// The daemon does the real work (mint token, open the LAN listener); this module is
// presentation plus one authenticated POST that carries the chosen ttl_seconds (the
// daemon clamps it). Every failure is surfaced as a toast, never a silent no-op.
//
// It is a loopback-console affordance only: a read-only viewer over the LAN never
// mounts the panel, and the daemon would reject the share trigger anyway (it is
// loopback + bearer guarded).

import { resolveDaemonHost, authHeaders, getLiveToken } from "../lib/daemon";
import { showToast } from "../lib/refresh-toast";
import { encodeToCanvas } from "../lib/qr";

// The lifetimes the operator picks before minting, in seconds (sent as ttl_seconds).
// The daemon clamps to [share.MinTTL, share.MaxTTL]; the default is the first entry.
const DURATIONS: readonly { label: string; seconds: number }[] = [
  { label: "15 min", seconds: 15 * 60 },
  { label: "1 hour", seconds: 60 * 60 },
  { label: "8 hours", seconds: 8 * 60 * 60 },
  { label: "24 hours", seconds: 24 * 60 * 60 },
];

export interface SharePanel {
  open(): void;
  close(): void;
  toggle(): void;
}

// mountSharePanel builds the singleton share panel (hidden) once and returns its
// controller. The shell wires the status-bar share button (rebuilt per surface) to
// toggle() through one delegated click, mirroring how the Panes tray drives its popup.
export function mountSharePanel(): SharePanel {
  const panel = document.createElement("section");
  panel.className = "console-shell-share";
  panel.id = "console-sharepanel";
  panel.setAttribute("role", "dialog");
  panel.setAttribute("aria-label", "Share a read-only view");
  panel.hidden = true;

  const head = document.createElement("div");
  head.className = "console-shell-share__head";
  const title = document.createElement("span");
  title.className = "console-shell-share__title";
  title.textContent = "Share a read-only view";
  const closeBtn = document.createElement("button");
  closeBtn.type = "button";
  closeBtn.className = "pf-v6-c-button pf-m-plain console-shell-share__close";
  closeBtn.setAttribute("aria-label", "Close share panel");
  closeBtn.textContent = "×"; // multiplication sign, the console's close glyph
  head.append(title, closeBtn);

  const body = document.createElement("div");
  body.className = "console-shell-share__body";
  panel.append(head, body);
  document.body.append(panel);

  let open = false;
  let selectedSeconds = DURATIONS[0].seconds;

  const setOpen = (v: boolean): void => {
    if (v === open) return;
    open = v;
    panel.hidden = !v;
    panel.setAttribute("aria-hidden", v ? "false" : "true");
    if (v) {
      renderPicker(); // always start on the picker, even after a prior mint
      requestAnimationFrame(() => body.querySelector("button")?.focus());
    }
  };

  closeBtn.addEventListener("click", () => setOpen(false));

  // Dismiss on an outside pointerdown or Escape. A click on any status-bar share
  // button ([data-share-toggle]) is that button's own toggle, so ignore it here to
  // avoid closing then immediately reopening (or vice versa).
  document.addEventListener("pointerdown", (e) => {
    if (!open) return;
    const t = e.target;
    if (!(t instanceof Node)) return;
    if (panel.contains(t)) return;
    if (t instanceof Element && t.closest("[data-share-toggle]")) return;
    setOpen(false);
  });
  document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key === "Escape" && open) setOpen(false);
  });

  // renderPicker draws phase 1: the duration toggle-group + Generate + a disclosure.
  function renderPicker(): void {
    body.replaceChildren();

    const pickLabel = document.createElement("p");
    pickLabel.className = "console-shell-share__picklabel";
    pickLabel.textContent = "Link stays live for";
    body.append(pickLabel);

    const group = document.createElement("div");
    group.className = "pf-v6-c-toggle-group console-shell-share__durations";
    group.setAttribute("role", "group");
    group.setAttribute("aria-label", "Share link lifetime");
    for (const d of DURATIONS) {
      const item = document.createElement("div");
      item.className = "pf-v6-c-toggle-group__item";
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "pf-v6-c-toggle-group__button";
      if (d.seconds === selectedSeconds) btn.classList.add("pf-m-selected");
      btn.setAttribute("aria-pressed", d.seconds === selectedSeconds ? "true" : "false");
      const text = document.createElement("span");
      text.className = "pf-v6-c-toggle-group__text";
      text.textContent = d.label;
      btn.append(text);
      btn.addEventListener("click", () => {
        selectedSeconds = d.seconds;
        for (const b of group.querySelectorAll(".pf-v6-c-toggle-group__button")) {
          const on = b === btn;
          b.classList.toggle("pf-m-selected", on);
          b.setAttribute("aria-pressed", on ? "true" : "false");
        }
      });
      item.append(btn);
      group.append(item);
    }
    body.append(group);

    const note = document.createElement("p");
    note.className = "console-shell-share__note";
    note.textContent =
      "Any device on the same network can open the link. It is read-only, and expires on its own.";
    body.append(note);

    const gen = document.createElement("button");
    gen.type = "button";
    gen.className = "pf-v6-c-button pf-m-primary console-shell-share__generate";
    gen.textContent = "Generate link";
    gen.addEventListener("click", () => void generate(gen));
    body.append(gen);
  }

  // renderResult draws phase 2: the QR, the click-to-copy URL, the expiry, and a way
  // back to the picker.
  function renderResult(url: string, expiresAt: string, superseded: boolean): void {
    body.replaceChildren();

    if (superseded) {
      const revoked = document.createElement("p");
      revoked.className = "console-shell-share__revoked";
      revoked.textContent = "Previous share link revoked.";
      body.append(revoked);
    }

    // The QR is always black-on-white with a light frame so it scans in either theme.
    const qrFrame = document.createElement("div");
    qrFrame.className = "console-shell-share__qr";
    const canvas = document.createElement("canvas");
    canvas.setAttribute("aria-label", "QR code for the share link");
    try {
      encodeToCanvas(canvas, url, 240);
    } catch {
      // A payload too large for the encoder should never happen for a LAN URL, but
      // never let a QR failure hide the URL itself - the copy line below still works.
      qrFrame.classList.add("console-shell-share__qr--failed");
    }
    qrFrame.append(canvas);
    body.append(qrFrame);

    const copyUrl = (): void => {
      navigator.clipboard?.writeText(url).then(
        () => showToast("Share", "Share link copied.", "ok"),
        () => showToast("Share", "Could not copy the link. Select and copy it manually.", "warn"),
      );
    };
    // The URL is a click-to-copy line: scan the QR on a phone, or copy the link and
    // open it on any other device on the network.
    const link = document.createElement("button");
    link.type = "button";
    link.className = "console-shell-share__url";
    link.textContent = url;
    link.title = "Click to copy";
    link.addEventListener("click", copyUrl);
    body.append(link);

    const expiry = formatExpiry(expiresAt);
    if (expiry) {
      const exp = document.createElement("p");
      exp.className = "console-shell-share__expiry";
      exp.textContent = expiry;
      body.append(exp);
    }

    const note = document.createElement("p");
    note.className = "console-shell-share__note";
    note.textContent = "This link is read-only. Any device on the same network can open it.";
    body.append(note);

    const actions = document.createElement("div");
    actions.className = "console-shell-share__actions";
    const copyBtn = document.createElement("button");
    copyBtn.type = "button";
    copyBtn.className = "pf-v6-c-button pf-m-secondary";
    copyBtn.textContent = "Copy link";
    copyBtn.addEventListener("click", copyUrl);
    const again = document.createElement("button");
    again.type = "button";
    again.className = "pf-v6-c-button pf-m-tertiary console-shell-share__again";
    again.textContent = "New link";
    again.addEventListener("click", renderPicker);
    actions.append(copyBtn, again);
    body.append(actions);
  }

  // generate mints the share for the selected lifetime and swaps the body to the
  // result, or toasts why it could not. The Generate button is disabled while the
  // request is in flight so a double click cannot mint twice.
  async function generate(trigger: HTMLButtonElement): Promise<void> {
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

    trigger.disabled = true;
    let res: Response;
    try {
      res = await fetch("http://" + host + "/api/v1/share", {
        method: "POST",
        headers: { ...authHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ ttl_seconds: selectedSeconds }),
        cache: "no-store",
      });
    } catch {
      trigger.disabled = false;
      showToast("Share", "Could not reach the daemon to start a share. Is it still running?", "error");
      return;
    }
    if (!res.ok) {
      trigger.disabled = false;
      let msg = "Share failed (HTTP " + res.status + ").";
      try {
        const errBody = await res.json();
        if (errBody && typeof errBody.error === "string") msg = errBody.error;
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
      trigger.disabled = false;
      showToast("Share", "The daemon returned an unreadable share response.", "error");
      return;
    }
    if (!data.url) {
      trigger.disabled = false;
      showToast("Share", "The daemon returned an empty share URL.", "error");
      return;
    }
    renderResult(data.url, data.expires_at ?? "", data.superseded === true);
  }

  renderPicker();
  return {
    open: () => setOpen(true),
    close: () => setOpen(false),
    toggle: () => setOpen(!open),
  };
}

// formatExpiry turns an RFC 3339 expiry into a short local-time line, or "" when the
// timestamp is missing or unparseable (the disclosure already states it expires).
function formatExpiry(iso: string): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const time = new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return "Link expires at " + time + ".";
}
