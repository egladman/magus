// toast.ts - the DOM half of a transient toast, with no notification-history side effect. Two callers
// need exactly that: refresh-toast.ts's showToast (which records the entry itself) and the notification
// center (which toasts an entry it has just admitted, so a deduplicated failure never toasts twice).
// A module of its own because each of those imports the other's module otherwise. Styled by
// .console-shell-toast in overrides.css.

export type ToastKind = "ok" | "warn" | "error";

export interface ToastLink {
  label: string;
  href?: string;
  run?: () => void | Promise<void>;
}

// renderTransientToast replaces any transient toast already up and removes itself after ms, which
// defaults to longer for a warn or error: those have to be read.
export function renderTransientToast(
  source: string,
  message: string,
  kind: ToastKind,
  link?: ToastLink,
  ms?: number,
): void {
  if (typeof document === "undefined") return;
  document.querySelector(".console-shell-toast--transient")?.remove();
  const toast = document.createElement("div");
  toast.className = "console-shell-toast console-shell-toast--transient";
  toast.dataset.kind = kind;
  // Only a hard error is assertive; a warn is a passive status the operator can read at leisure.
  toast.setAttribute("role", kind === "error" ? "alert" : "status");
  toast.append(sourceChip(source));
  const msg = document.createElement("span");
  msg.textContent = message;
  toast.appendChild(msg);
  if (link && (link.run || link.href)) {
    const action = document.createElement("button");
    action.type = "button";
    action.textContent = link.label || "Open";
    action.addEventListener("click", () => {
      if (link.run) void link.run();
      else if (link.href) location.assign(link.href);
    });
    toast.appendChild(action);
  }
  document.body.appendChild(toast);
  setTimeout(() => toast.remove(), ms ?? (kind === "ok" ? 2600 : 6000));
}

// sourceChip builds the quiet, tag-like source label prepended to a toast, matching the source shown
// on the history entry.
export function sourceChip(source: string): HTMLElement {
  const chip = document.createElement("span");
  chip.className = "console-shell-toast__source";
  chip.textContent = source;
  return chip;
}
