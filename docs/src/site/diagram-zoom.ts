// diagram-zoom.js - open an inlined diagram in a modal, with wheel zoom and drag to pan.
//
// Diagrams are laid out at a fixed viewBox and then scaled down to the prose column, which
// is fine for the shape and too small for the labels.
//
// Same shell as keyboard-help.js: a native <dialog> wrapping an <article> with a header and
// a "times" close button, opened with showModal(). That gets Pico's dialog styling, the
// focus trap, the backdrop and Esc-to-close for free, and keeps every overlay on this site
// looking like the same control rather than one per feature.
//
// Absent-but-harmless without JS: the expand button is injected here, so with the bundle
// off the figure is simply the inline SVG.
const MIN_SCALE = 0.25;
const MAX_SCALE = 8;

export function initDiagramZoom(): void {
  if (typeof document === "undefined") return;
  const figures = document.querySelectorAll<HTMLElement>(".magus-diagram-figure");
  if (figures.length === 0) return;

  figures.forEach(function (figure) {
    const svg = figure.querySelector("svg");
    if (!svg) return;
    // Same control as the playground's editor toggle (playground.html): the corner-arrows
    // glyph, the label "Fullscreen", and the same title wording. One verb for one gesture
    // across the site beats a second name for the same idea.
    const button = document.createElement("button");
    button.type = "button";
    button.className = "diagram-expand outline";
    button.title = "Expand the diagram to fill the screen";
    button.innerHTML =
      '<svg class="btn-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" ' +
      'stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
      '<path d="M8 3H5a2 2 0 0 0-2 2v3m18 0V5a2 2 0 0 0-2-2h-3M3 16v3a2 2 0 0 0 2 2h3m10 0h3a2 2 0 0 0 2-2v-3"/>' +
      '</svg><span class="btn-label">Fullscreen</span>';
    button.addEventListener("click", function () {
      open(svg, titleOf(svg));
    });
    figure.appendChild(button);
  });
}

// The renderer already writes an accessible name into <title>; reuse it as the dialog
// heading rather than inventing a second label that can drift from it.
function titleOf(svg: SVGElement): string {
  return svg.querySelector("title")?.textContent || "Diagram";
}

function open(source: SVGElement, heading: string): void {
  const d = document.createElement("dialog");
  d.className = "diagram-dialog";
  d.innerHTML =
    "<article>" +
    "<header><h2></h2>" +
    '<button type="button" aria-label="Close" class="diagram-close">&times;</button></header>' +
    '<div class="diagram-stage"></div>' +
    "</article>";
  // textContent, not innerHTML: the heading comes from diagram data.
  const h2 = d.querySelector("h2");
  if (h2) h2.textContent = heading;

  const stage = d.querySelector<HTMLElement>(".diagram-stage");
  const clone = source.cloneNode(true) as SVGElement;
  // The inline copy is width-constrained by .magus-diagram; the modal wants the full
  // viewBox, so drop the class the page stylesheet keys on.
  clone.removeAttribute("class");
  stage?.appendChild(clone);

  document.body.appendChild(d);
  d.querySelector(".diagram-close")?.addEventListener("click", function () {
    d.close();
  });
  d.addEventListener("click", function (e) {
    if (e.target === d) d.close();
  });
  // One dialog per open, so drop it on close rather than accumulating clones.
  d.addEventListener("close", function () {
    d.remove();
  });

  let scale = 1;
  let x = 0;
  let y = 0;
  const apply = function (): void {
    if (stage) stage.style.transform = `translate(${x}px, ${y}px) scale(${scale})`;
  };

  stage?.addEventListener(
    "wheel",
    function (e: WheelEvent) {
      e.preventDefault();
      // Multiplicative, not additive: a fixed step feels slow zoomed out and
      // uncontrollable zoomed in.
      const next = scale * (e.deltaY < 0 ? 1.12 : 1 / 1.12);
      scale = Math.min(MAX_SCALE, Math.max(MIN_SCALE, next));
      apply();
    },
    { passive: false },
  );

  let dragging = false;
  let startX = 0;
  let startY = 0;
  stage?.addEventListener("pointerdown", function (e: PointerEvent) {
    dragging = true;
    startX = e.clientX - x;
    startY = e.clientY - y;
    stage.setPointerCapture(e.pointerId);
  });
  stage?.addEventListener("pointermove", function (e: PointerEvent) {
    if (!dragging) return;
    x = e.clientX - startX;
    y = e.clientY - startY;
    apply();
  });
  const endDrag = function (): void {
    dragging = false;
  };
  stage?.addEventListener("pointerup", endDrag);
  stage?.addEventListener("pointercancel", endDrag);

  if (typeof d.showModal === "function") d.showModal();
  else d.setAttribute("open", "");
}
