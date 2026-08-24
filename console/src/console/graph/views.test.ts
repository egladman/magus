// views.test.ts - the direction and relation rules behind the "most depended-on" and "what is
// dead" questions. Run: `pnpm run test`.

import assert from "node:assert/strict";
import { test } from "node:test";
import type { GLink, GNode } from "./types";
import {
  LEGIBLE_NODE_COUNT,
  dependencyDegrees,
  disconnected,
  mostDependedOn,
  opensProjected,
  projectOwners,
} from "./views";

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

test("a self-dependency makes its kind dependency-bearing", () => {
  const nodes = [node("a"), node("b")];
  assert.deepEqual(disconnected(nodes, [link("a", "a")]), ["b"]);
});

test("disconnected excludes anything on either end of a dependency edge", () => {
  const ids = disconnected(NODES, LINKS);
  for (const id of ["app-a", "app-b", "lib"]) {
    assert.ok(!ids.includes(id), id + " sits on a dependency edge");
  }
});

test("documenting a node is not depending on it", () => {
  const nodes = ["readme", "build", "app", "lib"].map(node);
  // app -> lib is what makes the kind dependency-bearing; the documents edge must not save
  // readme or build from the dead list, and must not put build on the hub list.
  const links = [link("readme", "build", "documents"), link("app", "lib")];
  assert.deepEqual(disconnected(nodes, links), ["readme", "build"]);
  assert.deepEqual(mostDependedOn(nodes, links, 5), ["lib"]);
});

test("a kind that never bears a dependency is not dead code", () => {
  const nodes = [node("app"), node("lib"), { ...node("readme"), kind: "doc" } as GNode];
  const links = [link("app", "lib")];
  // The doc kind sits on no dependency edge anywhere in this graph, so calling it dead says
  // nothing about the workspace - unlike the target kind, where "no dependency" is a finding.
  assert.deepEqual(disconnected(nodes, links), []);
});

test("a kind qualifies as soon as one node of it bears a dependency", () => {
  const docA = { ...node("doc-a"), kind: "doc" } as GNode;
  const docB = { ...node("doc-b"), kind: "doc" } as GNode;
  const nodes = [docA, docB, node("lib")];
  const links = [link("doc-a", "lib", "references")];
  assert.deepEqual(disconnected(nodes, links), ["doc-b"]);
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

// projectOwners: who contains what. A knowledge graph carries no attrs.project, so the answer
// has to come from the `contains` edges.

const proj = (id: string): GNode => ({ id, kind: "project", label: id }) as unknown as GNode;

test("a project owns what it contains, transitively", () => {
  const nodes = [proj("project:web"), node("dir:src"), node("file:a.ts"), node("fn:a.ts:go")];
  const links = [
    link("project:web", "dir:src", "contains"),
    link("dir:src", "file:a.ts", "contains"),
    link("file:a.ts", "fn:a.ts:go", "contains"),
  ];
  const owner = projectOwners(nodes, links);
  assert.equal(owner.get("fn:a.ts:go"), "project:web", "ownership reaches three hops down");
  assert.equal(owner.get("project:web"), "project:web", "a project owns itself");
});

test("a nested project wins over the parent that also reaches it", () => {
  const nodes = [proj("project:."), proj("project:libs/x"), node("file:libs/x/a.ts")];
  const links = [
    link("project:.", "project:libs/x", "contains"),
    link("project:libs/x", "file:libs/x/a.ts", "contains"),
    link("project:.", "file:libs/x/a.ts", "contains"),
  ];
  const owner = projectOwners(nodes, links);
  assert.equal(owner.get("project:libs/x"), "project:libs/x", "a nested project owns itself");
  assert.equal(owner.get("file:libs/x/a.ts"), "project:libs/x");
});

test("only contains edges confer ownership", () => {
  const nodes = [proj("project:web"), node("spell:go")];
  const owner = projectOwners(nodes, [link("project:web", "spell:go", "uses")]);
  assert.equal(owner.has("spell:go"), false, "using a spell does not make it project-owned");
});

test("a node no project contains is absent rather than attributed upward", () => {
  const nodes = [proj("project:."), node("diagnostic:MGS1001")];
  const owner = projectOwners(nodes, []);
  assert.deepEqual([...owner.keys()], ["project:."]);
});

test("a containment cycle terminates", () => {
  const nodes = [proj("project:web"), node("a"), node("b")];
  const links = [
    link("project:web", "a", "contains"),
    link("a", "b", "contains"),
    link("b", "a", "contains"),
  ];
  const owner = projectOwners(nodes, links);
  assert.equal(owner.get("b"), "project:web");
});

// What a cold load opens on. These pin the DECISION, not the number - the number is a judgement and
// may well move, but each of these cases says what one side of the line is for, so moving it means
// answering them rather than editing a constant on a hunch.
test("a graph too big to read whole opens projected", () => {
  // magus's own graph, which is the one most readers meet first. It is the case the previous
  // threshold got wrong: that guard sat at 2500 for frame-budget reasons, so this opened as a
  // 2374-node hairball.
  assert.equal(opensProjected(2374, false), true);
});

test("a graph small enough to read whole opens whole", () => {
  // Collapsing this to a handful of project dots hides a picture that was already legible, which is
  // the worse first impression - the reason the rule is a threshold and not "always project".
  assert.equal(opensProjected(40, false), false);
  assert.equal(opensProjected(LEGIBLE_NODE_COUNT, false), false, "the threshold itself is legible");
  assert.equal(opensProjected(LEGIBLE_NODE_COUNT + 1, false), true);
});

test("a fragment directive is never overridden by the projection", () => {
  // #view/#q/#node name a specific thing to look at. Answering with a projects-only summary answers
  // a question nobody asked, and it breaks every `magus graph open` link.
  assert.equal(opensProjected(2374, true), false);
  assert.equal(opensProjected(40, true), false);
});
