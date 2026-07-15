// refresh-toast.ts - a small bottom-left "reload" prompt shared by service-worker-register
// (a newer version of the assets is available) and console-settings (a browser-side pref
// changed and takes effect on reload). Idempotent: a second call while a toast is already up
// is a no-op, so one Refresh button covers overlapping reasons to reload. Styled by .sw-toast
// in overrides.css.
export function showRefreshToast(message: string): void {
  if (document.querySelector(".sw-toast")) return;
  const toast = document.createElement("div");
  toast.className = "sw-toast";
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
