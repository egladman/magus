// toast.ts - the DOM half of a toast, with no notification-history side effect. Two callers need
// exactly that: refresh-toast.ts's showToast (which records the entry itself) and the notification
// center (which toasts an entry it has just admitted, so a deduplicated failure never toasts twice).
// A module of its own because each of those imports the other's module otherwise.
//
// A toast is a PatternFly Alert in the one toast Alert group. The group is a persistent aria-live
// region mounted at startup: a region inserted together with its first message is announced by
// fewer screen readers than one that was already there. Several bundles each carry a copy of this
// module, so everything that must be shared (the group, the live toasts) is read from the DOM and
// no state lives in a module variable.

import { alertIcon, alertName, alertTitle, type AlertVariant } from "../ui/alert";

export type ToastKind = "ok" | "warn" | "error";

export interface ToastLink {
  label: string;
  href?: string;
  run?: () => void | Promise<void>;
}

// TOAST_MS is how long a toast stays when the caller gives no duration.
export const TOAST_MS = 8000;

// MAX_TOASTS caps the stack; a burst past it dismisses the oldest rather than walling the page.
const MAX_TOASTS = 5;

// Time the exit transition gets before the node is removed. PF's slide-out is shorter than this.
const EXIT_MS = 400;

const GROUP_ID = "console-toasts";

export interface ToastSpec {
  source: string;
  message: string;
  // "info" is for a prompt that is neither a success nor a problem (a reload offer).
  kind?: ToastKind | "info";
  // Action buttons, rendered inline-link style after the message.
  actions?: ToastLink[];
  // Milliseconds before it dismisses itself; 0 keeps it until the reader closes it.
  ms?: number;
  // A toast with this key already on screen is returned instead of a second one.
  key?: string;
  onDismiss?: () => void;
}

export interface ToastHandle {
  readonly el: HTMLElement;
  // setMessage rewrites the toast's text in place, for a countdown.
  setMessage(text: string): void;
  dismiss(): void;
}

const VARIANT: Record<ToastKind | "info", AlertVariant> = {
  ok: "success",
  warn: "warning",
  error: "danger",
  info: "info",
};

const CLOSE_PATH =
  "M17.8 16.2 11.59 10l6.21-6.21c.42-.46.39-1.17-.07-1.59-.43-.4-1.09-.4-1.52 0l-6.2 6.2-6.22-6.19c-.44-.44-1.15-.44-1.59 0-.44.44-.44 1.15 0 1.59l6.2 6.21-6.2 6.2c-.42.46-.39 1.17.07 1.59.43.4 1.09.4 1.52 0L10 11.59l6.2 6.2c.44.44 1.15.44 1.59 0 .44-.45.44-1.16 0-1.6Z";

// mountToastGroup returns the toast Alert group, creating it on first use.
export function mountToastGroup(): HTMLElement {
  const found = document.getElementById(GROUP_ID);
  if (found) return found;
  const group = document.createElement("ul");
  group.id = GROUP_ID;
  group.className = "pf-v6-c-alert-group pf-m-toast";
  group.setAttribute("role", "list");
  group.setAttribute("aria-live", "polite");
  group.setAttribute("aria-atomic", "false");
  group.setAttribute("aria-relevant", "additions");
  document.body.append(group);
  return group;
}

function closeIcon(): HTMLElement {
  const ns = "http://www.w3.org/2000/svg";
  const icon = document.createElement("span");
  icon.className = "pf-v6-c-button__icon";
  const svg = document.createElementNS(ns, "svg");
  svg.setAttribute("class", "pf-v6-svg");
  svg.setAttribute("viewBox", "0 0 20 20");
  svg.setAttribute("fill", "currentColor");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("width", "1em");
  svg.setAttribute("height", "1em");
  const path = document.createElementNS(ns, "path");
  path.setAttribute("d", CLOSE_PATH);
  svg.append(path);
  icon.append(svg);
  return icon;
}

function reducedMotion(): boolean {
  return typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches;
}

function runLink(link: ToastLink): void {
  if (link.run) void link.run();
  else if (link.href) location.assign(link.href);
}

// pushToast shows a toast on top of the stack. It stays while the pointer rests on it or focus is
// inside it, and its timer resumes with the time it had left.
export function pushToast(spec: ToastSpec): ToastHandle {
  const group = mountToastGroup();
  const key = spec.key ?? (spec.kind ?? "ok") + "|" + spec.source + "|" + spec.message;
  for (const live of group.querySelectorAll<HTMLElement>("[data-toast-key]")) {
    if (live.dataset.toastKey === key && !live.classList.contains("pf-m-outgoing")) {
      return handleFor(live, () => {});
    }
  }

  const variant = VARIANT[spec.kind ?? "ok"];
  const item = document.createElement("li");
  item.className = "pf-v6-c-alert-group__item pf-m-incoming";
  item.dataset.toastKey = key;

  const alert = document.createElement("div");
  alert.className = "pf-v6-c-alert pf-m-" + variant;
  alert.setAttribute("aria-label", alertName(variant));
  if (variant === "danger") alert.setAttribute("role", "alert");
  alert.append(alertIcon(variant), alertTitle(variant, spec.source));

  const description = document.createElement("div");
  description.className = "pf-v6-c-alert__description";
  const text = document.createElement("p");
  text.textContent = spec.message;
  description.append(text);
  alert.append(description);

  const actions = (spec.actions ?? []).filter((a) => a.run || a.href);
  if (actions.length > 0) {
    const row = document.createElement("div");
    row.className = "pf-v6-c-alert__action-group";
    for (const link of actions) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "pf-v6-c-button pf-m-link pf-m-inline";
      const label = document.createElement("span");
      label.className = "pf-v6-c-button__text";
      label.textContent = link.label || "Open";
      button.append(label);
      button.addEventListener("click", () => runLink(link));
      row.append(button);
    }
    alert.append(row);
  }

  const close = document.createElement("button");
  close.type = "button";
  close.className = "pf-v6-c-button pf-m-plain";
  const closeName = "Close " + alertName(variant).toLowerCase() + ": " + spec.source;
  close.setAttribute("aria-label", closeName);
  close.append(closeIcon());
  const action = document.createElement("div");
  action.className = "pf-v6-c-alert__action";
  action.append(close);
  alert.append(action);
  item.append(alert);

  const total = spec.ms ?? TOAST_MS;
  let remaining = total;
  let startedAt = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let hovered = false;
  let focused = false;
  let gone = false;

  const stop = (): void => {
    if (timer === undefined) return;
    clearTimeout(timer);
    timer = undefined;
    remaining = Math.max(1000, remaining - (Date.now() - startedAt));
  };
  const start = (): void => {
    if (total <= 0 || gone || hovered || focused || timer !== undefined) return;
    startedAt = Date.now();
    timer = setTimeout(dismiss, remaining);
  };
  function dismiss(): void {
    if (gone) return;
    gone = true;
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
    const hadFocus = item.contains(document.activeElement);
    item.classList.remove("pf-m-incoming");
    item.classList.add("pf-m-outgoing");
    if (reducedMotion()) item.remove();
    else setTimeout(() => item.remove(), EXIT_MS);
    if (hadFocus && document.activeElement instanceof HTMLElement) document.activeElement.blur();
    spec.onDismiss?.();
  }
  dismissers.set(item, dismiss);

  item.addEventListener("mouseenter", () => {
    hovered = true;
    stop();
  });
  item.addEventListener("mouseleave", () => {
    hovered = false;
    start();
  });
  item.addEventListener("focusin", () => {
    focused = true;
    stop();
  });
  item.addEventListener("focusout", (e) => {
    if (e.relatedTarget instanceof Node && item.contains(e.relatedTarget)) return;
    focused = false;
    start();
  });
  close.addEventListener("click", dismiss);

  group.prepend(item);
  // PF slides an item in from above while it carries pf-m-incoming; drop the class a frame later.
  const settle = (): void => item.classList.remove("pf-m-incoming");
  if (typeof requestAnimationFrame === "function") requestAnimationFrame(settle);
  else setTimeout(settle, 0);

  const live = [...group.children].filter((c) => !c.classList.contains("pf-m-outgoing"));
  for (const extra of live.slice(MAX_TOASTS)) dismissers.get(extra as HTMLElement)?.();

  start();
  return handleFor(item, dismiss);
}

// dismissers lets a later call close a toast it did not create (the stack cap). A WeakMap, so a
// removed toast takes its entry with it.
const dismissers = new WeakMap<HTMLElement, () => void>();

function handleFor(item: HTMLElement, dismiss: () => void): ToastHandle {
  return {
    el: item,
    setMessage(text: string): void {
      const p = item.querySelector(".pf-v6-c-alert__description p");
      if (p) p.textContent = text;
    },
    dismiss: () => (dismissers.get(item) ?? dismiss)(),
  };
}

// renderTransientToast shows a toast for a notification and returns nothing, as its callers always
// have. Warnings and errors stay as long as confirmations now: all of them wait out the same 8s, and
// the pause on hover is what lets a long message be read.
export function renderTransientToast(
  source: string,
  message: string,
  kind: ToastKind,
  link?: ToastLink,
  ms?: number,
): void {
  if (typeof document === "undefined") return;
  pushToast({ source, message, kind, actions: link ? [link] : undefined, ms });
}

// sourceChip builds the quiet label naming the app that raised a toast or history entry.
export function sourceChip(source: string): HTMLElement {
  const chip = document.createElement("span");
  chip.className = "pf-v6-c-label pf-m-compact";
  const content = document.createElement("span");
  content.className = "pf-v6-c-label__content";
  const text = document.createElement("span");
  text.className = "pf-v6-c-label__text";
  text.textContent = source;
  content.append(text);
  chip.append(content);
  return chip;
}

if (typeof document !== "undefined") {
  if (document.body) mountToastGroup();
  else document.addEventListener("DOMContentLoaded", () => mountToastGroup(), { once: true });
}
