// help-popover.ts - the shared "?" help affordance. Every console app that carries a query/filter
// prompt (the log viewer, the graph explorer, the runs filter) had its OWN bare "?" button whose entire
// explanation lived in a native title= tooltip. Hover tooltips never appear on touch and the buttons had
// no click handler, so on mobile tapping "?" did nothing. This upgrades any such trigger into a real
// click-to-toggle PatternFly Popover: tap/click opens a small panel holding the same text, tap-outside /
// Escape / re-tap closes it. One implementation so the affordance is identical everywhere.
//
// Cost model: attaching a popover costs one click listener on its own trigger and nothing else. The
// popover element is built when it opens and removed when it closes, and the document and window
// listeners that keep an open popover dismissable and placed exist only while one is open, and are
// shared by every trigger. Nothing here grows with the number of triggers a page has attached, nor
// with the number of times a card is rebuilt: the previous version appended a hidden element and four
// global listeners per call, and the Dashboard, which rebuilds its cards on every frame, accumulated
// more than a thousand of each.
//
// The text falls back to the trigger's existing title= (which we then strip, so a desktop hover shows
// the popover-on-click rather than doubling up with the native tooltip).

const NS = "http://www.w3.org/2000/svg";

let seq = 0;

export interface HelpPopoverOptions {
  // Body copy. Defaults to the trigger's title= attribute (the pre-existing tooltip text).
  text?: string;
  // Accessible name for the popover dialog. Defaults to the trigger's aria-label, else "Help".
  label?: string;
}

interface Open {
  trigger: HTMLElement;
  pop: HTMLElement;
  close: HTMLElement;
  // Closes it. restoreFocus returns focus to the trigger.
  dismiss(restoreFocus: boolean): void;
}

// The one open popover. A second opening closes the first, so a single set of listeners serves all.
let current: Open | null = null;
let shared: AbortController | null = null;

function svg(viewBox: string, size: string, build: (el: SVGElement) => void): SVGElement {
  const el = document.createElementNS(NS, "svg");
  el.setAttribute("class", "pf-v6-svg");
  el.setAttribute("viewBox", viewBox);
  el.setAttribute("width", size);
  el.setAttribute("height", size);
  el.setAttribute("aria-hidden", "true");
  build(el);
  return el;
}

function closeButton(): HTMLButtonElement {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "pf-v6-c-button pf-m-plain";
  button.setAttribute("aria-label", "Close");
  const icon = document.createElement("span");
  icon.className = "pf-v6-c-button__icon";
  icon.append(
    svg("0 0 20 20", "1em", (el) => {
      el.setAttribute("fill", "currentColor");
      const path = document.createElementNS(NS, "path");
      path.setAttribute(
        "d",
        "M17.8 16.2 11.59 10l6.21-6.21c.42-.46.39-1.17-.07-1.59-.43-.4-1.09-.4-1.52 0l-6.2 6.2-6.22-6.19c-.44-.44-1.15-.44-1.59 0-.44.44-.44 1.15 0 1.59l6.2 6.21-6.2 6.2c-.42.46-.39 1.17.07 1.59.43.4 1.09.4 1.52 0L10 11.59l6.2 6.2c.44.44 1.15.44 1.59 0 .44-.45.44-1.16 0-1.6Z",
      );
      el.append(path);
    }),
  );
  button.append(icon);
  return button;
}

// createHelpButton builds the "?" trigger: a PF plain button whose hit area is at least 24px square.
// Pass it to attachHelpPopover, which adds the popup wiring.
export function createHelpButton(label: string): HTMLButtonElement {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "pf-v6-c-button pf-m-plain";
  button.setAttribute("aria-label", label);
  const icon = document.createElement("span");
  icon.className = "pf-v6-c-button__icon";
  icon.append(
    svg("0 0 24 24", "1em", (el) => {
      el.setAttribute("fill", "none");
      el.setAttribute("stroke", "currentColor");
      el.setAttribute("stroke-width", "2");
      el.setAttribute("stroke-linecap", "round");
      el.setAttribute("stroke-linejoin", "round");
      for (const [tag, attrs] of [
        ["circle", { cx: "12", cy: "12", r: "9" }],
        ["path", { d: "M9.5 9.5a2.5 2.5 0 1 1 3.5 2.3c-.7.4-1 .9-1 1.7" }],
        ["path", { d: "M12 17v.01" }],
      ] as const) {
        const part = document.createElementNS(NS, tag);
        for (const [k, v] of Object.entries(attrs)) part.setAttribute(k, v);
        el.append(part);
      }
    }),
  );
  button.append(icon);
  button.dataset.helpTrigger = "";
  return button;
}

// place puts the popover under the trigger, centred on it and clamped into the viewport so it never
// spills off a phone screen, or above it when there is no room below. position:fixed, so these are
// viewport coordinates. The arrow is pointed back at the trigger whatever the clamp did.
function place(trigger: HTMLElement, pop: HTMLElement): void {
  const r = trigger.getBoundingClientRect();
  const pw = pop.offsetWidth;
  const ph = pop.offsetHeight;
  const margin = 8;
  let left = r.left + r.width / 2 - pw / 2;
  left = Math.max(margin, Math.min(left, window.innerWidth - margin - pw));
  const above = r.bottom + ph + margin > window.innerHeight && r.top - ph - margin > 0;
  pop.classList.toggle("pf-m-bottom", !above);
  pop.classList.toggle("pf-m-top", above);
  pop.style.left = left + "px";
  pop.style.top = (above ? r.top - ph : r.bottom) + "px";
  const arrow = Math.max(margin, Math.min(r.left + r.width / 2 - left, pw - margin));
  pop.style.setProperty("--pf-v6-c-popover--m-bottom--InsetInlineStart", arrow + "px");
  pop.style.setProperty("--pf-v6-c-popover--m-top--InsetInlineStart", arrow + "px");
}

function listen(): void {
  if (shared) return;
  const ctl = new AbortController();
  shared = ctl;
  const { signal } = ctl;
  // The click that opened it is the trigger's own and is ignored here, so it cannot close what it
  // just opened.
  document.addEventListener(
    "click",
    (e) => {
      if (!current) return;
      const target = e.target instanceof Node ? e.target : null;
      if (current.pop.contains(target) || current.trigger.contains(target)) return;
      current.dismiss(false);
    },
    { signal },
  );
  document.addEventListener(
    "keydown",
    (e) => {
      if (!current) return;
      if (e.key === "Escape") {
        current.dismiss(true);
      } else if (e.key === "Tab" && current.pop.contains(document.activeElement)) {
        // The close button is the popover's only control; aria-modal says focus stays inside it.
        e.preventDefault();
        current.close.focus();
      }
    },
    { signal },
  );
  const reflow = (): void => {
    if (current) place(current.trigger, current.pop);
  };
  window.addEventListener("resize", reflow, { signal });
  // Capture-phase scroll: a scroll inside any ancestor (the sidebar, the toolbar overflow) should keep
  // the popover glued to its trigger, not just a scroll of the document.
  window.addEventListener("scroll", reflow, { capture: true, signal });
}

function unlisten(): void {
  shared?.abort();
  shared = null;
}

// attachHelpPopover upgrades an existing trigger (typically the "?" button) into a click-to-toggle
// popover. Returns a disposer that closes the popover if it is open and removes the trigger's
// listener; call it when the trigger leaves the page, since a detached trigger is otherwise held by
// its own listener only until it is collected.
export function attachHelpPopover(trigger: HTMLElement, opts: HelpPopoverOptions = {}): () => void {
  const title = trigger.getAttribute("title");
  const text = (opts.text ?? title ?? "").trim();
  if (!text) return () => {};
  const label = opts.label ?? trigger.getAttribute("aria-label") ?? "Help";
  // Strip title= so hover doesn't fire the native tooltip on top of our popover; keep aria-label as the
  // button's accessible name.
  trigger.removeAttribute("title");
  trigger.dataset.helpTrigger = "";
  trigger.setAttribute("aria-haspopup", "dialog");
  trigger.setAttribute("aria-expanded", "false");

  let mine: Open | null = null;

  const open = (): void => {
    current?.dismiss(false);
    // Several bundles each carry their own copy of this module and its counter, so the id is checked
    // against the document rather than trusted to be fresh.
    let id = "console-help-pop-" + ++seq;
    while (document.getElementById(id)) id = "console-help-pop-" + ++seq;
    const pop = document.createElement("div");
    pop.className = "pf-v6-c-popover pf-m-bottom console-help-popover";
    pop.id = id;
    pop.setAttribute("role", "dialog");
    pop.setAttribute("aria-modal", "true");
    pop.setAttribute("aria-label", label);
    pop.setAttribute("aria-describedby", id + "-body");
    const arrow = document.createElement("div");
    arrow.className = "pf-v6-c-popover__arrow";
    const content = document.createElement("div");
    content.className = "pf-v6-c-popover__content";
    const closeWrap = document.createElement("div");
    closeWrap.className = "pf-v6-c-popover__close";
    const close = closeButton();
    closeWrap.append(close);
    const body = document.createElement("div");
    body.className = "pf-v6-c-popover__body";
    body.id = id + "-body";
    body.textContent = text;
    content.append(closeWrap, body);
    pop.append(arrow, content);
    document.body.append(pop);

    const entry: Open = {
      trigger,
      pop,
      close,
      dismiss(restoreFocus) {
        if (current !== entry) return;
        current = null;
        mine = null;
        pop.remove();
        trigger.setAttribute("aria-expanded", "false");
        trigger.removeAttribute("aria-controls");
        unlisten();
        if (restoreFocus) trigger.focus();
      },
    };
    close.addEventListener("click", () => entry.dismiss(true));
    current = entry;
    mine = entry;
    trigger.setAttribute("aria-expanded", "true");
    trigger.setAttribute("aria-controls", id);
    listen();
    place(trigger, pop);
    close.focus();
  };

  const onTrigger = (e: MouseEvent): void => {
    e.preventDefault();
    if (mine) mine.dismiss(false);
    else open();
  };
  trigger.addEventListener("click", onTrigger);

  return () => {
    mine?.dismiss(false);
    trigger.removeEventListener("click", onTrigger);
    trigger.removeAttribute("aria-haspopup");
    trigger.removeAttribute("aria-expanded");
    delete trigger.dataset.helpTrigger;
    if (title !== null) trigger.setAttribute("title", title);
  };
}
