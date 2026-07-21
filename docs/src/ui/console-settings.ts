// console-settings.ts - the gear-button settings panel shared by the console apps. A page supplies
// the trigger (#settings-btn) and the panel (#settings-panel with #settings-poll / #settings-host);
// this fills the controls from localStorage (lib/settings), persists edits, and wires open/close
// (the gear, a click outside, Escape). No-ops where there is no gear, like every other main.js
// module. These are BROWSER-side prefs the operator controls; the dashboard reads them on load.
import { getPollMs, setPollMs, getDefaultHost, setDefaultHost } from "../lib/settings.js";
import { showRefreshToast } from "../lib/refresh-toast.js";
import { registerPopup, notifyPopupOpen } from "../site/popups.js";

export function initConsoleSettings(): void {
  const btn = document.getElementById("settings-btn");
  const panel = document.getElementById("settings-panel");
  if (!btn || !panel) return;

  const poll = document.getElementById("settings-poll") as HTMLSelectElement | null;
  const host = document.getElementById("settings-host") as HTMLInputElement | null;

  // The dashboard reads poll/host at load, so a change takes effect on the next reload.
  // Capture what booted so we only nag when the CURRENT state differs (reverting an edit
  // back to the applied value shouldn't leave a stale prompt).
  const appliedPoll = getPollMs();
  const appliedHost = getDefaultHost();
  const maybePromptReload = (): void => {
    if (getPollMs() !== appliedPoll || getDefaultHost() !== appliedHost) {
      showRefreshToast("Console settings changed. Reload to apply.");
    }
  };

  // Seed the controls from the stored prefs.
  if (poll) poll.value = String(getPollMs());
  if (host) host.value = getDefaultHost();

  if (poll) poll.addEventListener("change", () => { setPollMs(Number(poll.value)); maybePromptReload(); });
  if (host) host.addEventListener("change", () => { setDefaultHost(host.value); maybePromptReload(); });

  let open = false;
  // Registered with the popup coordinator so opening another overlay (the nav menu,
  // the reference drawer) closes this panel, and opening this panel closes them.
  const dismissable = { close: (): void => setOpen(false) };
  registerPopup(dismissable);

  const render = (): void => {
    panel.hidden = !open;
    btn.setAttribute("aria-expanded", open ? "true" : "false");
  };
  const setOpen = (v: boolean): void => { open = v; render(); if (v) notifyPopupOpen(dismissable); };

  // No stopPropagation: cross-popup dismissal is the coordinator's job now, not a
  // side effect of halting event bubbling. The outside-click handler below already
  // ignores clicks on the gear (btn.contains), so the panel it just opened survives.
  btn.addEventListener("click", () => { setOpen(!open); });
  // A click anywhere outside the panel (and not on the gear) closes it.
  document.addEventListener("click", (e) => {
    if (!open) return;
    const t = e.target as Node;
    if (panel.contains(t) || btn.contains(t)) return;
    setOpen(false);
  });
  document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key === "Escape" && open) setOpen(false);
  });

  render();
}
