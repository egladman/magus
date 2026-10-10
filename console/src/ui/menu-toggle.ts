// menu-toggle.ts - PatternFly's MenuToggle, built once: the button that opens a menu. Every trigger
// of a menu in the console is one of these rather than a Button with a caret drawn on it, so the
// expanded state, the caret and the focus treatment come from PF (aria-expanded drives its expanded
// style; ui/menu.ts's wireMenu keeps that attribute honest).

const NS = "http://www.w3.org/2000/svg";

// PF's 20-unit angle glyph, pointing right in its sheets; a quarter turn makes the toggle's caret.
const ANGLE =
  "M14.35 8.94 6.71 1.29l-.02-.02c-.4-.38-1.03-.37-1.41.02-.38.4-.37 1.03.02 1.41l7.29 7.29-7.29 7.29a1.003 1.003 0 0 0 1.42 1.42l7.65-7.65c.59-.59.59-1.54 0-2.12Z";
// PF's 32-unit vertical ellipsis, the kebab.
const KEBAB =
  "M12.25 5c0-2.068 1.683-3.75 3.75-3.75S19.75 2.932 19.75 5 18.067 8.75 16 8.75 12.25 7.068 12.25 5ZM16 12.25c-2.067 0-3.75 1.682-3.75 3.75s1.683 3.75 3.75 3.75 3.75-1.682 3.75-3.75-1.683-3.75-3.75-3.75Zm0 11c-2.067 0-3.75 1.682-3.75 3.75s1.683 3.75 3.75 3.75 3.75-1.682 3.75-3.75-1.683-3.75-3.75-3.75Z";

function glyph(viewBox: string, d: string, turn?: string): SVGElement {
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("class", "pf-v6-svg");
  svg.setAttribute("viewBox", viewBox);
  svg.setAttribute("fill", "currentColor");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("role", "img");
  svg.setAttribute("width", "1em");
  svg.setAttribute("height", "1em");
  const path = document.createElementNS(NS, "path");
  path.setAttribute("d", d);
  if (turn) path.setAttribute("transform", turn);
  svg.append(path);
  return svg;
}

export interface MenuToggleSpec {
  // The visible label. Omit it for an icon-only toggle, which then needs ariaLabel.
  text?: string;
  // A leading glyph, shown before the text.
  icon?: Node;
  // The accessible name when there is no text, as PF requires of a plain toggle.
  ariaLabel?: string;
  variant?: "default" | "secondary" | "plain";
  small?: boolean;
  danger?: boolean;
  // Extra classes for the caller's own hook.
  classes?: string;
}

// kebabIcon is the vertical ellipsis for an actions toggle.
export function kebabIcon(): SVGElement {
  return glyph("0 0 32 32", KEBAB);
}

// menuToggle builds the trigger. A toggle with text carries the caret in __controls; an icon-only
// plain toggle is just its icon.
export function menuToggle(spec: MenuToggleSpec): HTMLButtonElement {
  const variant = spec.variant ?? "default";
  const button = document.createElement("button");
  button.type = "button";
  button.className =
    "pf-v6-c-menu-toggle" +
    (variant === "default" ? "" : " pf-m-" + variant) +
    (spec.small ? " pf-m-small" : "") +
    (spec.danger ? " pf-m-danger" : "") +
    (spec.classes ? " " + spec.classes : "");
  button.setAttribute("aria-expanded", "false");
  if (spec.ariaLabel) button.setAttribute("aria-label", spec.ariaLabel);
  if (spec.icon) {
    const icon = document.createElement("span");
    icon.className = "pf-v6-c-menu-toggle__icon";
    icon.append(spec.icon);
    button.append(icon);
  }
  if (spec.text !== undefined) {
    const text = document.createElement("span");
    text.className = "pf-v6-c-menu-toggle__text";
    text.textContent = spec.text;
    button.append(text);
    const controls = document.createElement("span");
    controls.className = "pf-v6-c-menu-toggle__controls";
    const caret = document.createElement("span");
    caret.className = "pf-v6-c-menu-toggle__toggle-icon";
    caret.append(glyph("0 0 20 20", ANGLE, "rotate(90 10 10)"));
    controls.append(caret);
    button.append(controls);
  }
  return button;
}
