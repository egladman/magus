// api.test.ts - the diagrams client's error mapping and decoding. Every non-figure answer comes
// back as a read the view can put in words; none of them may decode as an empty figure.

import assert from "node:assert/strict";
import { test } from "node:test";
import {
  diagramUrl,
  listDiagrams,
  parseListing,
  parseRendered,
  parseSources,
  readFailure,
  renderDiagram,
} from "./api";

const HOST = "127.0.0.1:7391";

// respond builds a fetch that answers every request with one response and records the URLs.
function respond(status: number, body: string, seen: string[] = []): typeof fetch {
  return (async (input: string | URL | Request) => {
    seen.push(String(input));
    return new Response(body, {
      status,
      headers: { "content-type": status < 300 ? "application/json" : "text/plain" },
    });
  }) as typeof fetch;
}

test("each refusal keeps the server's sentence and its own kind", () => {
  const finding =
    'diagram "projects": 10 nodes exceeds the budget of 9; split into overview plus detail\n';
  assert.deepEqual(readFailure(422, finding, "diagram projects"), {
    kind: "refused",
    detail: finding.trim(),
  });
  assert.deepEqual(
    readFailure(409, "diagram: the import graph is not indexed; run magus graph build", "x"),
    {
      kind: "unindexed",
      detail: "diagram: the import graph is not indexed; run magus graph build",
    },
  );
  assert.deepEqual(readFailure(400, "diagram: depth needs a focus", "x"), {
    kind: "bad-lens",
    detail: "diagram: depth needs a focus",
  });
  assert.equal(readFailure(404, "404 page not found", "diagram nope").kind, "absent");
  assert.deepEqual(readFailure(500, "", "diagram x"), {
    kind: "unreadable",
    detail: "The server answered HTTP 500 for diagram x.",
  });
  assert.deepEqual(readFailure(422, "  ", "diagram x"), {
    kind: "refused",
    detail: "The server refused to draw diagram x.",
  });
});

test("a figure's URL carries its id and lens", () => {
  assert.equal(
    diagramUrl(HOST, "targets:libs/lib", { scope: ["libs"], focus: "", depth: null }),
    "http://127.0.0.1:7391/api/v1/diagrams/targets:libs/lib?scope=libs",
  );
});

test("a 422 comes back as refused with the finding, never as an empty figure", async () => {
  const seen: string[] = [];
  const read = await renderDiagram(
    { host: HOST, fetch: respond(422, "10 nodes exceeds the budget of 9", seen) },
    "projects",
    { scope: [], focus: "", depth: null },
  );
  assert.deepEqual(read, { kind: "refused", detail: "10 nodes exceeds the budget of 9" });
  assert.deepEqual(seen, ["http://127.0.0.1:7391/api/v1/diagrams/projects"]);
});

test("a network failure is unreadable and an abort is not a failure", async () => {
  const refused = (async () => {
    throw new TypeError("Failed to fetch");
  }) as typeof fetch;
  assert.deepEqual(await listDiagrams({ host: HOST, fetch: refused }), {
    kind: "unreadable",
    detail: "Failed to fetch",
  });
  const aborted = (async () => {
    throw new DOMException("The operation was aborted.", "AbortError");
  }) as typeof fetch;
  assert.deepEqual(await listDiagrams({ host: HOST, fetch: aborted }), { kind: "aborted" });
});

test("a body that is not the listing is unreadable, with what was wrong", async () => {
  const read = await listDiagrams({ host: HOST, fetch: respond(200, '{"figures":[]}') });
  assert.equal(read.kind, "unreadable");
  assert.match(read.kind === "unreadable" ? read.detail : "", /no diagrams array/);
});

test("the listing decodes its optional fields only when present", () => {
  assert.deepEqual(
    parseListing({
      diagrams: [
        { id: "projects", kind: "projects", title: "Workspace projects" },
        { id: "targets:app", kind: "targets", title: "Targets in app", project: "app" },
        { id: "imports", kind: "imports", title: "Package imports", indexed: false },
      ],
    }),
    [
      { id: "projects", kind: "projects", title: "Workspace projects" },
      { id: "targets:app", kind: "targets", title: "Targets in app", project: "app" },
      { id: "imports", kind: "imports", title: "Package imports", indexed: false },
    ],
  );
  assert.throws(() => parseListing({ diagrams: [{ id: 1 }] }), /id is not a string/);
});

test("a rendered figure decodes source_url and tolerates missing node fields", () => {
  assert.deepEqual(
    parseRendered({
      id: "projects",
      title: "Workspace projects",
      svg: "<svg/>",
      nodes: [{ id: "app", anchor: "app", label: "app" }, { id: "x" }],
      source_url: "https://github.com/acme/widgets/blob/abc/{path}#L{line}",
    }),
    {
      id: "projects",
      title: "Workspace projects",
      svg: "<svg/>",
      nodes: [
        { id: "app", anchor: "app", label: "app" },
        { id: "x", anchor: "", label: "x" },
      ],
      sourceUrl: "https://github.com/acme/widgets/blob/abc/{path}#L{line}",
    },
  );
  assert.throws(() => parseRendered({ id: "p", title: "t", nodes: [] }), /svg is not a string/);
});

test("the figure module source decodes as path to text", () => {
  assert.deepEqual(parseSources({ files: { "libs/figure/figure.buzz": "namespace figure;" } }), {
    "libs/figure/figure.buzz": "namespace figure;",
  });
  assert.throws(() => parseSources({ files: { "a.buzz": 3 } }), /a\.buzz is not a string/);
});
