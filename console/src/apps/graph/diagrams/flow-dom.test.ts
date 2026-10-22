import assert from "node:assert/strict";
import { afterEach, describe, test } from "node:test";
import { FLOW_CLASS, attachFlow, flowRunning, type Flow } from "./flow";

const SVG =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 480 280">' +
  '<defs><marker id="f-arrow-default"/></defs>' +
  '<path data-edge="a->b" id="f-edge-a-b" d="M 0 0 L 10 0" stroke="#4f5d75" marker-end="url(#f-arrow-default)"/>' +
  '<path data-edge="b->c" id="f-edge-b-c" d="M 10 0 L 20 0" stroke="#4f5d75"/>' +
  '<path data-edge="c->d" id="f-edge-c-d" d="M 20 0 L 30 0" stroke="#4f5d75" stroke-dasharray="4,4"/>' +
  '<g data-node="a"><rect x="0" y="0" width="5" height="5"/></g>' +
  "</svg>";

function mountSvg(): SVGSVGElement {
  const host = document.createElement("div");
  host.innerHTML = SVG;
  document.body.append(host);
  return host.querySelector("svg") as SVGSVGElement;
}

describe("figure edge flow", () => {
  let flow: Flow | null = null;
  afterEach(() => {
    flow?.stop();
    flow = null;
    document.body.replaceChildren();
  });

  test("overlays solid edges only, each right after its edge and no longer an edge", () => {
    const svg = mountSvg();
    flow = attachFlow(svg, { reducedMotion: () => false });
    const overlays = [...svg.querySelectorAll("." + FLOW_CLASS)];
    assert.equal(overlays.length, 2);
    assert.equal(svg.querySelectorAll("[data-edge]").length, 3);
    for (const o of overlays) {
      assert.equal(o.previousElementSibling?.hasAttribute("data-edge"), true);
      assert.equal(o.hasAttribute("data-edge"), false);
      assert.equal(o.hasAttribute("id"), false);
      assert.equal(o.hasAttribute("marker-end"), false);
      assert.equal(o.getAttribute("aria-hidden"), "true");
      assert.ok(o.getAttribute("stroke-dasharray"));
    }
    assert.equal(overlays[0].getAttribute("d"), "M 0 0 L 10 0");
    assert.equal(flowRunning(), true);
  });

  test("draws nothing and starts no timer under reduced motion", () => {
    const svg = mountSvg();
    flow = attachFlow(svg, { reducedMotion: () => true });
    assert.equal(svg.querySelectorAll("." + FLOW_CLASS).length, 0);
    assert.equal(flowRunning(), false);
  });

  test("stop removes the overlays and stops the shared timer", () => {
    const svg = mountSvg();
    flow = attachFlow(svg, { reducedMotion: () => false });
    flow.stop();
    assert.equal(svg.querySelectorAll("." + FLOW_CLASS).length, 0);
    assert.equal(flowRunning(), false);
    flow.stop();
  });

  test("the timer stays up until the last figure stops", () => {
    const one = attachFlow(mountSvg(), { reducedMotion: () => false });
    const two = attachFlow(mountSvg(), { reducedMotion: () => false });
    one.stop();
    assert.equal(flowRunning(), true);
    two.stop();
    assert.equal(flowRunning(), false);
  });
});
