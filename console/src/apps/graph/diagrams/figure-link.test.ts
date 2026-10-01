// figure-link.test.ts - the payload of a #figure= link, read the way figure-link.buzz writes it:
// JSON, UTF-8, base64url with no padding. Every refusal names what was wrong with the link.

import assert from "node:assert/strict";
import { test } from "node:test";
import { decodeFigureLink } from "./figure-link";
import { figureForLink, linkedRows } from "./wasm";

// The payload figure-link.buzz's figureLink writes for the graph decoded below: UTF-8 JSON as
// unpadded base64url. The last node's label puts a "-" in it. Two edges are plain and one is
// optional and labelled, the way a session figure draws spawns and dependencies.
const FIXTURE =
  "eyJ2IjoyLCJraW5kIjoiZGVwcyIsInRpdGxlIjoiU2Vzc2lvbiBzLTE6IGNhZsOpIOKGkiBqb2JzIiwibm9kZXMiOlt7ImlkIjoic2Vzc2lvbiIsImxhYmVsIjoic2Vzc2lvbiIsInNlZWQiOnRydWV9LHsiaWQiOiJqb2ItYSIsImxhYmVsIjoiam9iLWEgKGRvbmUpIiwic2VlZCI6ZmFsc2V9LHsiaWQiOiJqb2ItYiIsImxhYmVsIjoiasO2Yi1iPz8-Iiwic2VlZCI6ZmFsc2V9XSwiZWRnZXMiOltbInNlc3Npb24iLCJqb2ItYSJdLFsic2Vzc2lvbiIsImpvYi1iIl0sWyJqb2ItYSIsImpvYi1iIiwib3B0aW9uYWwiLCJhZnRlciJdXX0";

function encode(doc: unknown): string {
  return Buffer.from(JSON.stringify(doc), "utf8")
    .toString("base64")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

const ok = {
  v: 2,
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
      title: "Session s-1: café → jobs",
      nodes: [
        { id: "session", label: "session", seed: true },
        { id: "job-a", label: "job-a (done)", seed: false },
        { id: "job-b", label: "jöb-b??>", seed: false },
      ],
      edges: [
        { from: "session", to: "job-a", stroke: "plain", label: "" },
        { from: "session", to: "job-b", stroke: "plain", label: "" },
        { from: "job-a", to: "job-b", stroke: "optional", label: "after" },
      ],
    },
  });
});

test("every stroke and label survives the round trip", () => {
  const nodes = [
    { id: "a", label: "a" },
    { id: "b", label: "b" },
  ];
  const edges = [
    ["a", "b"],
    ["a", "b", "optional", "after"],
    ["b", "a", "focal"],
    ["b", "a", "external", "calls"],
    ["a", "b", "plain", "spawns"],
  ];
  const read = decodeFigureLink(encode({ ...ok, nodes, edges }));
  assert.ok(read.ok, read.ok ? "" : read.error);
  assert.deepEqual(
    read.figure.edges.map((e) => [e.from, e.to, e.stroke, e.label]),
    [
      ["a", "b", "plain", ""],
      ["a", "b", "optional", "after"],
      ["b", "a", "focal", ""],
      ["b", "a", "external", "calls"],
      ["a", "b", "plain", "spawns"],
    ],
  );
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
  assert.match(refusal(encode({ ...ok, v: 1 })), /version 1 and this console reads version 2/);
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

test("an edge that is not two known ids, a stroke and a label is refused by index", () => {
  assert.match(refusal(encode({ ...ok, edges: [["a"]] })), /edge 0 is not/);
  assert.match(refusal(encode({ ...ok, edges: [["a", 1]] })), /edge 0 is not/);
  assert.match(refusal(encode({ ...ok, edges: [["a", "a", "plain", "x", "y"]] })), /edge 0 is not/);
  assert.match(refusal(encode({ ...ok, edges: [["a", "z"]] })), /edge 0 names "z", no node/);
  assert.match(
    refusal(encode({ ...ok, edges: [["a", "a", "dotted"]] })),
    /edge 0 has stroke "dotted", not one of plain, focal, external, optional/,
  );
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
        ["b", "d", "optional", "after"],
      ],
    }),
  );
  assert.ok(read.ok);
  return read.figure;
};

test("the record is actors and flows carrying each edge's stroke and label, seeds tagged edited", () => {
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
    f.flows.map((fl) => [fl.src.actor?.name, fl.dst.actor?.name, fl.stroke, fl.label]),
    [
      ["a", "b", "plain", ""],
      ["b", "c", "plain", ""],
      ["b", "d", "optional", "after"],
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
