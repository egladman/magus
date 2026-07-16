// search.ts - find-in-page over the rendered log body. Case-insensitive substring highlight
// that wraps matches in <mark> (preserving the surrounding ANSI-colored spans), tracks an
// active match, and steps through them (Enter / prev / next), expanding a collapsed section so
// the active match is visible. Operates on the DOM render() produced, so it is cleared and
// re-run whenever the body is rebuilt (a view toggle, a filter change).

import { bodyEl, el } from "./dom";

let searchMarks: HTMLElement[] = [];
let activeMark = -1;

export function runSearch(query: string): void {
  clearMarks();
  const countEl = el("search-count");
  const prevBtn = el("search-prev");
  const nextBtn = el("search-next");
  if (!query) {
    if (countEl) countEl.textContent = "";
    if (prevBtn) (prevBtn as HTMLButtonElement).disabled = true;
    if (nextBtn) (nextBtn as HTMLButtonElement).disabled = true;
    return;
  }
  const needle = query.toLowerCase();
  for (const lc of bodyEl.querySelectorAll(".console-render-line__content")) {
    highlightIn(lc, needle);
  }
  if (countEl) countEl.textContent = searchMarks.length ? "1/" + searchMarks.length : "0";
  const has = searchMarks.length > 0;
  if (prevBtn) (prevBtn as HTMLButtonElement).disabled = !has;
  if (nextBtn) (nextBtn as HTMLButtonElement).disabled = !has;
  if (has) setActiveMark(0);
}

// highlightIn walks the text nodes of one line and wraps case-insensitive matches
// of needle in <mark>, preserving the surrounding ANSI-colored spans.
function highlightIn(lc: Element, needle: string): void {
  const walker = document.createTreeWalker(lc, NodeFilter.SHOW_TEXT);
  const textNodes: Node[] = [];
  let n: Node | null;
  while ((n = walker.nextNode())) textNodes.push(n);
  for (const node of textNodes) {
    const text = node.nodeValue!;
    const lower = text.toLowerCase();
    let idx = lower.indexOf(needle);
    if (idx < 0) continue;
    const frag = document.createDocumentFragment();
    let pos = 0;
    while (idx >= 0) {
      if (idx > pos) frag.appendChild(document.createTextNode(text.slice(pos, idx)));
      const mark = document.createElement("mark");
      mark.textContent = text.slice(idx, idx + needle.length);
      frag.appendChild(mark);
      searchMarks.push(mark);
      pos = idx + needle.length;
      idx = lower.indexOf(needle, pos);
    }
    if (pos < text.length) frag.appendChild(document.createTextNode(text.slice(pos)));
    (node.parentNode as Node).replaceChild(frag, node);
  }
}

export function clearMarks(): void {
  for (const mark of searchMarks) {
    const parent = mark.parentNode as (Node & ParentNode) | null;
    if (!parent) continue;
    parent.replaceChild(document.createTextNode(mark.textContent!), mark);
    parent.normalize();
  }
  searchMarks = [];
  activeMark = -1;
}

export function setActiveMark(i: number): void {
  if (!searchMarks.length) return;
  if (activeMark >= 0 && searchMarks[activeMark]) searchMarks[activeMark].removeAttribute("data-active");
  activeMark = (i + searchMarks.length) % searchMarks.length;
  const mark = searchMarks[activeMark];
  mark.setAttribute("data-active", "");
  // Expand a collapsed section so the active match is visible.
  const sec = mark.closest(".console-render-section");
  if (sec && sec.hasAttribute("data-collapsed")) {
    sec.removeAttribute("data-collapsed");
    const head = sec.querySelector(".console-render-section__head");
    if (head) head.setAttribute("aria-expanded", "true");
  }
  mark.scrollIntoView({ block: "center", behavior: "smooth" });
  const countEl = el("search-count");
  if (countEl) countEl.textContent = activeMark + 1 + "/" + searchMarks.length;
}

// stepActiveMark advances the active match by dir (+1 / -1), the Enter-key gesture. Exposed so
// the controls wiring need not read the module-private activeMark cursor.
export function stepActiveMark(dir: number): void {
  setActiveMark(activeMark + dir);
}
