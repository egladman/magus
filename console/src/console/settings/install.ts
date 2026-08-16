// install.ts - the Settings "Install" section: one button that raises the browser's install offer for
// the console, and an honest readout when there is no offer to raise.
//
// It is NOT part of the staged-config model the rest of the General tab uses. Installing is an act on the
// BROWSER, not a console preference: there is nothing to persist, nothing to diff, and nothing a Reset
// could undo. So it applies immediately, like the daemon-facing Access and Memory sections.
//
// The offer itself is captured at shell boot (lib/install.ts) - by the time this section mounts the
// browser's one `beforeinstallprompt` has long fired. This only renders whatever state the store is in
// and subscribes for the rest.

import type { InstallState, InstallStore } from "../../lib/install";
import { showToast } from "../../lib/refresh-toast";
import { h } from "../view";

// What installing actually buys, said once. Every state below is a variation on whether it is available.
const PITCH =
  "Installing gives the console its own window and icon, drops the browser chrome, and keeps a cached shell that opens without waiting on the network.";

// The status line per state. Each says what is true AND what to do about it, so no state is a dead end.
function statusText(state: InstallState, hint: string): string {
  switch (state) {
    case "installed":
      return "The console is installed on this device. Remove it from your system's app list or the browser's app settings.";
    case "ready":
      return PITCH;
    case "dismissed":
      return "You dismissed the install prompt. Your browser will offer it again on a later visit, so reload the console to be asked once more.";
    case "manual":
      return (
        "This browser has no install button a page can raise, but it can still install the console. " +
        hint
      );
    case "pending":
      // The browser never says WHY it withheld the offer, so this names the likeliest cause rather than
      // asserting one. That cause bites this app specifically: a LAN share link is plain http, which no
      // browser treats as a secure origin, so the console is not installable on the phone it went to.
      //
      // The last sentence is the point of this state. Only the AUTOMATIC prompt is gated on a secure
      // origin - the browser's own menu is not, and Safari's Share sheet route is a bookmark that is not
      // gated at all. So the operator who arrived here over a LAN share still has a way through, and
      // this must not read as a dead end.
      return "Your browser has not offered to install the console from this address. The automatic prompt needs a secure origin: the loopback address the daemon prints, or an https:// URL. A plain http:// address on the local network never qualifies. Your browser's own menu may still offer Add to Home Screen.";
  }
}

// buildInstallSection builds the section body and keeps it in step with the store. Returns the body and
// a destroy() the surface calls on teardown, so a store that outlives the surface (it is created once at
// boot and lives for the tab's lifetime) never repaints a detached node.
export function buildInstallSection(store: InstallStore): { el: HTMLElement; destroy(): void } {
  const body = h("div", "console-settings-install");

  const status = h("p", "console-settings-install__status");
  status.setAttribute("role", "status");
  status.setAttribute("aria-live", "polite");

  const installBtn = h("button", "pf-v6-c-button pf-m-primary", "Install") as HTMLButtonElement;
  installBtn.type = "button";
  installBtn.title = "Install the console as an app on this device";
  installBtn.addEventListener("click", () => {
    installBtn.disabled = true;
    void store.prompt().then(
      (outcome) => {
        if (outcome === "accepted") showToast("Settings", "Installing the console.");
        // A dismissal needs no toast: paint() has already replaced the button with the line explaining
        // that the browser will ask again, which is the whole of what there is to say.
        if (outcome === "unavailable")
          showToast(
            "Settings",
            "The install offer expired. Reload the console and try again.",
            "warn",
          );
        paint();
      },
      () => {
        showToast("Settings", "The browser refused the install prompt.", "error");
        paint();
      },
    );
  });

  // The button only exists where it works. Every other state is carried by the status line above, which
  // says what to do instead - a permanently disabled button says nothing and invites a click that cannot
  // do anything.
  function paint(): void {
    const state = store.state();
    status.textContent = statusText(state, store.manualHint());
    body.dataset.state = state;
    const ready = state === "ready";
    installBtn.hidden = !ready;
    installBtn.disabled = !ready;
  }

  const unsubscribe = store.subscribe(() => paint());
  body.append(status, installBtn);
  paint();

  return {
    el: body,
    destroy(): void {
      unsubscribe();
    },
  };
}
