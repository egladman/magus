// connectPrompt.ts - the one prompt a surface shows while it has no daemon to read. Every
// daemon-backed surface renders it through renderConnectPrompt, so the words, the actions and the
// docs link cannot drift between surfaces. What a surface shows once it IS connected (nothing kept
// yet, a clean tree) stays the surface's own and goes through renderEmptyMessage: those are facts
// about its data, not the connection.
//
// Nothing here connects or retries on its own. Every way forward is a control the reader presses.

import { parseHash, resolveDaemonHostOrRemembered, wantsDemo } from "../lib/daemon";
import { subscribeDefaultHost } from "../lib/settings";
import type { PageController, PageModule, SearchProvider, TitleSource } from "./page";
import type { ConnectionState } from "./status";
import { h } from "./view";

export const DAEMON_GUIDE_URL = "https://eli.gladman.cc/magus/guides/integrations/daemon/";

// Surfaces are separate bundles with separate command registries, so a surface cannot reach the
// shell through dispatchCommand. The shell listens for this on document and opens the address field.
export const REQUEST_DAEMON_SETTINGS_EVENT = "magus:request-daemon-settings";

// The prompt's states are the status bar's own ConnectionState values, narrowed to the three a
// surface can be stuck in, so the console keeps one vocabulary for a connection.
export type ConnectPromptState =
  | { connection: Extract<ConnectionState, "none"> }
  | { connection: Extract<ConnectionState, "connecting">; host: string }
  | { connection: Extract<ConnectionState, "disconnected">; host: string; reason?: string };

// The elements of a surface's empty state. actions is the [data-empty-ways] row.
export interface EmptyStateSlots {
  title: HTMLElement;
  message: HTMLElement;
  actions: HTMLElement;
}

export interface ConnectPromptOptions {
  // What the surface shows once connected, in one sentence. Shown only in the "none" state.
  purpose?: string;
  // Backs the Retry button in the "disconnected" state. Without it there is no Retry.
  onRetry?: () => void;
}

// renderConnectPrompt writes the prompt for state into slots. Rendering the prompt already on
// screen is a no-op, so a caller that re-renders on every poll does not rebuild the buttons out
// from under the reader's focus.
export function renderConnectPrompt(
  slots: EmptyStateSlots,
  state: ConnectPromptState,
  options: ConnectPromptOptions = {},
): void {
  const key = JSON.stringify([state, options.purpose ?? ""]);
  if (slots.actions.dataset.connectPrompt === key) return;
  slots.actions.dataset.connectPrompt = key;
  switch (state.connection) {
    case "none":
      slots.title.textContent = "No daemon connected";
      slots.message.textContent = options.purpose ?? "";
      slots.actions.replaceChildren(
        startWay("Then open the link it prints, or enter the address it listens on.", [
          wayButton("pf-m-primary", "Set daemon address", requestDaemonSettings),
          daemonGuideLink(),
        ]),
        demoWay(),
      );
      return;
    case "connecting":
      slots.title.textContent = "Connecting";
      slots.message.textContent = "Reaching the daemon at " + state.host + ".";
      slots.actions.replaceChildren();
      return;
    case "disconnected": {
      slots.title.textContent = "Could not reach the daemon";
      slots.message.textContent =
        "The console could not reach " +
        state.host +
        (state.reason ? " (" + state.reason + ")." : ".");
      const retry = options.onRetry;
      slots.actions.replaceChildren(
        startWay("Start it if it is not running, then retry.", [
          ...(retry ? [wayButton("pf-m-primary", "Retry", retry)] : []),
          wayButton("pf-m-secondary", "Change address", requestDaemonSettings),
          daemonGuideLink(),
        ]),
        demoWay(),
      );
      return;
    }
  }
}

// renderEmptyMessage writes an empty state that is not about the connection (nothing kept yet, a
// clean tree) into the same slots, and forgets any prompt rendered there so the next
// renderConnectPrompt draws again rather than assuming it is still on screen.
export function renderEmptyMessage(slots: EmptyStateSlots, title: string, message: string): void {
  delete slots.actions.dataset.connectPrompt;
  slots.actions.replaceChildren();
  slots.title.textContent = title;
  slots.message.textContent = message;
}

// daemonGuideLink opens the daemon guide in a new tab. The URL is absolute because the console is
// often served from the daemon's own origin, where a relative docs path does not exist.
export function daemonGuideLink(): HTMLAnchorElement {
  const link = h("a", "pf-v6-c-button pf-m-link pf-m-inline", "Setup guide");
  link.href = DAEMON_GUIDE_URL;
  link.target = "_blank";
  link.rel = "noopener";
  return link;
}

function requestDaemonSettings(): void {
  document.dispatchEvent(new CustomEvent(REQUEST_DAEMON_SETTINGS_EVENT));
}

function startWay(hint: string, controls: HTMLElement[]): HTMLElement {
  const way = h("div");
  way.dataset.emptyWay = "";
  const label = h("span", undefined, "Start a daemon");
  label.dataset.emptyWayLabel = "";
  const command = h("pre");
  command.dataset.emptyCmd = "";
  command.append(h("code", undefined, "magus server start"));
  const note = h("span", undefined, hint);
  note.dataset.emptyHint = "";
  const row = h("div");
  row.dataset.emptyWayActions = "";
  row.append(...controls);
  way.append(label, command, note, row);
  return way;
}

function demoWay(): HTMLElement {
  const way = h("div");
  way.dataset.emptyWay = "";
  const label = h("span", undefined, "Try the demo");
  label.dataset.emptyWayLabel = "";
  const note = h("span", undefined, DEMO_HINT);
  note.dataset.emptyHint = "";
  way.append(label, note);
  return way;
}

// Exported for the launcher, whose demo way is not a connection prompt but says the same thing.
export const DEMO_HINT = "Pick acme from the Workspace menu. Demo data, no daemon needed.";

function wayButton(modifier: string, label: string, onClick: () => void): HTMLButtonElement {
  const control = h("button", "pf-v6-c-button " + modifier, label);
  control.type = "button";
  control.addEventListener("click", onClick);
  return control;
}

// DaemonNeed is a surface registry entry's declaration that the surface has nothing to show without
// a daemon. purpose is the sentence the connect page shows in its place.
export interface DaemonNeed {
  purpose: string;
}

// requireDaemon wraps module so that activating it with no daemon address and no demo shows the
// shell's connect page instead, and the module is not activated. The surface opens once an address
// is applied (Settings, or another tab) and its pane is not hidden, so a surface that measures its
// DOM at init sees real dimensions. Nothing polls: an applied address is the only trigger. Once
// open, the surface stays open whatever the connection does, and its own inline prompt answers a
// drop. With need undefined, module is returned unchanged.
export function requireDaemon<S, Q>(
  module: PageModule<S, Q>,
  need: DaemonNeed | undefined,
): PageModule<S, Q> {
  if (!need) return module;
  return {
    id: module.id,
    title: module.title,
    activate: async (host) =>
      daemonAvailable() ? module.activate(host) : connectPage(module, host, need),
  };
}

// The order the surfaces resolve their own source in, so the page never stands in front of a
// surface that would have found a daemon.
function daemonAvailable(): boolean {
  return wantsDemo(parseHash()) || resolveDaemonHostOrRemembered() !== null;
}

function connectPage<S, Q>(
  module: PageModule<S, Q>,
  host: HTMLElement,
  need: DaemonNeed,
): PageController<S, Q> {
  const page = h("div", "pf-v6-c-empty-state");
  page.dataset.connectPage = module.id;
  const content = h("div", "pf-v6-c-empty-state__content");
  const header = h("div", "pf-v6-c-empty-state__header");
  const titleBox = h("div", "pf-v6-c-empty-state__title");
  const slots: EmptyStateSlots = {
    title: h("h2", "pf-v6-c-empty-state__title-text"),
    message: h("div", "pf-v6-c-empty-state__body"),
    actions: h("div", "pf-v6-c-empty-state__actions"),
  };
  slots.actions.dataset.emptyWays = "";
  titleBox.append(slots.title);
  header.append(titleBox);
  content.append(header, slots.message, slots.actions);
  page.append(content);
  renderConnectPrompt(slots, { connection: "none" }, { purpose: need.purpose });
  host.replaceChildren(page);

  let inner: PageController<S, Q> | null = null;
  let opening = false;
  let closed = false;
  let visible = false;
  const titleListeners = new Set<(title: string | null) => void>();
  let innerTitleUnsub: (() => void) | null = null;

  const open = (): void => {
    if (closed || inner || opening || !daemonAvailable() || host.closest("[hidden]")) return;
    opening = true;
    unsubscribeHost();
    host.replaceChildren();
    void module.activate(host).then((controller) => {
      opening = false;
      if (closed) {
        controller.deactivate();
        return;
      }
      inner = controller;
      controller.setVisible(visible);
      const src = controller.docTitle;
      if (!src) return;
      for (const fn of titleListeners) fn(src.get());
      innerTitleUnsub = src.subscribe((t) => {
        for (const fn of titleListeners) fn(t);
      });
    });
  };
  const unsubscribeHost = subscribeDefaultHost(open);

  // The tile reads docTitle once, before the surface exists, so it gets a stable source that
  // forwards the surface's once it opens.
  const docTitle: TitleSource = {
    get: () => inner?.docTitle?.get() ?? null,
    subscribe(fn) {
      titleListeners.add(fn);
      return () => titleListeners.delete(fn);
    },
  };

  return {
    get search(): SearchProvider<Q> {
      return inner?.search ?? (noSearch as SearchProvider<Q>);
    },
    get state(): S | undefined {
      return inner?.state;
    },
    docTitle,
    setVisible(next) {
      visible = next;
      if (inner) inner.setVisible(next);
      // An address applied while this pane was hidden opens the surface when it is revealed.
      else open();
    },
    deactivate() {
      closed = true;
      unsubscribeHost();
      innerTitleUnsub?.();
      titleListeners.clear();
      if (inner) inner.deactivate();
      else host.replaceChildren();
    },
  };
}

const noSearch: SearchProvider<null> = {
  placeholder: "",
  parse: () => null,
  apply: () => ({ matches: 0 }),
};
