import { persisted } from "../lib/persist";

// ref-drawer.ts - the console's right-side slide-out Reference panel. It shows the ACTIVE surface's
// reference sections: the help blocks each surface scaffold carries marked [data-legacy-ref] (the
// graph explorer's query/search-syntax help, the log viewer's filter help). The old docs-site drawer
// relocated those blocks once at load, but the console mounts surfaces dynamically, so this CLONES
// the active surface's blocks each time it opens (and refreshes when the active tab changes while
// open). It can be pinned (docked beside the content) or float as an overlay with a dimming backdrop;
// the pinned state persists. No-ops where the drawer markup is absent.
//
// Cloned example buttons are inert (cloneNode drops listeners), so a click inside the drawer on an
// example that carries a distinguishing data-* (the graph's data-q / data-view / data-lens) is
// forwarded to the matching live control in the active surface pane, which IS wired.
export function initRefDrawer(): void {
  const drawer = document.getElementById("console-refdrawer");
  const backdrop = document.getElementById("console-refbackdrop");
  const bodyEl = document.getElementById("console-refdrawer-body");
  const trigger = document.getElementById("console-refbtn");
  if (!drawer || !backdrop || !bodyEl || !trigger) return;

  const pinBtn = document.getElementById("console-refpin");
  const closeBtn = document.getElementById("console-refclose");

  // The active surface is the one visible pane in the outlet (main.ts hides the others).
  const activePane = (): HTMLElement | null =>
    document.querySelector<HTMLElement>("#console-outlet div[data-tab-id]:not([hidden])");

  const collect = (pane: HTMLElement | null): HTMLElement[] =>
    pane ? [...pane.querySelectorAll<HTMLElement>("[data-legacy-ref]")].filter((b) => b.id !== "ask-panel") : [];

  // Paint the given reference blocks into the panel body. Cloning (not moving) keeps the source intact
  // so a surface can unmount/remount freely. Nested <details> open so the reference reads as content;
  // ids are stripped from clones to avoid duplicates.
  const paint = (blocks: HTMLElement[]): void => {
    bodyEl.replaceChildren();
    if (blocks.length === 0) {
      const empty = document.createElement("p");
      empty.className = "console-shell-refdrawer__empty";
      empty.textContent = "No reference for this view.";
      bodyEl.append(empty);
      return;
    }
    for (const b of blocks) {
      const clone = b.cloneNode(true) as HTMLElement;
      clone.removeAttribute("data-legacy-ref"); // clones are shown; the [data-legacy-ref]{display:none} rule hides only sources
      clone.removeAttribute("id");
      clone.querySelectorAll("[id]").forEach((el) => el.removeAttribute("id"));
      if (clone instanceof HTMLDetailsElement) clone.open = true;
      clone.querySelectorAll("details").forEach((d) => { (d as HTMLDetailsElement).open = true; });
      bodyEl.append(clone);
    }
  };

  // Show the active surface's reference sections. #ask-panel is skipped (the graph explorer surfaces
  // those "Ask a question" views in its sidebar already). A freshly opened surface mounts its scaffold
  // asynchronously (its bundle is a dynamic import), so if the pane has no blocks yet, watch it briefly
  // and repaint once its content lands - otherwise a just-opened tab would read "No reference".
  let watcher: MutationObserver | null = null;
  const refresh = (): void => {
    watcher?.disconnect();
    watcher = null;
    const pane = activePane();
    const blocks = collect(pane);
    paint(blocks);
    if (pane && blocks.length === 0) {
      const obs = new MutationObserver(() => {
        const found = collect(pane);
        if (found.length > 0) { obs.disconnect(); watcher = null; paint(found); }
      });
      watcher = obs;
      obs.observe(pane, { childList: true, subtree: true });
      // Stop watching a genuinely reference-less surface after it has had time to mount.
      setTimeout(() => { if (watcher === obs) { obs.disconnect(); watcher = null; } }, 3000);
    }
  };

  // Forward a click on a cloned example to the live control in the active surface. Match by the first
  // distinguishing attribute present; the source control (same attr+value) carries the real listener.
  const FORWARD_ATTRS = ["data-q", "data-view", "data-lens"] as const;
  bodyEl.addEventListener("click", (e) => {
    const t = e.target;
    if (!(t instanceof Element)) return;
    const src = t.closest<HTMLElement>("[data-q],[data-view],[data-lens]");
    if (!src) return;
    const pane = activePane();
    if (!pane) return;
    for (const attr of FORWARD_ATTRS) {
      const val = src.getAttribute(attr);
      if (val === null) continue;
      const live = pane.querySelector<HTMLElement>(`[${attr}="${CSS.escape(val)}"]`);
      if (live) { e.preventDefault(); live.click(); return; }
    }
  });

  // Pinned persists: a pinned panel is docked open on load and stays docked as you switch tabs; an
  // unpinned panel is a temporary overlay dimmed by the backdrop.
  const pinnedCell = persisted("ref-pinned", false);
  let pinned = pinnedCell.get();
  let isOpen = pinned;

  const render = (): void => {
    drawer.toggleAttribute("data-open", isOpen);
    drawer.toggleAttribute("data-pinned", isOpen && pinned);
    document.body.toggleAttribute("data-ref-pinned", isOpen && pinned);
    backdrop.hidden = !(isOpen && !pinned);
    drawer.setAttribute("aria-hidden", isOpen ? "false" : "true");
    trigger.setAttribute("aria-expanded", isOpen ? "true" : "false");
    pinBtn?.setAttribute("aria-pressed", pinned ? "true" : "false");
  };

  const setOpen = (open: boolean): void => {
    isOpen = open;
    if (open) refresh();
    // Closing a pinned panel also unpins it, so it does not spring back on the next open.
    if (!open && pinned) { pinned = false; pinnedCell.set(false); }
    render();
  };

  const togglePin = (): void => {
    pinned = !pinned;
    pinnedCell.set(pinned);
    if (pinned) isOpen = true;
    render();
  };

  trigger.addEventListener("click", () => setOpen(!isOpen));
  closeBtn?.addEventListener("click", () => setOpen(false));
  pinBtn?.addEventListener("click", togglePin);
  backdrop.addEventListener("click", () => setOpen(false));
  document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key === "Escape" && isOpen && !pinned) setOpen(false);
  });

  // main.ts dispatches this when the active tab changes; a docked/open panel re-reads the new surface.
  document.addEventListener("console:activetab", () => { if (isOpen) refresh(); });

  // Apply the persisted (pinned) state without animating the slide on load.
  document.documentElement.classList.add("ref-instant");
  render();
  if (isOpen) refresh();
  requestAnimationFrame(() => document.documentElement.classList.remove("ref-instant"));
}
