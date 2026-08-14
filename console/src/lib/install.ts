// install.ts - the PWA install-prompt store: capture the browser's one install offer and hold it until
// the operator asks for it.
//
// Chromium fires `beforeinstallprompt` ONCE, early in the page's life, and only if the page is not
// already installed and meets the install criteria. Nothing may prompt from that handler - the whole
// point of preventDefault()ing it is to defer the offer to a moment the operator chose. So the event has
// to be caught at SHELL BOOT and stashed; a listener added later (when the Settings surface mounts, long
// after load) is registered for an event that has already been and gone. The shell therefore creates this
// store at boot and hands it to the Settings surface as a dep.
//
// The captured event is SINGLE-USE. prompt() may be called on it exactly once; a second call throws, so a
// dismissed offer cannot be re-raised from the page. The browser re-fires the event on a later navigation
// instead, which is why "dismissed" is its own state with "reload to be offered again" as the honest copy
// rather than a button that silently does nothing.
//
// Firefox and Safari implement no part of this. They are not failures to report - Safari has a real
// manual route (Share > Add to Home Screen on iOS, File > Add to Dock on macOS) - so the store reports
// "manual" and the surface prints the route instead of a dead button.

// The Chromium-only event. Not in lib.dom, so it is declared here rather than cast at each use.
export interface BeforeInstallPromptEvent extends Event {
  prompt(): Promise<void>;
  readonly userChoice: Promise<{ outcome: "accepted" | "dismissed"; platform: string }>;
}

// What the operator can do about installing, right now:
//   installed  - already running as an app (or the browser just told us it was installed)
//   ready      - an offer is in hand: the Install button works
//   dismissed  - the offer was raised and declined; the browser will re-offer on a later load
//   manual     - this browser has no install API, but does have a documented manual route
//   pending    - the API exists and has not offered yet: the install criteria are not met here
export type InstallState = "installed" | "ready" | "dismissed" | "manual" | "pending";

export type PromptOutcome = "accepted" | "dismissed" | "unavailable";

// The browser surface the store depends on, injected so the store is exercised without a DOM.
export interface InstallHost {
  on(type: string, fn: (ev: Event) => void): void;
  // Already running as an installed app: a standalone-family display mode, or iOS's own flag.
  installed(): boolean;
  // Whether this browser exposes the install-prompt API at all (Chromium yes, Firefox/Safari no).
  hasPromptApi(): boolean;
  // iOS/iPadOS Safari - the one platform with no API and a genuinely different manual route.
  isIOS(): boolean;
}

export interface InstallStore {
  state(): InstallState;
  // The manual route for this browser, for the "manual" state. Empty elsewhere.
  manualHint(): string;
  // Raise the captured offer. Resolves with the operator's choice, or "unavailable" when there was
  // no offer to raise (so a caller never has to guess whether the click did anything).
  prompt(): Promise<PromptOutcome>;
  subscribe(fn: (state: InstallState) => void): () => void;
}

// iOS puts the install route behind the system Share sheet; every other browser without the API puts it
// in an application menu. Naming Safari's macOS route explicitly is worth the sentence - it is the only
// desktop browser where install exists but is unreachable from script.
const IOS_HINT = "Tap the Share button, then Add to Home Screen.";
const DESKTOP_HINT =
  "In Safari, choose File > Add to Dock. Other browsers put install in the address bar or the browser menu.";

export function createInstallStore(host: InstallHost): InstallStore {
  let deferred: BeforeInstallPromptEvent | null = null;
  let declined = false;
  let installed = host.installed();
  const listeners = new Set<(s: InstallState) => void>();

  const state = (): InstallState => {
    if (installed) return "installed";
    if (deferred) return "ready";
    if (declined) return "dismissed";
    return host.hasPromptApi() ? "pending" : "manual";
  };

  const notify = (): void => {
    const s = state();
    for (const fn of [...listeners]) fn(s);
  };

  // preventDefault() is what stops Chromium raising its own mini-infobar, which is the whole reason the
  // event is deferrable. Without it the browser owns the moment and this store never gets a turn.
  host.on("beforeinstallprompt", (ev) => {
    ev.preventDefault();
    deferred = ev as BeforeInstallPromptEvent;
    declined = false; // a re-offer supersedes an earlier decline
    notify();
  });

  // Fires when the install completes - including an install started from the browser's own menu rather
  // than from this page, which is why the state is not derived from prompt()'s outcome alone.
  host.on("appinstalled", () => {
    installed = true;
    deferred = null;
    notify();
  });

  return {
    state,
    manualHint: () => (state() !== "manual" ? "" : host.isIOS() ? IOS_HINT : DESKTOP_HINT),
    async prompt(): Promise<PromptOutcome> {
      const ev = deferred;
      if (!ev) return "unavailable";
      // Drop the reference BEFORE prompting: the event is single-use, so holding it would leave the
      // button live for a second click that throws.
      deferred = null;
      notify();
      await ev.prompt();
      const { outcome } = await ev.userChoice;
      if (outcome === "accepted") installed = true;
      else declined = true;
      notify();
      return outcome;
    },
    subscribe(fn): () => void {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
  };
}

// The display modes that mean "running as an installed app". window-controls-overlay is included because
// the console's manifest asks for it first (manifest.webmanifest's display_override), so an installed
// console on a desktop that honors it reports THAT mode and not "standalone".
const APP_DISPLAY_MODES = ["standalone", "window-controls-overlay", "minimal-ui", "fullscreen"];

// browserInstallHost binds the store to the real window. Every access is guarded for the non-DOM
// contexts the plain unit tests run in.
export function browserInstallHost(): InstallHost {
  return {
    on(type, fn): void {
      if (typeof window !== "undefined") window.addEventListener(type, fn);
    },
    installed(): boolean {
      if (typeof window === "undefined") return false;
      // iOS Safari has no display-mode media query for this; navigator.standalone is its own signal.
      const nav = navigator as Navigator & { standalone?: boolean };
      if (nav.standalone === true) return true;
      if (typeof window.matchMedia !== "function") return false;
      return APP_DISPLAY_MODES.some((m) => window.matchMedia("(display-mode: " + m + ")").matches);
    },
    hasPromptApi: (): boolean => typeof window !== "undefined" && "onbeforeinstallprompt" in window,
    // "standalone" on navigator is an iOS/iPadOS Safari extension and nothing else ships it, so its mere
    // PRESENCE (not its value) identifies the platform without reading the user agent.
    isIOS: (): boolean => typeof navigator !== "undefined" && "standalone" in navigator,
  };
}
