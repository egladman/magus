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

// showToast pops a transient, auto-dismissing status toast (no action button) in the same bottom-left
// slot as the reload prompt. Used to confirm a Settings save/apply. A fresh call replaces any transient
// toast already up, so rapid saves do not stack. Styled by .console-shell-toast (+ --transient modifier)
// in overrides.css.
export function showToast(message: string, ms = 2600): void {
  document.querySelector(".console-shell-toast--transient")?.remove();
  const toast = document.createElement("div");
  toast.className = "console-shell-toast console-shell-toast--transient";
  toast.setAttribute("role", "status");
  const msg = document.createElement("span");
  msg.textContent = message;
  toast.appendChild(msg);
  document.body.appendChild(toast);
  setTimeout(() => toast.remove(), ms);
}
