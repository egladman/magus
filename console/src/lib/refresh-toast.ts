// refresh-toast.ts - a small bottom-left "reload" prompt shared by service-worker-register
// (a newer version of the assets is available) and console-settings (a browser-side pref
// changed and takes effect on reload). Idempotent: a second call while a toast is already up
// is a no-op, so one Refresh button covers overlapping reasons to reload. Styled by .console-shell-toast
// in overrides.css.
export function showRefreshToast(message: string): void {
  if (document.querySelector(".console-shell-toast")) return;
  const toast = document.createElement("div");
  toast.className = "console-shell-toast";
  toast.setAttribute("role", "status");

  const msg = document.createElement("span");
  msg.textContent = message;
  toast.appendChild(msg);

  const refresh = document.createElement("button");
  refresh.type = "button";
  refresh.textContent = "Refresh";
  refresh.addEventListener("click", () => {
    location.reload();
  });
  toast.appendChild(refresh);

  document.body.appendChild(toast);
}

// showToast pops a transient, auto-dismissing toast (no action button) in the same bottom-left slot as
// the reload prompt: a Settings save/apply confirmation ("ok"), a "warn" (a partial success worth
// reading, e.g. an import that dropped some keys), or an "error" explaining why something failed.
// A fresh call replaces any transient toast already up, so rapid calls do not stack. Errors and warnings
// linger longer than confirmations - they have to be read. Styled by .console-shell-toast in overrides.css.
export function showToast(message: string, kind: "ok" | "warn" | "error" = "ok", ms = kind === "ok" ? 2600 : 6000): void {
  document.querySelector(".console-shell-toast--transient")?.remove();
  const toast = document.createElement("div");
  toast.className = "console-shell-toast console-shell-toast--transient";
  toast.dataset.kind = kind;
  // Only a hard error is assertive; a warn is a passive status the operator can read at leisure.
  toast.setAttribute("role", kind === "error" ? "alert" : "status");
  const msg = document.createElement("span");
  msg.textContent = message;
  toast.appendChild(msg);
  document.body.appendChild(toast);
  setTimeout(() => toast.remove(), ms);
}
