// help-popover.ts - the shared "?" help affordance. Every console surface that carries a query/filter
// prompt (the log viewer, the graph explorer) had its OWN bare "?" button whose entire explanation lived
// in a native title= tooltip. Hover tooltips never appear on touch and the buttons had no click handler,
// so on mobile tapping "?" did nothing. This upgrades any such trigger into a real click-to-toggle
// popover: tap/click opens a small panel holding the same text, tap-outside / Escape / re-tap closes it.
// One implementation so the affordance is identical everywhere (the graph and log "?" used to diverge).
//
// Mirrors the open/close/outside-click/Escape idiom of app-menu.ts and the settings gear popover, so the
// three title-bar-style popovers behave the same. The text falls back to the trigger's existing title=
// (which we then strip, so a desktop hover shows the popover-on-click rather than doubling up with the
// native tooltip).

let seq = 0;

export interface HelpPopoverOptions {
  // Body copy. Defaults to the trigger's title= attribute (the pre-existing tooltip text).
  text?: string;
  // Accessible name for the popover dialog. Defaults to the trigger's aria-label, else "Help".
  label?: string;
}

// attachHelpPopover upgrades an existing trigger (typically the "?" button) into a click-to-toggle
// popover. Returns a disposer that tears the listeners and the popover element down again.
export function attachHelpPopover(trigger: HTMLElement, opts: HelpPopoverOptions = {}): () => void {
  const text = (opts.text ?? trigger.getAttribute("title") ?? "").trim();
  if (!text) return () => {};
  // Strip title= so hover doesn't fire the native tooltip on top of our popover; keep aria-label as the
  // button's accessible name.
  trigger.removeAttribute("title");

  const id = "console-help-pop-" + ++seq;
  const pop = document.createElement("div");
  pop.className = "console-help-popover";
  pop.id = id;
  pop.setAttribute("role", "dialog");
  pop.setAttribute("aria-label", opts.label ?? trigger.getAttribute("aria-label") ?? "Help");
  pop.hidden = true;
  const body = document.createElement("p");
  body.className = "console-help-popover__body";
  body.textContent = text;
  pop.append(body);
  document.body.append(pop);

  trigger.setAttribute("aria-haspopup", "dialog");
  trigger.setAttribute("aria-controls", id);
  trigger.setAttribute("aria-expanded", "false");

  let open = false;

  // Position the popover just below the trigger, right-aligned to it, and clamped into the viewport so it
  // never spills off a narrow phone screen (position:fixed, so these are viewport coordinates).
  const place = (): void => {
    const r = trigger.getBoundingClientRect();
    // Measure after making it laid-out-but-invisible so width/height are real.
    const pw = pop.offsetWidth;
    const margin = 8;
    let left = r.right - pw;
    if (left < margin) left = margin;
    if (left + pw > window.innerWidth - margin) left = window.innerWidth - margin - pw;
    pop.style.left = left + "px";
    pop.style.top = r.bottom + 6 + "px";
  };

  const setOpen = (v: boolean, restoreFocus = false): void => {
    if (v === open) return;
    open = v;
    trigger.setAttribute("aria-expanded", v ? "true" : "false");
    if (v) {
      pop.hidden = false;
      place();
    } else {
      pop.hidden = true;
      if (restoreFocus) trigger.focus();
    }
  };

  // stopPropagation so this same click doesn't immediately reach the document outside-click handler and
  // toggle it back closed (the flash-and-reopen race the cheatsheet toggle hit).
  const onTrigger = (e: MouseEvent): void => {
    e.preventDefault();
    e.stopPropagation();
    setOpen(!open);
  };
  const onDocClick = (e: MouseEvent): void => {
    if (!open) return;
    const t = e.target as Node;
    if (pop.contains(t) || trigger.contains(t)) return;
    setOpen(false);
  };
  const onKey = (e: KeyboardEvent): void => {
    if (e.key === "Escape" && open) setOpen(false, true);
  };
  const onReflow = (): void => {
    if (open) place();
  };

  trigger.addEventListener("click", onTrigger);
  document.addEventListener("click", onDocClick);
  document.addEventListener("keydown", onKey);
  window.addEventListener("resize", onReflow);
  // Capture-phase scroll: a scroll inside any ancestor (the sidebar, the toolbar overflow) should keep
  // the popover glued to its trigger, not just a scroll of the document.
  window.addEventListener("scroll", onReflow, true);

  return () => {
    setOpen(false);
    trigger.removeEventListener("click", onTrigger);
    document.removeEventListener("click", onDocClick);
    document.removeEventListener("keydown", onKey);
    window.removeEventListener("resize", onReflow);
    window.removeEventListener("scroll", onReflow, true);
    pop.remove();
  };
}
