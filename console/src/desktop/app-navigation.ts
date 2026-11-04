// A mode is a view an app switches to inside itself: the Dashboard's jobs, the Graph's figures.
export type AppNavigation = {
  pageId: string;
  mode?: string;
};

const eventName = "console:open-app";
const modeEventName = "console:app-mode";

declare global {
  interface Window {
    __magusConsoleModeIntent?: Record<string, string>;
  }
}

export function openApp(detail: AppNavigation): void {
  window.dispatchEvent(new CustomEvent<AppNavigation>(eventName, { detail }));
}

export function appNavigation(event: Event): AppNavigation | null {
  if (!(event instanceof CustomEvent)) return null;
  return readNavigation(event.detail);
}

// readNavigation accepts an event detail only when it has the AppNavigation shape.
function readNavigation(detail: unknown): AppNavigation | null {
  if (!detail || typeof detail !== "object" || !("pageId" in detail)) return null;
  const pageId = detail.pageId;
  if (typeof pageId !== "string") return null;
  if (!("mode" in detail) || detail.mode === undefined) return { pageId };
  const mode = detail.mode;
  return typeof mode === "string" ? { pageId, mode } : null;
}

// requestMode is the shell's half. The intent waits on window because an app's bundle loads lazily
// and has not mounted yet on the first request; the event reaches one that already has.
export function requestMode(pageId: string, mode: string): void {
  window.__magusConsoleModeIntent = { ...window.__magusConsoleModeIntent, [pageId]: mode };
  window.dispatchEvent(new CustomEvent(modeEventName, { detail: { pageId, mode } }));
}

// takeModeIntent is the app's half on mount: the mode asked for before it loaded, consumed once.
export function takeModeIntent(pageId: string): string | null {
  const intents = window.__magusConsoleModeIntent;
  const mode = intents?.[pageId];
  if (intents) delete intents[pageId];
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
      if (!(event instanceof CustomEvent)) return;
      const detail = readNavigation(event.detail);
      if (detail?.pageId !== pageId || detail.mode === undefined) return;
      takeModeIntent(pageId);
      switchTo(detail.mode);
    },
    signal ? { signal } : {},
  );
}

export { eventName as appNavigationEvent };
