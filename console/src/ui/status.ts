// status.ts - the console's status marks. A dot or a colour alone says nothing to a reader who cannot
// tell the hues apart, so every status carries a shape (the icon) and a word (the hidden text). The
// icons are PatternFly's own status paths, so a mark matches the Alert beside it.

const NS = "http://www.w3.org/2000/svg";

export type Status = "success" | "danger" | "warning" | "info" | "running" | "neutral";
export type GlyphKind = Status | "custom";

// PF's 32x32 status glyphs, as shipped in its Alert examples.
const PATHS: Record<Exclude<GlyphKind, "running">, string> = {
  success:
    "M16 1C7.729 1 1 7.729 1 16s6.729 15 15 15 15-6.729 15-15S24.271 1 16 1Zm7.795 11.795-8.646 8.646c-.317.317-.733.475-1.149.475s-.832-.158-1.149-.475l-4.646-4.646a1.126 1.126 0 0 1 1.591-1.591l4.205 4.205 8.205-8.205a1.126 1.126 0 0 1 1.591 1.591Z",
  danger:
    "M16 1C7.729 1 1 7.729 1 16s6.729 15 15 15 15-6.729 15-15S24.271 1 16 1Zm-1.5 8a1.5 1.5 0 1 1 3 0v7a1.5 1.5 0 1 1-3 0V9ZM16 25.001a2 2 0 1 1-.001-3.999A2 2 0 0 1 16 25.001Z",
  warning:
    "m31.874 28.514-15.011-27a1.001 1.001 0 0 0-1.748 0l-15.011 27A1 1 0 0 0 .978 30H31a1 1 0 0 0 .874-1.486ZM14.5 12a1.5 1.5 0 0 1 3 0v5a1.5 1.5 0 0 1-3 0v-5ZM16 26.001a2 2 0 1 1-.001-3.999A2 2 0 0 1 16 26.001Z",
  info: "M16 1C7.729 1 1 7.729 1 16s6.729 15 15 15 15-6.729 15-15S24.271 1 16 1Zm1.5 22a1.5 1.5 0 1 1-3 0v-5.157l-.188.04a1.5 1.5 0 0 1-.625-2.934l1.956-.416c.112-.024.223-.032.333-.03l.024-.002a1.5 1.5 0 0 1 1.5 1.5v7Zm-.08-12.58c-.38.37-.89.58-1.42.58a1.998 1.998 0 0 1-1.851-2.76c.051-.13.11-.24.19-.35.07-.11.15-.21.25-.3.74-.75 2.08-.75 2.83 0 .09.09.17.19.24.3.08.11.14.22.189.35.05.12.09.24.11.37.03.13.04.26.04.39 0 .53-.21 1.04-.58 1.42Z",
  custom:
    "M28.75 22v3.5c0 .689-.561 1.25-1.25 1.25h-7.521c.005.084.021.166.021.25 0 2.206-1.794 4-4 4s-4-1.794-4-4c0-.084.016-.166.021-.25H4.5c-.689 0-1.25-.561-1.25-1.25V22a.75.75 0 0 1 .75-.75c1.24 0 2.25-1.009 2.25-2.25v-4c0-4.826 3.528-8.833 8.138-9.605A2.482 2.482 0 0 1 13.5 3.5C13.5 2.122 14.621 1 16 1s2.5 1.122 2.5 2.5c0 .761-.349 1.436-.888 1.895 4.61.772 8.138 4.779 8.138 9.605v4c0 1.241 1.01 2.25 2.25 2.25a.75.75 0 0 1 .75.75Z",
  // A disc with a bar through it: not a verdict, so neither a check nor a cross.
  neutral:
    "M16 1C7.729 1 1 7.729 1 16s6.729 15 15 15 15-6.729 15-15S24.271 1 16 1Zm7 17H9a2 2 0 1 1 0-4h14a2 2 0 1 1 0 4Z",
};

const LABELS: Record<Status, string> = {
  success: "Succeeded",
  danger: "Failed",
  warning: "Warning",
  info: "Info",
  running: "Running",
  neutral: "No status",
};

// statusLabel is the word a status reads as when the caller supplies none.
export function statusLabel(status: Status): string {
  return LABELS[status];
}

// statusGlyph is the bare svg for a status (or the Alert's "custom" bell): 1em square, currentColor,
// aria-hidden. The enclosing element supplies the colour; Alerts do it through their variant class.
// "running" is PF's Spinner, so a run in flight moves as well as being coloured.
export function statusGlyph(kind: GlyphKind): SVGElement {
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("aria-hidden", "true");
  if (kind === "running") {
    svg.setAttribute("class", "pf-v6-c-spinner pf-m-inline");
    svg.setAttribute("viewBox", "0 0 100 100");
    const path = document.createElementNS(NS, "circle");
    path.setAttribute("class", "pf-v6-c-spinner__path");
    path.setAttribute("cx", "50");
    path.setAttribute("cy", "50");
    path.setAttribute("r", "45");
    path.setAttribute("fill", "none");
    svg.append(path);
    return svg;
  }
  svg.setAttribute("class", "pf-v6-svg");
  svg.setAttribute("viewBox", "0 0 32 32");
  svg.setAttribute("fill", "currentColor");
  svg.setAttribute("width", "1em");
  svg.setAttribute("height", "1em");
  const path = document.createElementNS(NS, "path");
  path.setAttribute("d", PATHS[kind]);
  svg.append(path);
  return svg;
}

// statusIcon is the shape half of a status: a PF inline Icon in the status colour, aria-hidden. It
// carries no name of its own, so pair it with statusText (or visible text) in the same cell.
export function statusIcon(status: Status): HTMLElement {
  const icon = document.createElement("span");
  icon.className = "pf-v6-c-icon pf-m-inline";
  icon.setAttribute("aria-hidden", "true");
  const content = document.createElement("span");
  content.className =
    "pf-v6-c-icon__content" + (status === "neutral" ? "" : " pf-m-" + modifier(status));
  content.append(statusGlyph(status));
  icon.append(content);
  return icon;
}

// statusText is the word half: visually hidden, read by a screen reader and found by a text search.
export function statusText(status: Status, label?: string): HTMLElement {
  const text = document.createElement("span");
  text.className = "pf-v6-screen-reader";
  text.textContent = label ?? LABELS[status];
  return text;
}

// statusMark is the usual pairing, for a cell that shows only the icon: shape for the eye, word for
// the reader that has no eye.
export function statusMark(status: Status, label?: string): HTMLElement {
  const mark = document.createElement("span");
  mark.dataset.status = status;
  mark.append(statusIcon(status), statusText(status, label));
  return mark;
}

// modifier maps a status to the PF Icon colour modifier that carries it. PF has no "running"
// colour; a run in flight takes the info blue, which is the console's --console-status-running.
function modifier(status: Exclude<Status, "neutral">): string {
  return status === "running" ? "info" : status;
}
