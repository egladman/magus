// view-dom.test.ts - a figure as the page shows it: the server's SVG prepared (roles, names, real
// links), then upgraded by attachFigure (focus and dim, hover, keys, Tab order). The fixture is
// shaped like magus/figure's output: data-node groups with data-anchor, data-edge paths.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { afterEach, describe, test } from "node:test";
import { attachFigure, type FigureController } from "./interact";
import { markListFocus, notice, prepareSvg, renderNodeList, sourceHref } from "./view";
import type { DiagramNode } from "./api";

const FIXTURE_SVG =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 480 280" width="480" height="280" role="img"' +
  ' aria-labelledby="t-title t-desc" class="magus-diagram"><title id="t-title">Fixture</title>' +
  '<desc id="t-desc"></desc><rect width="100%" height="100%" fill="var(--magus-diagram-paper, #f5f5f5)"/>' +
  '<g transform="translate(0,0)">' +
  '<path data-edge="a->b" d="M 192 140 L 288 140" fill="none" stroke="#4f5d75"/>' +
  '<path data-edge="d->b" d="M 368 76 L 368 112" fill="none" stroke="#4f5d75"/>' +
  '<g data-node="a" data-anchor="internal/job"><rect x="32" y="112" width="160" height="56"/><text x="112" y="140">job</text></g>' +
  '<g data-node="b" data-anchor="internal/job/store"><rect x="288" y="112" width="160" height="56"/><text x="368" y="140">store</text></g>' +
  '<g data-node="c"><rect x="32" y="200" width="160" height="56"/><text x="112" y="228">lone</text></g>' +
  '<g data-node="d" data-anchor="types"><rect x="288" y="20" width="160" height="56"/><text x="368" y="48">types</text></g>' +
  '<script>window.bad = 1</script><g onclick="window.bad = 2" data-x="1"></g>' +
  "</g></svg>";

const FIXTURE_NODES: DiagramNode[] = [
  { id: "a", anchor: "internal/job", label: "job" },
  { id: "b", anchor: "internal/job/store", label: "store" },
  { id: "c", anchor: "", label: "lone" },
  { id: "d", anchor: "types", label: "types" },
];

const TEMPLATE = "https://github.com/acme/widgets/blob/abc/{path}#L{line}";

let figure: FigureController | null = null;
// Scoped to this suite: the dom tests share one process, where a top-level hook would run around
// every other file's tests too.
describe("a prepared figure", () => {
  afterEach(() => {
    figure?.destroy();
    figure = null;
    document.body.replaceChildren();
  });

  function mount(): { frame: HTMLElement; svg: SVGSVGElement } {
    const frame = document.createElement("div");
    const svg = prepareSvg(FIXTURE_SVG, { nodes: FIXTURE_NODES, sourceUrl: TEMPLATE });
    frame.append(svg);
    document.body.append(frame);
    figure = attachFigure({ frame, svg, reducedMotion: () => true });
    return { frame, svg };
  }

  const node = (svg: SVGSVGElement, id: string): Element => {
    const el = svg.querySelector('[data-node="' + id + '"]');
    assert.ok(el, "node " + id);
    return el;
  };
  const edge = (svg: SVGSVGElement, id: string): Element => {
    const el = svg.querySelector('[data-edge="' + id + '"]');
    assert.ok(el, "edge " + id);
    return el;
  };

  test("a source link drops the line fragment and keeps the path's slashes", () => {
    assert.equal(
      sourceHref(TEMPLATE, "internal/job"),
      "https://github.com/acme/widgets/blob/abc/internal/job",
    );
    assert.equal(sourceHref("", "internal/job"), "");
    assert.equal(sourceHref(TEMPLATE, ""), "");
  });

  test("the prepared figure is a graphics document whose anchored nodes are links", () => {
    const { svg } = mount();
    assert.equal(svg.getAttribute("role"), "graphics-document");
    assert.equal(svg.hasAttribute("width"), false, "the frame sizes the figure");
    const a = node(svg, "a");
    assert.equal(a.localName, "a");
    assert.equal(a.getAttribute("href"), "https://github.com/acme/widgets/blob/abc/internal/job");
    assert.equal(a.getAttribute("target"), "_blank");
    assert.equal(a.getAttribute("aria-label"), "job, source internal/job");
    const lone = node(svg, "c");
    assert.equal(lone.localName, "g", "a node with no anchor has nowhere to link");
    assert.equal(lone.getAttribute("role"), "graphics-object");
    assert.equal(lone.getAttribute("aria-label"), "lone");
  });

  test("a node the server already linked keeps its link", () => {
    const linked = FIXTURE_SVG.replace(
      '<g data-node="a" data-anchor="internal/job">',
      '<a href="https://example.test/code/internal/job" data-node="a" data-anchor="internal/job"><g>',
    ).replace('<text x="112" y="140">job</text></g>', '<text x="112" y="140">job</text></g></a>');
    const svg = prepareSvg(linked, { nodes: FIXTURE_NODES, sourceUrl: TEMPLATE });
    assert.equal(node(svg, "a").getAttribute("href"), "https://example.test/code/internal/job");
  });

  test("nothing in the figure can run", () => {
    const { svg } = mount();
    assert.equal(svg.querySelector("script"), null);
    assert.equal(svg.querySelector("[onclick]"), null);
  });

  test("double-click focuses a node: its declared edges light, the rest dims, Esc clears", () => {
    const { frame, svg } = mount();
    node(svg, "a").dispatchEvent(new MouseEvent("dblclick", { bubbles: true }));
    assert.equal(svg.getAttribute("data-focused"), "a");
    assert.equal(node(svg, "a").hasAttribute("data-dim"), false);
    assert.equal(node(svg, "b").hasAttribute("data-dim"), false, "one hop out stays");
    assert.equal(node(svg, "c").hasAttribute("data-dim"), true);
    assert.equal(node(svg, "d").hasAttribute("data-dim"), true, "two hops out dims");
    assert.equal(edge(svg, "a->b").hasAttribute("data-lit"), true);
    assert.equal(edge(svg, "d->b").hasAttribute("data-dim"), true);

    frame.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    assert.equal(svg.hasAttribute("data-focused"), false);
    assert.equal(node(svg, "c").hasAttribute("data-dim"), false);
  });

  test("focusing a node with no declared edges lights nothing but itself", () => {
    const { svg } = mount();
    figure?.focusNode("c");
    assert.equal(node(svg, "c").hasAttribute("data-dim"), false);
    for (const id of ["a", "b", "d"])
      assert.equal(node(svg, id).hasAttribute("data-dim"), true, id);
    for (const id of ["a->b", "d->b"])
      assert.equal(edge(svg, id).hasAttribute("data-lit"), false, id);
  });

  test("hover turns only the hovered node's declared edges accent", () => {
    const { svg } = mount();
    node(svg, "d").dispatchEvent(new PointerEvent("pointermove", { bubbles: true }));
    assert.equal(edge(svg, "d->b").hasAttribute("data-hot"), true);
    assert.equal(edge(svg, "a->b").hasAttribute("data-hot"), false);
    node(svg, "c").dispatchEvent(new PointerEvent("pointermove", { bubbles: true }));
    assert.equal(
      svg.querySelectorAll("[data-hot]").length,
      0,
      "the lone node has no edge to light",
    );
  });

  test("one node is tabbable at a time, and Tab walks them in reading order", () => {
    const { svg } = mount();
    const tabbable = (): string[] =>
      [...svg.querySelectorAll('[data-node][tabindex="0"]')].map(
        (n) => n.getAttribute("data-node") ?? "",
      );
    assert.deepEqual(tabbable(), ["d"], "d sits on the top row");
    const walk: string[] = [];
    let at = node(svg, "d");
    for (;;) {
      walk.push(at.getAttribute("data-node") ?? "");
      const ev = new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true });
      at.dispatchEvent(ev);
      if (!ev.defaultPrevented) break; // the last node lets Tab leave the figure
      const next = svg.querySelector('[data-node][tabindex="0"]');
      assert.ok(next);
      at = next;
    }
    assert.deepEqual(walk, ["d", "a", "b", "c"]);
  });

  test("keys fit and zoom, snapping under reduced motion, and a modified key passes through", () => {
    const { frame, svg } = mount();
    const fitted = svg.getAttribute("viewBox");
    frame.dispatchEvent(new KeyboardEvent("keydown", { key: "+", bubbles: true }));
    const zoomed = svg.getAttribute("viewBox");
    assert.notEqual(zoomed, fitted, "reduced motion applies the zoom at once");
    const browserZoom = new KeyboardEvent("keydown", {
      key: "+",
      ctrlKey: true,
      bubbles: true,
      cancelable: true,
    });
    frame.dispatchEvent(browserZoom);
    assert.equal(browserZoom.defaultPrevented, false);
    assert.equal(svg.getAttribute("viewBox"), zoomed);
    frame.dispatchEvent(new KeyboardEvent("keydown", { key: "0", bubbles: true }));
    assert.equal(svg.getAttribute("viewBox"), "0 0 480 280", "0 is the figure's own size");
    frame.dispatchEvent(new KeyboardEvent("keydown", { key: "f", bubbles: true }));
    assert.equal(svg.getAttribute("viewBox"), fitted);
  });

  test("a plain wheel is the page's and a ctrl-wheel zooms the figure", () => {
    const { frame, svg } = mount();
    const before = svg.getAttribute("viewBox");
    const plain = new WheelEvent("wheel", { deltaY: 100, bubbles: true, cancelable: true });
    frame.dispatchEvent(plain);
    assert.equal(plain.defaultPrevented, false);
    assert.equal(svg.getAttribute("viewBox"), before);
    const pinch = new WheelEvent("wheel", {
      deltaY: -100,
      ctrlKey: true,
      bubbles: true,
      cancelable: true,
    });
    // happy-dom's WheelEvent drops the modifier keys from its init; a browser's carries them.
    Object.defineProperty(pinch, "ctrlKey", { value: true });
    frame.dispatchEvent(pinch);
    assert.equal(pinch.defaultPrevented, true);
    assert.notEqual(svg.getAttribute("viewBox"), before);
  });

  test("a re-layout swaps the drawing and keeps the reader's zoom", () => {
    const { frame, svg } = mount();
    figure?.zoom(2);
    const kept = svg.getAttribute("viewBox");
    const next = prepareSvg(FIXTURE_SVG, { nodes: FIXTURE_NODES, sourceUrl: "" });
    svg.replaceWith(next);
    figure?.replace(next);
    assert.equal(next.getAttribute("viewBox"), kept);
    assert.equal(frame.querySelector("svg"), next);
  });

  test("the node list names every box, links its source, and mirrors focus", () => {
    const list = document.createElement("ul");
    const focused: string[] = [];
    renderNodeList(list, {
      nodes: FIXTURE_NODES,
      sourceUrl: TEMPLATE,
      onFocus: (id) => focused.push(id),
    });
    const rows = [...list.querySelectorAll("li")];
    assert.deepEqual(
      rows.map((r) => r.querySelector("button")?.textContent),
      ["job", "store", "lone", "types"],
    );
    assert.equal(
      rows[0].querySelector("a")?.getAttribute("href"),
      "https://github.com/acme/widgets/blob/abc/internal/job",
    );
    assert.equal(rows[2].querySelector("a"), null);
    rows[1].querySelector("button")?.click();
    assert.deepEqual(focused, ["b"]);
    markListFocus(list, "a", new Set(["a", "b"]));
    assert.deepEqual(
      rows.map((r) => r.hasAttribute("data-dim")),
      [false, false, true, true],
    );
  });

  test("a notice is the console's alert strip with the server's words", () => {
    const el = notice("warning", "Too big", "10 nodes exceeds the budget of 9");
    assert.ok(el.classList.contains("pf-v6-c-alert"));
    assert.ok(el.classList.contains("pf-m-warning"));
    assert.ok(el.querySelector(".pf-v6-c-alert__icon"), "PF's grid needs the icon slot");
    assert.match(el.textContent ?? "", /exceeds the budget of 9/);
  });

  test("a notice names its severity in words and draws it as a shape, never a letter", () => {
    const danger = notice("danger", "Could not read the figure", "HTTP 500");
    assert.equal(danger.getAttribute("role"), "alert", "a failure is always announced");
    assert.match(danger.textContent ?? "", /Danger alert/);
    assert.ok(danger.querySelector(".pf-v6-c-alert__icon svg"), "the icon is PF's glyph");
    assert.doesNotMatch(
      danger.querySelector(".pf-v6-c-alert__icon")?.textContent ?? "",
      /\S/,
      "no letter standing in for the icon",
    );
    const info = notice("info", "Nothing to draw", "");
    assert.equal(info.getAttribute("role"), "status");
    assert.equal(info.querySelector(".pf-v6-c-alert__description"), null, "no empty description");
  });

  test("a notice body may be a node, for a command that wants a <code> element", () => {
    const body = document.createElement("p");
    body.append(
      "Start one with ",
      Object.assign(document.createElement("code"), { textContent: "magus server start" }),
    );
    const el = notice("info", "No server", body);
    assert.equal(el.querySelector("code")?.textContent, "magus server start");
  });

  // Every role cssVarPalette paints (magus/figure, libs/figure/figure.buzz) needs a console
  // definition, or that role falls back to its light hex on a dark console.
  test("tokens.css defines every --magus-diagram-* role", () => {
    const css = readFileSync("src/styles/tokens.css", "utf8");
    for (const role of [
      "paper",
      "fill",
      "ink",
      "muted",
      "soft",
      "rule",
      "accent",
      "accent-tint",
      "link",
    ])
      assert.match(css, new RegExp("--magus-diagram-" + role + ":\\s*[^;\\s][^;]*;"), role);
  });
});
