// A mode is a view an app switches to inside itself: the Dashboard's jobs, the Graph's figures.
export type SurfaceNavigation = {
  pageId: string;
  mode?: string;
};

const eventName = "console:open-surface";
const modeEventName = "console:surface-mode";

type ModeWindow = Window & { __magusConsoleModeIntent?: Record<string, string> };

export function openSurface(detail: SurfaceNavigation): void {
  window.dispatchEvent(new CustomEvent<SurfaceNavigation>(eventName, { detail }));
}

export function surfaceNavigation(event: Event): SurfaceNavigation | null {
  if (!(event instanceof CustomEvent)) return null;
  const detail = event.detail;
  if (!detail || typeof detail !== "object") return null;
  const { pageId, mode } = detail as Partial<SurfaceNavigation>;
  if (typeof pageId !== "string") return null;
  if (mode !== undefined && typeof mode !== "string") return null;
  return { pageId, mode };
}

// requestMode is the shell's half. The intent waits on window because an app's bundle loads lazily
// and has not mounted yet on the first request; the event reaches one that already has.
export function requestMode(pageId: string, mode: string): void {
  const win = window as ModeWindow;
  win.__magusConsoleModeIntent = { ...win.__magusConsoleModeIntent, [pageId]: mode };
  window.dispatchEvent(new CustomEvent(modeEventName, { detail: { pageId, mode } }));
}

// takeModeIntent is the app's half on mount: the mode asked for before it loaded, consumed once.
export function takeModeIntent(pageId: string): string | null {
  const win = window as ModeWindow;
  const mode = win.__magusConsoleModeIntent?.[pageId];
  if (win.__magusConsoleModeIntent) delete win.__magusConsoleModeIntent[pageId];
  return mode ?? null;
}

// onModeRequest runs switchTo for every mode the shell asks pageId for until signal aborts. The
// intent is consumed too, so a later mount does not replay a switch this one already made.
export function onModeRequest(
  pageId: string,
  switchTo: (mode: string) => void,
  signal: AbortSignal | undefined,
): void {
  window.addEventListener(
    modeEventName,
    (event) => {
      const detail = (event as CustomEvent<{ pageId?: string; mode?: string }>).detail;
      if (detail?.pageId !== pageId || typeof detail.mode !== "string") return;
      takeModeIntent(pageId);
      switchTo(detail.mode);
    },
    { signal },
  );
}

export { eventName as surfaceNavigationEvent };
