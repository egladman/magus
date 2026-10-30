import assert from "node:assert/strict";
import { test } from "node:test";
import { demoDiagramList, renderDemoDiagram } from "./demo";

test("each listed demo figure renders nodes that its edges name", () => {
  const entries = demoDiagramList();
  assert.ok(entries.length >= 2);
  for (const entry of entries) {
    const drawn = renderDemoDiagram(entry.id);
    assert.ok(!drawn.error, entry.id);
    const svg = drawn.value.svg;
    const nodes = new Set([...svg.matchAll(/data-node="([^"]+)"/g)].map((m) => m[1]));
    const edges = [...svg.matchAll(/data-edge="([^"]+)->([^"]+)"/g)];
    assert.ok(edges.length > 0, entry.id + " draws no edge");
    for (const [, from, to] of edges) {
      assert.ok(nodes.has(from ?? ""), `${entry.id}: edge from unknown ${from}`);
      assert.ok(nodes.has(to ?? ""), `${entry.id}: edge to unknown ${to}`);
    }
  }
});

test("the demo draws without touching the network", () => {
  const real = globalThis.fetch;
  globalThis.fetch = (() => {
    throw new Error("the demo asked for the network");
  }) as typeof fetch;
  try {
    for (const entry of demoDiagramList()) assert.ok(!renderDemoDiagram(entry.id).error);
  } finally {
    globalThis.fetch = real;
  }
});

test("an unknown figure is a failure, not an empty drawing", () => {
  assert.ok(renderDemoDiagram("nope").error);
});
