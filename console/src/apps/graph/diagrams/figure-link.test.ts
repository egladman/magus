// figure-link.test.ts - the payload of a #figure= link, read the way figure-link.buzz writes it:
// JSON, UTF-8, base64url with no padding. Every refusal names what was wrong with the link.

import assert from "node:assert/strict";
import { test } from "node:test";
import { decodeFigureLink } from "./figure-link";
import { figureForLink, linkedRows } from "./wasm";

// The payload figure-link.buzz's figureLink writes for the graph decoded below: UTF-8 JSON as
// unpadded base64url. The last node's label puts a "-" in it.
const FIXTURE =
  "eyJ2IjoxLCJraW5kIjoiZGVwcyIsInRpdGxlIjoiSG93IGZhciB0aGlzIGNoYW5nZSByZWFjaGVzIOKGkiBjYWbDqSIsIm5vZGVzIjpbeyJpZCI6ImxpYnMvYSIsImxhYmVsIjoiYSIsInNlZWQiOnRydWV9LHsiaWQiOiJjbWQvYiIsImxhYmVsIjoiYiIsInNlZWQiOmZhbHNlfSx7ImlkIjoiaW50ZXJuYWwvYyIsImxhYmVsIjoiY8OpPz8-Iiwic2VlZCI6ZmFsc2V9XSwiZWRnZXMiOltbImxpYnMvYSIsImNtZC9iIl0sWyJsaWJzL2EiLCJpbnRlcm5hbC9jIl1dfQ";

function encode(doc: unknown): string {
  return Buffer.from(JSON.stringify(doc), "utf8")
    .toString("base64")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

const ok = {
  v: 1,
  kind: "deps",
  title: "t",
  nodes: [{ id: "a", label: "a", seed: true }],
  edges: [],
};

function refusal(payload: string): string {
  const read = decodeFigureLink(payload);
  assert.equal(read.ok, false, "expected a refusal");
  return read.ok ? "" : read.error;
}

test("the fixture decodes to the graph it was built from", () => {
  assert.deepEqual(decodeFigureLink(FIXTURE), {
    ok: true,
    figure: {
      title: "How far this change reaches → café",
      nodes: [
        { id: "libs/a", label: "a", seed: true },
        { id: "cmd/b", label: "b", seed: false },
        { id: "internal/c", label: "cé??>", seed: false },
      ],
      edges: [
        ["libs/a", "cmd/b"],
        ["libs/a", "internal/c"],
      ],
    },
  });
});

test("a padded payload is read as well", () => {
  assert.equal(decodeFigureLink(FIXTURE + "==").ok, true);
});

test("a node without a seed flag is no seed", () => {
  const read = decodeFigureLink(encode({ ...ok, nodes: [{ id: "a", label: "a" }] }));
  assert.deepEqual(read.ok && read.figure.nodes, [{ id: "a", label: "a", seed: false }]);
});

test("an empty payload is refused", () => {
  assert.match(refusal(""), /no figure/);
});

test("characters outside base64url are refused", () => {
  assert.match(refusal("eyJ2+/=="), /not base64url/);
  assert.match(refusal("ab cd"), /not base64url/);
});

test("a truncated payload is refused", () => {
  assert.match(refusal("eyJ2I"), /truncated/);
});

test("bytes that are not UTF-8 are refused", () => {
  assert.match(refusal("_w"), /not UTF-8/);
});

test("text that is not JSON is refused", () => {
  assert.match(refusal(Buffer.from("not json").toString("base64url")), /not JSON/);
});

test("JSON that is not an object is refused", () => {
  assert.match(refusal(encode([1, 2])), /no object/);
});

test("an unknown version is refused by name", () => {
  assert.match(refusal(encode({ ...ok, v: 2 })), /version 2 and this console reads version 1/);
  assert.match(refusal(encode({ kind: "deps" })), /version undefined/);
});

test("an unknown kind is refused by name", () => {
  assert.match(refusal(encode({ ...ok, kind: "flow" })), /"flow" figure/);
});

test("a missing title, nodes or edges is refused", () => {
  assert.match(refusal(encode({ ...ok, title: 3 })), /no title/);
  assert.match(refusal(encode({ ...ok, nodes: "a" })), /no nodes/);
  assert.match(refusal(encode({ ...ok, edges: null })), /no edges/);
});

test("a node without an id or label is refused by index", () => {
  const nodes = [{ id: "a", label: "a" }, { label: "b" }];
  assert.match(refusal(encode({ ...ok, nodes })), /node 1/);
  assert.match(refusal(encode({ ...ok, nodes: [{ id: "", label: "a" }] })), /node 0/);
});

test("a repeated node id is refused", () => {
  const nodes = [
    { id: "a", label: "a" },
    { id: "a", label: "b" },
  ];
  assert.match(refusal(encode({ ...ok, nodes })), /"a" appears twice/);
});

test("an edge that is not a pair of known ids is refused by index", () => {
  assert.match(refusal(encode({ ...ok, edges: [["a"]] })), /edge 0 is not/);
  assert.match(refusal(encode({ ...ok, edges: [["a", 1]] })), /edge 0 is not/);
  assert.match(refusal(encode({ ...ok, edges: [["a", "z"]] })), /edge 0 names "z", no node/);
});

// ---- the record the runtime draws --------------------------------------------------------

const linked = (seeds: number) => {
  const read = decodeFigureLink(
    encode({
      ...ok,
      nodes: ["a", "b", "c", "d"].map((id, i) => ({ id, label: id, seed: i < seeds })),
      edges: [
        ["a", "b"],
        ["b", "c"],
        ["b", "d"],
      ],
    }),
  );
  assert.ok(read.ok);
  return read.figure;
};

test("the record is actors and flows, dependency first, seeds tagged edited", () => {
  const f = figureForLink(linked(1));
  assert.deepEqual(
    f.boxes.map((b) => [b.actor?.name, b.actor?.tag, b.actor?.look]),
    [
      ["a", "edited", "focal"],
      ["b", "", "plain"],
      ["c", "", "plain"],
      ["d", "", "plain"],
    ],
  );
  assert.deepEqual(
    f.flows.map((fl) => [fl.src.actor?.name, fl.dst.actor?.name]),
    [
      ["a", "b"],
      ["b", "c"],
      ["b", "d"],
    ],
  );
  assert.equal(f.title, "t");
  assert.equal(f.graphEdges, false);
  assert.notEqual(f.unscopedWhy, "", "an actor-only figure says why it draws no directory");
});

test("seeds past the figure's accent budget are tagged and left plain", () => {
  const f = figureForLink(linked(3));
  assert.deepEqual(
    f.boxes.map((b) => [b.actor?.tag, b.actor?.look]),
    [
      ["edited", "plain"],
      ["edited", "plain"],
      ["edited", "plain"],
      ["", "plain"],
    ],
  );
});

test("a label two nodes share takes the node's id", () => {
  const read = decodeFigureLink(
    encode({
      ...ok,
      nodes: [
        { id: "libs/core", label: "core" },
        { id: "app/core", label: "core" },
      ],
    }),
  );
  assert.ok(read.ok);
  assert.deepEqual(
    figureForLink(read.figure).boxes.map((b) => b.actor?.name),
    ["core (libs/core)", "core (app/core)"],
  );
  assert.deepEqual(
    linkedRows(read.figure).map((n) => n.id),
    ["external:core (libs/core)", "external:core (app/core)"],
    "the list is keyed by the ids the drawing carries",
  );
});
