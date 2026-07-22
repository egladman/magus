// toolbar.ts - the ONE shared responsive-toolbar overflow controller for the console's PF Toolbars.
//
// Both the log viewer and the graph explorer collapse their secondary controls behind a single PF
// toolbar toggle button on narrow viewports and expand them into a PF __expandable-content dropdown;
// on wide viewports the controls sit inline in the toolbar row. This is PatternFly's OWN
// ToolbarToggleGroup + expandable-content mechanism: the PF React component moves the collapsed
// children into the expandable panel and toggles pf-m-expanded, and we do exactly that here in the
// CSS-only console with one small helper - so the behavior is defined ONCE and shared, not duplicated
// per surface with divergent ad-hoc media queries (that was the whole point of adopting PatternFly).
//
// Markup contract (per collapsible toolbar - see logs/graph scaffold.html):
//
//   <div class="pf-v6-c-toolbar__content">                          (PF sets position:relative)
//     <div class="pf-v6-c-toolbar__content-section">
//       ...always-visible primary controls (the filter / query field)...
//       <div class="pf-v6-c-toolbar__group pf-m-toggle-group pf-m-show-on-<bp>"
//            data-overflow-group="<token>">
//         <div class="pf-v6-c-toolbar__toggle">
//           <button ... data-overflow-toggle aria-expanded="false">kebab</button>
//         </div>
//         ...the collapsible secondary groups/items (PF __group / __item)...
//       </div>
//     </div>
//     <div class="pf-v6-c-toolbar__expandable-content" data-overflow-panel="<token>"></div>
//   </div>
//
// Everything in the toggle-group except the .pf-v6-c-toolbar__toggle is collapsible: this helper moves
// that set between the toggle-group (inline, wide) and the expandable panel (dropdown, narrow). PF's
// pf-m-show-on-<bp> utility governs the toggle-button + inline-item visibility at the breakpoint, so
// there is NO bespoke media query: the single breakpoint lives in that class, and this helper reads it
// back so the DOM move flips at the SAME width PF flips the visibility. data-overflow-group and
// data-overflow-panel share a <token> so a page can host more than one overflow toolbar unambiguously.

// PF's core breakpoints (patternfly-base), keyed by the pf-m-show-on-<bp> suffix. rem so the media
// query tracks the root font size exactly the way PF's own @media rules do.
const BREAKPOINTS: Record<string, string> = {
  sm: "36rem",
  md: "48rem",
  lg: "62rem",
  xl: "75rem",
  "2xl": "90.625rem",
};

function breakpointOf(group: HTMLElement): string {
  for (const cls of Array.from(group.classList)) {
    const m = /^pf-m-show-on-(sm|md|lg|xl|2xl)$/.exec(cls);
    if (m) return BREAKPOINTS[m[1]];
  }
  return BREAKPOINTS.lg; // sane default if the class is ever dropped
}

function setupOne(group: HTMLElement): void {
  if (group.dataset.overflowWired) return; // idempotent: activate() may run more than once
  const token = group.dataset.overflowGroup || "";
  const toggleWrap = group.querySelector<HTMLElement>(".pf-v6-c-toolbar__toggle");
  const toggle = group.querySelector<HTMLButtonElement>("[data-overflow-toggle]");
  const panel = document.querySelector<HTMLElement>(`[data-overflow-panel="${token}"]`);
  if (!toggleWrap || !toggle || !panel) return;
  group.dataset.overflowWired = "1";

  // The collapsible controls are every element child of the toggle-group except the toggle button
  // wrapper, captured in order so they move back inline in their original layout on wide viewports.
  const items = Array.from(group.children).filter((c) => c !== toggleWrap) as HTMLElement[];

  let expanded = false;
  const setExpanded = (v: boolean): void => {
    expanded = v;
    // PF's __expandable-content is display:none until pf-m-expanded; that single class is the dropdown.
    panel.classList.toggle("pf-m-expanded", v);
    toggle.setAttribute("aria-expanded", v ? "true" : "false");
  };

  const mq = window.matchMedia(`(min-width: ${breakpointOf(group)})`);
  let wasWide: boolean | null = null;
  const apply = (): void => {
    const wide = mq.matches;
    if (wide === wasWide) return; // no breakpoint change: nothing to move (keeps resize cheap)
    wasWide = wide;
    // Wide: the controls sit inline in the toolbar row (PF's pf-m-show-on-<bp> reveals them and hides
    // the toggle). Narrow: they live in the expandable panel and PF shows the toggle button. appendChild
    // moves in the captured order, so the inline layout is preserved when they return.
    const dest = wide ? group : panel;
    for (const it of items) if (it.parentElement !== dest) dest.appendChild(it);
    setExpanded(false);
  };

  // Listen to both: matchMedia change is the precise breakpoint signal; resize is the belt-and-suspenders
  // fallback for environments that resize the viewport without dispatching a matchMedia change. The
  // wasWide guard makes apply a no-op except on an actual breakpoint crossing, so resize stays cheap.
  mq.addEventListener("change", apply);
  window.addEventListener("resize", apply);
  apply();

  toggle.addEventListener("click", (ev) => {
    ev.stopPropagation();
    setExpanded(!expanded);
  });
  // Dismiss the dropdown on an outside click or Escape, the way a PF menu closes.
  document.addEventListener("click", (ev) => {
    if (!expanded) return;
    const t = ev.target as Node;
    if (!panel.contains(t) && !toggle.contains(t)) setExpanded(false);
  });
  document.addEventListener("keydown", (ev) => {
    if (expanded && (ev as KeyboardEvent).key === "Escape") {
      setExpanded(false);
      toggle.focus();
    }
  });
}

// wireToolbarOverflow finds every overflow toolbar under `root` and wires its collapse behavior. Safe
// to call more than once (each group wires itself only once), so a surface can call it from activate().
export function wireToolbarOverflow(root: ParentNode = document): void {
  root.querySelectorAll<HTMLElement>("[data-overflow-group]").forEach(setupOne);
}
