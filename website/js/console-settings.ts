// console-settings.ts - the gear-button settings panel shared by the console apps. A page supplies
// the trigger (#settings-btn) and the panel (#settings-panel with #settings-poll / #settings-host);
// this fills the controls from localStorage (lib/settings), persists edits, and wires open/close
// (the gear, a click outside, Escape). No-ops where there is no gear, like every other main.js
// module. These are BROWSER-side prefs the operator controls; the dashboard reads them on load.
import { getPollMs, setPollMs, getDefaultHost, setDefaultHost } from "./lib/settings.js";

(function () {
  const btn = document.getElementById("settings-btn");
  const panel = document.getElementById("settings-panel");
  if (!btn || !panel) return;

  const poll = document.getElementById("settings-poll") as HTMLSelectElement | null;
  const host = document.getElementById("settings-host") as HTMLInputElement | null;

  // Seed the controls from the stored prefs.
  if (poll) poll.value = String(getPollMs());
  if (host) host.value = getDefaultHost();

  if (poll) poll.addEventListener("change", () => setPollMs(Number(poll.value)));
  if (host) host.addEventListener("change", () => setDefaultHost(host.value));

  let open = false;
  const render = (): void => {
    panel.hidden = !open;
    btn.setAttribute("aria-expanded", open ? "true" : "false");
  };
  const setOpen = (v: boolean): void => { open = v; render(); };

  btn.addEventListener("click", (e) => { e.stopPropagation(); setOpen(!open); });
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
})();
