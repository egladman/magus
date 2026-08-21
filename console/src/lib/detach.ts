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

// Document PiP only. The old version OR'd in `typeof window.open === "function"`, which is always
// true, so the control it gates was never once hidden - a capability check that answered nothing.
// window.open stays as a runtime fallback; it is just not something to promise in advance, because
// a popup blocker decides that per click and not per browser.
export function canDetach(): boolean {
  return "documentPictureInPicture" in window;
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
    const dst = target.documentElement;
    dst.className = src.className;
    const want = new Map(
      Array.from(src.attributes)
        .filter((a) => a.name.startsWith("data-"))
        .map((a) => [a.name, a.value]),
    );
    // Removals matter as much as sets: settings signal their defaults by CLEARING the attribute, so
    // a set-only mirror pinned the detached window to whatever it was first opened with. Turning
    // Motion back to auto left it reduced until the panel came home.
    for (const { name } of Array.from(dst.attributes)) {
      if (name.startsWith("data-") && !want.has(name)) dst.removeAttribute(name);
    }
    for (const [name, value] of want) dst.setAttribute(name, value);
  };
  apply();
  const obs = new MutationObserver(apply);
  obs.observe(src, { attributes: true });
  return () => obs.disconnect();
}

// Both window APIs spend the user activation a gesture grants, and an await before either one loses
// it. So the two paths are chosen SYNCHRONOUSLY: window.open when there is no Document PiP, and
// requestWindow when there is. Falling back to window.open after requestWindow rejects cannot work -
// the activation is already spent - which made the fallback fail on exactly the browsers it existed
// for. openWindow is async, but its body runs synchronously up to whichever call it makes.
async function openWindow(opts: DetachOptions): Promise<Window | { reason: string }> {
  const w = opts.width ?? 420;
  const h = opts.height ?? 640;
  const pip = (
    window as { documentPictureInPicture?: { requestWindow(o: object): Promise<Window> } }
  ).documentPictureInPicture;
  if (!pip) {
    const popup = window.open("", "", `popup=yes,width=${w},height=${h}`);
    return popup ?? { reason: "the popup was blocked" };
  }
  try {
    return await pip.requestWindow({ width: w, height: h });
  } catch (e) {
    return { reason: e instanceof Error ? e.message : "the browser refused a second window" };
  }
}

// A discriminated result rather than null: the caller has to tell the reader WHY nothing happened,
// and "the popup was blocked" and "Document PiP requires user activation" are different sentences.
// With null the one call site had to invent a reason, which is how a correct refusal reads as a bug.
export type DetachResult = { ok: true; handle: DetachHandle } | { ok: false; reason: string };

export async function detachPanel(el: HTMLElement, opts: DetachOptions): Promise<DetachResult> {
  // Nothing to put back into: a panel with no parent could be moved out but never returned.
  const parent = el.parentElement;
  if (!parent) return { ok: false, reason: "the panel is not attached to anything" };

  const opened = await openWindow(opts);
  if (!(opened instanceof Window)) return { ok: false, reason: opened.reason };
  const win = opened;

  // Marks the gap AND the way back: without it the host column silently collapses and the panel
  // looks closed rather than moved, and restore would have only an index to aim at - which the host
  // invalidates the moment it rearranges around the gap.
  const placeholder = document.createElement("div");
  placeholder.dataset.detachedPlaceholder = "";
  placeholder.textContent = opts.title + " is in its own window.";
  parent.insertBefore(placeholder, el);

  win.document.title = opts.title;
  copyStyles(win.document);
  const stopMirror = mirrorRoot(win.document);
  win.document.body.dataset.detachedHost = "";
  win.document.body.append(el);

  let open = true;
  const closeWin = (): void => win.close();
  const restore = (): void => {
    if (!open) return;
    open = false;
    stopMirror();
    window.removeEventListener("pagehide", closeWin);
    // The slot may be gone: the host can re-render, or replaceChildren its column, while the panel
    // is away. insertBefore against a detached placeholder throws NotFoundError - and it would throw
    // AFTER open was cleared, from inside a pagehide handler, losing the panel silently.
    if (placeholder.parentNode === parent) parent.insertBefore(el, placeholder);
    else parent.append(el);
    placeholder.remove();
    opts.onReturn?.();
  };

  win.addEventListener("pagehide", restore);
  // The host going away takes the detached window with it, so the panel is never stranded in a
  // document being torn down. Removed on restore, or every detach/re-attach cycle leaves one behind
  // holding a closed Window.
  window.addEventListener("pagehide", closeWin);

  return {
    ok: true,
    handle: {
      close: () => {
        win.close();
        restore(); // close() does not always fire pagehide in time; restore is idempotent
      },
      isOpen: () => open,
    },
  };
}
