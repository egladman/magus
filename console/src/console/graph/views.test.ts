// views.test.ts - the direction and relation rules behind the "most depended-on" and "what is
// dead" questions. Run: `pnpm run test`.

import assert from "node:assert/strict";
import { test } from "node:test";
import type { GLink, GNode } from "./types";
import { dependencyDegrees, disconnected, mostDependedOn } from "./views";

function node(id: string): GNode {
  return { id, kind: "target", label: id } as unknown as GNode;
}
function link(source: string, target: string, relation = "depends_on"): GLink {
  return { source, target, relation };
}

// A project containing three targets, where lib is the one both apps depend on, and `spare` is
// held only by the project's structural edge.
const NODES = ["proj", "app-a", "app-b", "lib", "spare"].map(node);
const LINKS = [
  link("proj", "app-a", "contains"),
  link("proj", "app-b", "contains"),
  link("proj", "lib", "contains"),
  link("proj", "spare", "contains"),
  link("app-a", "lib"),
  link("app-b", "lib"),
];

test("dependency degrees ignore structural containment", () => {
  const deg = dependencyDegrees(NODES, LINKS);
  assert.deepEqual(deg.get("lib"), { dependents: 2, dependencies: 0 });
  assert.deepEqual(deg.get("app-a"), { dependents: 0, dependencies: 1 });
  // Four contains edges leave the project, and none of them is a dependency.
  assert.deepEqual(deg.get("proj"), { dependents: 0, dependencies: 0 });
});

test("dependents count the incoming direction, not the undirected total", () => {
  const deg = dependencyDegrees(NODES, LINKS);
  assert.equal(deg.get("lib")?.dependents, 2, "two apps depend on lib");
  assert.equal(deg.get("lib")?.dependencies, 0, "lib depends on nothing");
});

test("mostDependedOn ranks the dependency, not the container", () => {
  // The project touches four nodes and lib only two, so an undirected degree over every
  // relation would put the container first - the answer this question must not give.
  assert.deepEqual(mostDependedOn(NODES, LINKS, 3), ["lib"]);
});

test("mostDependedOn drops nodes nothing depends on rather than padding to the limit", () => {
  assert.deepEqual(mostDependedOn(NODES, LINKS, 12), ["lib"]);
});

test("mostDependedOn returns nothing for a graph with no dependency edges", () => {
  const structural = LINKS.filter((e) => e.relation === "contains");
  assert.deepEqual(mostDependedOn(NODES, structural, 12), []);
});

test("mostDependedOn breaks ties by id so the answer is stable", () => {
  const nodes = ["z", "a", "hub-1", "hub-2"].map(node);
  const links = [link("z", "hub-2"), link("a", "hub-1")];
  assert.deepEqual(mostDependedOn(nodes, links, 2), ["hub-1", "hub-2"]);
});

test("mostDependedOn counts every dependency relation, not only depends_on", () => {
  const nodes = ["caller", "importer", "user", "core"].map(node);
  const links = [
    link("caller", "core", "calls"),
    link("importer", "core", "imports"),
    link("user", "core", "uses"),
  ];
  assert.deepEqual(dependencyDegrees(nodes, links).get("core"), {
    dependents: 3,
    dependencies: 0,
  });
  assert.deepEqual(mostDependedOn(nodes, links, 1), ["core"]);
});

test("a node held only by its project's contains edge still reads as dead", () => {
  // The reason a plain degree-zero test finds nearly nothing: in a knowledge graph every
  // target carries this edge, so no target is ever degree zero.
  assert.deepEqual(disconnected(NODES, LINKS), ["proj", "spare"]);
});

test("disconnected excludes anything on either end of a dependency edge", () => {
  const ids = disconnected(NODES, LINKS);
  for (const id of ["app-a", "app-b", "lib"]) {
    assert.ok(!ids.includes(id), id + " sits on a dependency edge");
  }
});

test("documenting a node is not depending on it", () => {
  const nodes = ["readme", "build"].map(node);
  const links = [link("readme", "build", "documents")];
  assert.deepEqual(disconnected(nodes, links), ["readme", "build"]);
  assert.deepEqual(mostDependedOn(nodes, links, 5), []);
});

test("an edge to a node outside the set is skipped", () => {
  const nodes = [node("a")];
  assert.deepEqual(dependencyDegrees(nodes, [link("a", "ghost")]).get("a"), {
    dependents: 0,
    dependencies: 0,
  });
});

test("a self-dependency counts on both sides and is not dead", () => {
  const nodes = [node("a")];
  const links = [link("a", "a")];
  assert.deepEqual(dependencyDegrees(nodes, links).get("a"), { dependents: 1, dependencies: 1 });
  assert.deepEqual(disconnected(nodes, links), []);
});

test("an empty graph yields empty answers", () => {
  assert.deepEqual(mostDependedOn([], [], 12), []);
  assert.deepEqual(disconnected([], []), []);
});
