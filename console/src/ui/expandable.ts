// expandable.ts - PatternFly's Expandable section, built once: a link-button toggle over a region,
// closed unless opened. PF turns the chevron when the section carries pf-m-expanded, so the state
// lives on the section, on the button's aria-expanded and on the region's hidden attribute together.

import { h } from "../desktop/view";

const NS = "http://www.w3.org/2000/svg";

// PF's 20-unit angle-down; the section's expanded state turns it to point up.
const CHEVRON =
  "M18.71 5.29a.996.996 0 0 0-1.41 0l-7.29 7.29-7.3-7.29a.987.987 0 0 0-1.41-.02.987.987 0 0 0-.02 1.41l.02.02 7.65 7.65c.29.29.68.44 1.06.44s.77-.15 1.06-.44l7.65-7.65a.996.996 0 0 0 0-1.41Z";

let seq = 0;

export interface ExpandableOptions {
  open?: boolean;
  // Extra classes for the toggle button, beside PF's, for a caller that has to find or style it.
  toggleClass?: string;
}

export interface ExpandableSection {
  readonly el: HTMLElement;
  readonly toggle: HTMLButtonElement;
  readonly body: HTMLElement;
  set(open: boolean): void;
}

export function expandableSection(text: string, opts: ExpandableOptions = {}): ExpandableSection {
  const id = "console-expand-" + ++seq;
  const el = h("div", "pf-v6-c-expandable-section");
  const toggleBox = h("div", "pf-v6-c-expandable-section__toggle");
  const toggle = h(
    "button",
    "pf-v6-c-button pf-m-link" + (opts.toggleClass ? " " + opts.toggleClass : ""),
  );
  toggle.type = "button";
  toggle.id = id + "-toggle";
  toggle.setAttribute("aria-controls", id);
  const slot = h("span", "pf-v6-c-button__icon pf-m-start");
  const icon = h("span", "pf-v6-c-expandable-section__toggle-icon");
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("class", "pf-v6-svg");
  svg.setAttribute("viewBox", "0 0 20 20");
  svg.setAttribute("fill", "currentColor");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("width", "1em");
  svg.setAttribute("height", "1em");
  const path = document.createElementNS(NS, "path");
  path.setAttribute("d", CHEVRON);
  svg.append(path);
  icon.append(svg);
  slot.append(icon);
  toggle.append(slot, h("span", "pf-v6-c-button__text", text));
  toggleBox.append(toggle);
  const body = h("div", "pf-v6-c-expandable-section__content");
  body.id = id;
  body.setAttribute("role", "region");
  body.setAttribute("aria-labelledby", toggle.id);
  const set = (open: boolean): void => {
    toggle.setAttribute("aria-expanded", String(open));
    body.hidden = !open;
    el.classList.toggle("pf-m-expanded", open);
  };
  toggle.addEventListener("click", () => set(toggle.getAttribute("aria-expanded") !== "true"));
  set(opts.open ?? false);
  el.append(toggleBox, body);
  return { el, toggle, body, set };
}
