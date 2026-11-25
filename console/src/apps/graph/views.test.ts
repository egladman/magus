// views.test.ts - which project owns a node, and what a cold load opens on.
// Run: `magus run test console`.

import assert from "node:assert/strict";
import { test } from "node:test";
import type { GLink, GNode } from "./types";
import { LEGIBLE_NODE_COUNT, opensProjected, projectOwners } from "./views";

function node(id: string): GNode {
  return { id, kind: "target", label: id } as unknown as GNode;
}
function link(source: string, target: string, relation = "depends_on"): GLink {
  return { source, target, relation };
}

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
  // a question nobody asked, and it breaks every `magus graph export --open` link.
  assert.equal(opensProjected(2374, true), false);
  assert.equal(opensProjected(40, true), false);
});
