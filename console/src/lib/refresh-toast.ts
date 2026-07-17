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
// the reload prompt: a Settings save/apply confirmation, or an error explaining why something failed.
// A fresh call replaces any transient toast already up, so rapid calls do not stack. Errors linger
// longer than confirmations - they have to be read. Styled by .console-shell-toast in overrides.css.
export function showToast(message: string, kind: "ok" | "error" = "ok", ms = kind === "error" ? 6000 : 2600): void {
  document.querySelector(".console-shell-toast--transient")?.remove();
  const toast = document.createElement("div");
  toast.className = "console-shell-toast console-shell-toast--transient";
  toast.dataset.kind = kind;
  toast.setAttribute("role", kind === "error" ? "alert" : "status");
  const msg = document.createElement("span");
  msg.textContent = message;
  toast.appendChild(msg);
  document.body.appendChild(toast);
  setTimeout(() => toast.remove(), ms);
}
