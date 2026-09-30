// Turns the server's SVG into the page's figure and builds its node list and inline notices.
// Listeners beyond the list's own live in interact.ts.

import { h } from "../view";
import type { DiagramNode } from "./api";

const SVG_NS = "http://www.w3.org/2000/svg";

// sourceHref fills a source_url template for an anchor. Anchors name directories, so the line
// fragment is dropped. "" when there is no template or no anchor.
export function sourceHref(template: string, anchor: string): string {
  if (!template || !anchor) return "";
  const path = anchor.split("/").map(encodeURIComponent).join("/");
  return template.replace("#L{line}", "").replace("{line}", "").replace("{path}", path);
}

function isSvg(el: Element | null): el is SVGSVGElement {
  return el !== null && el.namespaceURI === SVG_NS && el.localName === "svg";
}

// The server drew it, but it is still markup from a response: drop anything that could run or embed.
function sanitize(root: Element): void {
  for (const el of [...root.querySelectorAll("script, foreignObject, iframe")]) el.remove();
  for (const el of [root, ...root.querySelectorAll("*")]) {
    for (const attr of [...el.attributes]) {
      const value = attr.value.trim().toLowerCase();
      if (attr.name.startsWith("on") || value.startsWith("javascript:"))
        el.removeAttribute(attr.name);
    }
  }
}

export interface PrepareOptions {
  readonly nodes: readonly DiagramNode[];
  readonly sourceUrl: string;
}

// prepareSvg parses the server's SVG into the page's figure: graphics roles, every node labelled,
// every anchored node a link that works without script, no fixed size. Server links are kept.
export function prepareSvg(text: string, opts: PrepareOptions): SVGSVGElement {
  const doc = new DOMParser().parseFromString(text, "image/svg+xml");
  const parsed = doc.documentElement;
  if (!isSvg(parsed) || doc.querySelector("parsererror"))
    throw new Error("the figure is not an SVG document");
  const svg = document.importNode(parsed, true);
  if (!isSvg(svg)) throw new Error("the figure is not an SVG document");
  sanitize(svg);

  // role="img" would make every node presentational, and the nodes are the point.
  svg.setAttribute("role", "graphics-document");
  svg.setAttribute("aria-roledescription", "diagram");
  svg.removeAttribute("width");
  svg.removeAttribute("height");

  const labels = new Map(opts.nodes.map((n) => [n.id, n.label]));
  for (const el of [...svg.querySelectorAll("[data-node]")]) {
    const id = el.getAttribute("data-node") ?? "";
    const label = labels.get(id) ?? el.textContent?.trim() ?? id;
    const anchor = el.getAttribute("data-anchor") ?? "";
    let node = el;
    if (el.localName !== "a") {
      const href = sourceHref(opts.sourceUrl, anchor);
      if (href) {
        const a = document.createElementNS(SVG_NS, "a");
        a.setAttribute("href", href);
        for (const name of ["data-node", "data-anchor"]) {
          const v = el.getAttribute(name);
          if (v !== null) a.setAttribute(name, v);
          el.removeAttribute(name);
        }
        el.replaceWith(a);
        a.append(el);
        node = a;
      }
    }
    if (node.localName === "a") {
      // The console is an app window; a source link opens beside it rather than replacing it.
      node.setAttribute("target", "_blank");
      node.setAttribute("rel", "noopener noreferrer");
      node.setAttribute("aria-label", label + (anchor ? ", source " + anchor : ""));
    } else {
      node.setAttribute("role", "graphics-object");
      node.setAttribute("aria-label", label);
    }
    node.setAttribute("tabindex", "-1");
  }
  return svg;
}

export interface NodeListOptions {
  readonly nodes: readonly DiagramNode[];
  readonly sourceUrl: string;
  readonly onFocus: (id: string) => void;
}

// renderNodeList is the figure in words: every box, its source link, and a control that focuses
// it in the drawing.
export function renderNodeList(list: HTMLElement, opts: NodeListOptions): void {
  list.replaceChildren();
  for (const n of opts.nodes) {
    const li = h("li", "console-diagrams__node");
    li.dataset.nodeId = n.id;
    const focus = h("button", "pf-v6-c-button pf-m-link pf-m-inline", n.label);
    focus.type = "button";
    focus.setAttribute("aria-label", "Focus " + n.label + " in the figure");
    focus.addEventListener("click", () => opts.onFocus(n.id));
    li.append(focus);
    const href = sourceHref(opts.sourceUrl, n.anchor);
    if (href) {
      const a = h("a", "console-diagrams__source", n.anchor);
      a.href = href;
      a.target = "_blank";
      a.rel = "noopener noreferrer";
      li.append(a);
    } else if (n.anchor) {
      li.append(h("span", "console-diagrams__source", n.anchor));
    }
    list.append(li);
  }
}

// markListFocus mirrors the figure's focus in the list.
export function markListFocus(
  list: HTMLElement,
  focused: string | null,
  lit: ReadonlySet<string>,
): void {
  for (const li of list.querySelectorAll<HTMLElement>("[data-node-id]")) {
    const id = li.dataset.nodeId ?? "";
    li.toggleAttribute("data-lit", focused !== null && lit.has(id));
    li.toggleAttribute("data-dim", focused !== null && !lit.has(id));
  }
}

export type NoticeTone = "danger" | "warning" | "info";

// notice builds the console's inline alert; severity shows in the left rule (diagrams.css).
export function notice(tone: NoticeTone, title: string, body: string): HTMLElement {
  const el = h("div", "pf-v6-c-alert pf-m-inline pf-m-" + tone + " console-diagrams__notice");
  el.setAttribute("role", tone === "info" ? "status" : "alert");
  const icon = h("div", "pf-v6-c-alert__icon", tone === "info" ? "i" : "!");
  icon.setAttribute("aria-hidden", "true");
  el.append(icon, h("p", "pf-v6-c-alert__title", title));
  if (body) {
    const desc = h("div", "pf-v6-c-alert__description");
    desc.append(h("p", undefined, body));
    el.append(desc);
  }
  return el;
}
