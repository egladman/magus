// install-platform.ts - show the install page's Windows route to Windows readers.
//
// The page ships the Unix route visible and the Windows one `hidden`, so a reader with
// scripting off still lands on the route most of them want, and both blocks are in the
// markup either way. This only swaps when the platform is clearly Windows, which is the
// difference between a helpful default and a page that has decided for you. A wrong guess
// costs one link click - the Windows guide is in the route row directly underneath - which
// is why nothing here is load bearing.
//
// navigator.userAgentData.platform where it exists, userAgent otherwise: the old
// navigator.platform is deprecated and lies about Apple Silicon.
export function initInstallPlatform(): void {
  const root = document.getElementById("install-platform");
  if (!root) return;

  const ua = navigator.userAgent;
  const uaPlatform =
    (navigator as { userAgentData?: { platform?: string } }).userAgentData?.platform ?? "";
  const hay = (uaPlatform + " " + ua).toLowerCase();

  // Only Windows moves the needle. Everything else - macOS, Linux, the BSDs, and anything
  // unrecognized - is served by the install script the Unix route already shows.
  const isWindows = hay.includes("windows") || hay.includes("win32") || hay.includes("win64");
  if (!isWindows) return;

  const unix = root.querySelector<HTMLElement>('[data-install-route="unix"]');
  const windows = root.querySelector<HTMLElement>('[data-install-route="windows"]');
  if (!unix || !windows) return;

  unix.hidden = true;
  windows.hidden = false;
}
