// lens.test.ts - the lens form, its query string, its #fragment round trip, and the cut that
// must keep what the server's graph.cut keeps (internal/handler/diagram/render_test.go).

import assert from "node:assert/strict";
import { test } from "node:test";
import {
  EMPTY_LENS,
  cutDeclaration,
  describeLens,
  fragmentForView,
  lensFields,
  lensQuery,
  parseLensFields,
  viewFromHash,
  type Declaration,
} from "./lens";

test("an empty lens is the bare figure URL", () => {
  assert.equal(lensQuery(EMPTY_LENS), "");
});

test("scope repeats, focus and depth follow, as the handler's parseLens reads them", () => {
  const q = lensQuery({ scope: ["internal/server", "types"], focus: "libs/lib", depth: 2 });
  assert.equal(q, "?scope=internal%2Fserver&scope=types&focus=libs%2Flib&depth=2");
  assert.deepEqual(new URLSearchParams(q).getAll("scope"), ["internal/server", "types"]);
  assert.equal(
    lensQuery({ scope: [], focus: "app", depth: null }),
    "?focus=app",
    "the server defaults depth",
  );
});

test("the form splits scope on commas and spaces and refuses what the server would", () => {
  assert.deepEqual(parseLensFields({ scope: " a/b, c  d ", focus: " x ", depth: "" }), {
    value: { scope: ["a/b", "c", "d"], focus: "x", depth: null },
  });
  assert.deepEqual(parseLensFields({ scope: "", focus: "x", depth: "0" }), {
    value: { scope: [], focus: "x", depth: 0 },
  });
  assert.equal(
    parseLensFields({ scope: "", focus: "", depth: "2" }).error?.message,
    "Depth needs a focus.",
  );
  assert.ok(parseLensFields({ scope: "", focus: "x", depth: "-1" }).error);
  assert.ok(parseLensFields({ scope: "", focus: "x", depth: "1.5" }).error);
});

test("the form shows a lens the way it reads one", () => {
  const lens = { scope: ["a", "b"], focus: "x", depth: 3 };
  const parsed = parseLensFields(lensFields(lens));
  assert.deepEqual(parsed, { value: lens });
});

test("the fragment carries the view and keeps every other key", () => {
  const view = { id: "targets:app", lens: { scope: ["libs"], focus: "libs-lib", depth: 2 } };
  const hash = fragmentForView({ port: "7391", demo: "", focus: "stale" }, view);
  assert.equal(hash, "#port=7391&demo&diagram=targets%3Aapp&scope=libs&focus=libs-lib&depth=2");
  const params = Object.fromEntries(
    hash
      .slice(1)
      .split("&")
      .map((p) => {
        const [k = "", v = ""] = p.split("=");
        return [decodeURIComponent(k), decodeURIComponent(v)];
      }),
  );
  assert.deepEqual(viewFromHash(params), view);
  assert.equal(params.port, "7391");
});

test("no figure clears the view keys and leaves an empty fragment empty", () => {
  assert.equal(
    fragmentForView({ diagram: "projects", scope: "a" }, { id: null, lens: EMPTY_LENS }),
    "",
  );
  assert.deepEqual(viewFromHash({}), { id: null, lens: EMPTY_LENS });
  assert.deepEqual(
    viewFromHash({ diagram: "projects", depth: "2" }).lens,
    EMPTY_LENS,
    "depth without focus drops",
  );
});

test("the subtitle says what the server's Lens.describe says", () => {
  assert.equal(describeLens(EMPTY_LENS), "");
  assert.equal(
    describeLens({ scope: ["a", "b"], focus: "x", depth: null }),
    "scope a, b; focus x, depth 1",
  );
});

// chain is app -> lib -> core -> base, plus tools, as the handler test's chainWorkspace.
const chain: Declaration = {
  nodes: [
    { id: "app", anchor: "app", label: "app" },
    { id: "libs-lib", anchor: "libs/lib", label: "lib" },
    { id: "libs-core", anchor: "libs/core", label: "core" },
    { id: "libs-base", anchor: "libs/base", label: "base" },
    { id: "tools", anchor: "tools", label: "tools" },
  ],
  edges: [
    ["app", "libs-lib"],
    ["libs-lib", "libs-core"],
    ["libs-core", "libs-base"],
  ],
};

const anchors = (d: Declaration): string[] => d.nodes.map((n) => n.anchor);

test("the cut walks both directions from the focus, as the server's does", () => {
  const one = cutDeclaration(chain, { scope: [], focus: "libs/lib", depth: 1 }).value;
  assert.ok(one);
  assert.deepEqual(anchors(one), ["app", "libs/lib", "libs/core"]);
  assert.deepEqual(one.edges, [
    ["app", "libs-lib"],
    ["libs-lib", "libs-core"],
  ]);

  const alone = cutDeclaration(chain, { scope: [], focus: "libs/lib", depth: 0 }).value;
  assert.ok(alone);
  assert.deepEqual(anchors(alone), ["libs/lib"]);
  assert.deepEqual(alone.edges, []);

  const scoped = cutDeclaration(chain, { scope: ["libs/"], focus: "", depth: null }).value;
  assert.ok(scoped);
  assert.deepEqual(anchors(scoped), ["libs/lib", "libs/core", "libs/base"]);
  assert.equal(scoped.edges.length, 2, "the app -> lib edge leaves with app");
});

test("scope drops a node before the walk can reach through it", () => {
  const cut = cutDeclaration(chain, { scope: ["libs"], focus: "libs-lib", depth: 2 }).value;
  assert.ok(cut);
  assert.deepEqual(anchors(cut), ["libs/lib", "libs/core", "libs/base"]);
});

test("the cut refuses what the server refuses", () => {
  assert.equal(
    cutDeclaration(chain, { scope: [], focus: "", depth: 2 }).error?.message,
    "depth needs a focus",
  );
  assert.ok(cutDeclaration(chain, { scope: [], focus: "nowhere", depth: null }).error);
  assert.ok(cutDeclaration(chain, { scope: ["tools"], focus: "app", depth: null }).error);
});
