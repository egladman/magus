// glyph.ts - inline-SVG marks that appear on more than one surface, so the same action carries the
// same mark wherever a reader meets it. Refresh was an icon in the log viewer's run browser and
// bare text on both the Runs surface and the dashboard's insight band. A one-off mark still belongs
// in the surface that uses it.

const NS = "http://www.w3.org/2000/svg";

// REFRESH is the conventional circular arrow: an arc most of the way round with its head at the
// start, so it reads as "go again" rather than as a generic loop.
export const REFRESH: readonly string[] = ["M21 12a9 9 0 1 1-2.64-6.36", "M21 3v6h-6"];

// svgGlyph builds one mark from path d-strings. Stroked in currentColor and aria-hidden: it themes
// for free and every caller pairs it with a real label, so it must not be announced twice.
export function svgGlyph(paths: readonly string[], size = 16): SVGElement {
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("width", String(size));
  svg.setAttribute("height", String(size));
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "2");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  for (const d of paths) {
    const p = document.createElementNS(NS, "path");
    p.setAttribute("d", d);
    svg.append(p);
  }
  return svg;
}
