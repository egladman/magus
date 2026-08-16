// offline-status.ts - fills the settings panel's Offline row.
//
// The landing page claims the site works offline. This is where that claim becomes
// checkable rather than asserted: it reports whether a service worker has actually
// taken control of this page, and whether a newer one is waiting to replace it.
//
// The row starts hidden and is only revealed once there is something true to say. A
// browser with no service-worker support, or a page served where registration failed,
// leaves it hidden rather than showing a reassuring "Ready" that nothing backs up -
// an offline indicator that lies is worse than none.

const ROW = "settings-offline";
const VALUE = "settings-offline-value";

function show(text: string): void {
  const row = document.getElementById(ROW);
  const value = document.getElementById(VALUE);
  if (!row || !value) return;
  value.textContent = text;
  row.hidden = false;
}

export function initOfflineStatus(): void {
  if (!document.getElementById(ROW)) return;
  if (!("serviceWorker" in navigator)) return;

  const sw = navigator.serviceWorker;

  // `controller` is the honest signal: a registration can exist while this particular
  // page load is still uncontrolled (the very first visit), and that page is NOT yet
  // available offline. Reporting on the registration alone would claim otherwise.
  const report = (): void => {
    void sw.getRegistration().then((reg) => {
      if (!reg) return; // nothing registered: stay silent rather than guess
      if (reg.waiting) {
        show("Update ready - reload to apply");
        return;
      }
      show(sw.controller ? "Cached for offline" : "Caching on first visit");
    });
  };

  report();
  // A worker can take control or finish installing after this runs, so re-report
  // instead of leaving whatever was true at load.
  sw.addEventListener("controllerchange", report);
  void sw.ready.then(report);
}
