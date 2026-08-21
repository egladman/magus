// detach.ts - move a live panel into its own OS window, and put it back when that window closes.
//
// The element is MOVED, not copied. Its listeners, closures and subscriptions come with it, so a
// detached panel keeps working with no cross-window channel and nothing to serialize - which is the
// difference between a primitive any surface can adopt and a per-panel porting exercise.
//
// Document Picture-in-Picture where it exists (Chromium): a real window the reader can drag to another
// display, with no browser chrome. window.open elsewhere. Both are same-origin, so both can adopt the
// node; only the chrome differs.

export interface DetachHandle {
  close(): void;
  isOpen(): boolean;
}

export interface DetachOptions {
  title: string;
  width?: number;
  height?: number;
  // Called after the panel is back in its original place, whether the reader closed the detached
  // window or the host page went away.
  onReturn?: () => void;
}

export function canDetach(): boolean {
  return "documentPictureInPicture" in window || typeof window.open === "function";
}

// The detached window gets a COPY of every stylesheet, because it has its own document and inherits
// nothing. Cross-origin sheets cannot be read (cssRules throws), so those are re-linked by href and
// left to load normally.
function copyStyles(target: Document): void {
  for (const sheet of Array.from(document.styleSheets)) {
    try {
      const rules = Array.from(sheet.cssRules)
        .map((r) => r.cssText)
        .join("\n");
      const style = target.createElement("style");
      style.textContent = rules;
      target.head.append(style);
    } catch {
      if (!sheet.href) continue;
      const link = target.createElement("link");
      link.rel = "stylesheet";
      link.href = sheet.href;
      target.head.append(link);
    }
  }
}

// Theme lives on the host's <html> as a class plus data-* attributes, and a reader can change it while
// a panel is detached. Mirroring once would leave the detached window in the old theme, so it is
// mirrored and then kept in sync until the window closes.
function mirrorRoot(target: Document): () => void {
  const src = document.documentElement;
  const apply = (): void => {
    target.documentElement.className = src.className;
    for (const { name, value } of Array.from(src.attributes)) {
      if (name.startsWith("data-")) target.documentElement.setAttribute(name, value);
    }
  };
  apply();
  const obs = new MutationObserver(apply);
  obs.observe(src, { attributes: true });
  return () => obs.disconnect();
}

// requestWindow must be reached in the SAME task as the click: both APIs spend the user activation a
// gesture grants, and an await before either one loses it. openWindow is async but its body runs
// synchronously up to this call, so a caller that invokes it directly from a handler keeps the grant.
async function openWindow(opts: DetachOptions): Promise<Window | null> {
  const w = opts.width ?? 420;
  const h = opts.height ?? 640;
  const pip = (
    window as unknown as {
      documentPictureInPicture?: { requestWindow(o: object): Promise<Window> };
    }
  ).documentPictureInPicture;
  if (pip) {
    try {
      return await pip.requestWindow({ width: w, height: h });
    } catch {
      // Denied (no activation, or disabled by policy): try the ordinary popup rather than give up.
    }
  }
  return window.open("", "", `popup=yes,width=${w},height=${h}`);
}

export async function detachPanel(
  el: HTMLElement,
  opts: DetachOptions,
): Promise<DetachHandle | null> {
  const win = await openWindow(opts);
  if (!win) return null;

  // Where to put the panel back. Captured BEFORE the move, and as a sibling rather than an index, so
  // it returns to the same slot even if the host rearranged around the gap.
  const parent = el.parentElement;
  const nextSibling = el.nextSibling;
  // The gap left behind. Without it the host column silently collapses and the panel looks closed
  // rather than moved, which is the same confusion as a lost window.
  const placeholder = document.createElement("div");
  placeholder.dataset.detachedPlaceholder = "";
  placeholder.textContent = opts.title + " is in its own window.";
  parent?.insertBefore(placeholder, el);

  win.document.title = opts.title;
  copyStyles(win.document);
  const stopMirror = mirrorRoot(win.document);
  win.document.body.dataset.detachedHost = "";
  win.document.body.append(el);

  let open = true;
  const restore = (): void => {
    if (!open) return;
    open = false;
    stopMirror();
    // Back to the exact slot, then drop the placeholder. In this order, so the column never briefly
    // has neither and reflows twice.
    parent?.insertBefore(el, placeholder);
    placeholder.remove();
    opts.onReturn?.();
  };

  win.addEventListener("pagehide", restore);
  // The host going away takes the detached window with it, and the panel must not be stranded in a
  // document that is being torn down.
  window.addEventListener("pagehide", () => win.close(), { once: true });

  return {
    close: () => {
      win.close();
      restore(); // close() does not always fire pagehide in time; restore is idempotent
    },
    isOpen: () => open,
  };
}
