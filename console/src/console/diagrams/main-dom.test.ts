// main-dom.test.ts - the Diagrams surface mounted against a fake server, and the runtime's
// program. Pinned here:
//
//   - THE STATIC RENDER IS THE PAGE. The server's SVG is inline, linked and listed as soon as it
//     arrives, with no runtime loaded.
//   - A REFUSAL IS WORDS, NOT AN EMPTY FIGURE. 422 and 409 bodies become the inline notice.
//   - THE LENS IS ADDRESSABLE. Applying one re-requests the figure and writes the fragment.
//   - THE RUNTIME IS EXPLICIT. Nothing loads it but its control; once loaded, a lens change lays
//     out in the page from the declaration the server served, with no second render request.
//   - THE DRIVER IS THE HANDLER'S. magus/figure's one source plus a driver shaped like
//     internal/handler/diagram's: Dir records for the import figure, actors for the rest.

import assert from "node:assert/strict";
import { afterEach, beforeEach, describe, test } from "node:test";
import { setDefaultHost } from "../../lib/settings";
import { activate } from "./main";
import {
  anchorTemplate,
  buzzString,
  drawnNodes,
  driverFor,
  figureId,
  linkTo,
  parseRelayout,
  programFor,
  FIGURE_PATH,
  IMPORTS,
} from "./wasm";

const HOST = "127.0.0.1:7391";
const BLOB = "https://github.com/acme/widgets/blob/abc/";
const realFetch = globalThis.fetch;

// magus/figure draws a served projects figure as actors, keyed external:<name> and linked by
// the handler's anchorHref.
function actor(name: string, path: string, x: number, y: number): string {
  return (
    '<a href="' +
    BLOB +
    path +
    '" data-node="external:' +
    name +
    '"><g><rect x="' +
    x +
    '" y="' +
    y +
    '" width="160" height="56"/></g></a>'
  );
}

const SVG =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 480 280" width="480" height="280" role="img">' +
  '<path data-edge="external:app->external:lib" d="M 0 0 L 1 1"/>' +
  actor("app", "app", 32, 20) +
  actor("lib", "libs/lib", 32, 120) +
  actor("tools", "tools", 288, 20) +
  "</svg>";

const RELAID =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 240 200">' +
  '<path data-edge="external:app->external:lib" d="M 0 0 L 1 1"/>' +
  actor("app", "app", 32, 20) +
  actor("lib", "libs/lib", 32, 120) +
  "</svg>";

// An import figure boxes directories, keyed by path.
const IMPORTS_SVG =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 480 200">' +
  '<path data-edge="internal/a->internal/b" d="M 0 0 L 1 1"/>' +
  '<g data-node="internal/a" data-anchor="internal/a"><rect x="32" y="20" width="160" height="56"/></g>' +
  '<g data-node="internal/b" data-anchor="internal/b"><rect x="288" y="20" width="160" height="56"/></g>' +
  '<g data-node="internal/c" data-anchor="internal/c"><rect x="32" y="120" width="160" height="56"/></g>' +
  "</svg>";

const FIGURE_SRC = [
  "namespace figure;",
  'import "std";',
  'import "assert";',
  "export fun of(id: str) > mut Figure { return mut Figure{ id = id }; }",
].join("\n");

let requests: string[] = [];
let importsIndexed = false;

function serve(): void {
  globalThis.fetch = (async (input: string | URL | Request) => {
    const url = String(input);
    requests.push(url.replace("http://" + HOST, ""));
    const path = new URL(url).pathname;
    const json = (v: unknown): Response =>
      new Response(JSON.stringify(v), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    if (path === "/api/v1/diagrams")
      return json({
        diagrams: [
          { id: "projects", kind: "projects", title: "Workspace projects" },
          { id: "imports", kind: "imports", title: "Package imports", indexed: importsIndexed },
          { id: "big", kind: "projects", title: "Too big" },
        ],
      });
    if (path === "/api/v1/diagrams/source") return json({ files: { [FIGURE_PATH]: FIGURE_SRC } });
    if (path === "/api/v1/diagrams/imports")
      return importsIndexed
        ? json({
            id: "imports",
            title: "Package imports",
            svg: IMPORTS_SVG,
            nodes: [
              { id: "internal-a", anchor: "internal/a", label: "internal/a" },
              { id: "internal-b", anchor: "internal/b", label: "internal/b" },
              { id: "internal-c", anchor: "internal/c", label: "internal/c" },
            ],
            source_url: BLOB + "{path}#L{line}",
          })
        : new Response("diagram: the import graph is not indexed; run magus graph build\n", {
            status: 409,
          });
    if (path === "/api/v1/diagrams/big")
      return new Response(
        'diagram "big": 12 nodes exceeds the budget of 9; split into overview plus detail\n',
        {
          status: 422,
        },
      );
    if (path === "/api/v1/diagrams/projects")
      return json({
        id: "projects",
        title: "Workspace projects",
        svg: SVG,
        nodes: [
          { id: "app", anchor: "app", label: "app" },
          { id: "libs-lib", anchor: "libs/lib", label: "lib" },
          { id: "tools", anchor: "tools", label: "tools" },
        ],
        source_url: BLOB + "{path}#L{line}",
      });
    return new Response("404 page not found", { status: 404 });
  }) as typeof fetch;
}

async function settle(turns = 12): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

// The hooks live in a suite: the dom tests share one process, where a top-level hook would run
// around every other file's tests too (and theirs around ours, which is why these run after).
describe("the Diagrams surface", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    document.body.replaceChildren();
    document.documentElement.dataset.motion = "reduced";
    location.hash = "";
    requests = [];
    importsIndexed = false;
    setDefaultHost(HOST);
    serve();
  });

  afterEach(() => {
    setDefaultHost("");
    globalThis.fetch = realFetch;
    delete (globalThis as { buzz?: unknown }).buzz;
    delete document.documentElement.dataset.motion;
  });

  function mountSurface() {
    const host = document.createElement("div");
    document.body.append(host);
    const instance = activate(host);
    const q = <T extends Element>(sel: string): T => {
      const el = host.querySelector<T>(sel);
      assert.ok(el, sel);
      return el;
    };
    return { host, instance, q };
  }

  function loadRuntime(host: HTMLElement): void {
    const runtime = [...host.querySelectorAll("button")].find(
      (b) => b.textContent === "Load interactive runtime",
    );
    assert.ok(runtime);
    runtime.click();
  }

  test("the server's figure is inline, linked and listed, with no runtime loaded", async () => {
    const { host, instance, q } = mountSurface();
    await settle();
    assert.deepEqual(requests, ["/api/v1/diagrams", "/api/v1/diagrams/projects"]);
    const svg = q<SVGSVGElement>(".console-diagrams__frame svg");
    assert.equal(svg.getAttribute("role"), "graphics-document");
    assert.equal(
      svg.querySelector('[data-node="external:lib"]')?.getAttribute("href"),
      BLOB + "libs/lib",
    );
    assert.deepEqual(
      [...host.querySelectorAll(".console-diagrams__node button")].map((b) => b.textContent),
      ["app", "lib", "tools"],
    );
    assert.deepEqual(
      [...host.querySelectorAll<HTMLElement>(".console-diagrams__node")].map(
        (li) => li.dataset.nodeId,
      ),
      ["external:app", "external:lib", "external:tools"],
      "the list is keyed by the ids the figure draws",
    );
    assert.equal(q(".console-diagrams__caption").textContent, "Workspace projects");
    assert.equal((globalThis as { buzz?: unknown }).buzz, undefined, "nothing loaded the runtime");
    // The controls are a row of their own, never inside the figure.
    assert.equal(q(".console-diagrams__frame").querySelector("button"), null);
    instance.deactivate();
  });

  test("a 422 is the server's finding in an inline notice and no stale figure", async () => {
    const { q, instance } = mountSurface();
    await settle();
    const picker = q<HTMLSelectElement>("select");
    picker.value = "big";
    picker.dispatchEvent(new Event("change"));
    await settle();
    const note = q(".console-diagrams__notice");
    assert.match(
      note.textContent ?? "",
      /12 nodes exceeds the budget of 9; split into overview plus detail/,
    );
    assert.equal(q(".console-diagrams__frame").querySelector("svg"), null);
    assert.match(location.hash, /diagram=big/);
    instance.deactivate();
  });

  test("a 409 names the command that builds the index", async () => {
    location.hash = "#diagram=imports";
    const { q, instance } = mountSurface();
    await settle();
    assert.match(q(".console-diagrams__notice").textContent ?? "", /run magus graph build/);
    instance.deactivate();
  });

  test("the lens form re-requests the figure and the fragment carries the view", async () => {
    const { q, instance } = mountSurface();
    await settle();
    q<HTMLInputElement>('input[name="focus"]').value = "app";
    q<HTMLInputElement>('input[name="depth"]').value = "1";
    q<HTMLFormElement>("form").dispatchEvent(new Event("submit", { cancelable: true }));
    await settle();
    assert.equal(requests.at(-1), "/api/v1/diagrams/projects?focus=app&depth=1");
    assert.match(location.hash, /diagram=projects&focus=app&depth=1/);
    instance.deactivate();
  });

  test("an incomplete lens is refused in the form, before any request", async () => {
    const { q, instance } = mountSurface();
    await settle();
    const before = requests.length;
    q<HTMLInputElement>('input[name="depth"]').value = "2";
    q<HTMLFormElement>("form").dispatchEvent(new Event("submit", { cancelable: true }));
    await settle();
    assert.equal(requests.length, before);
    assert.match(q(".console-diagrams__notice").textContent ?? "", /Depth needs a focus/);
    instance.deactivate();
  });

  test("once loaded, the runtime lays a lens out in the page without asking the server", async () => {
    const programs: string[] = [];
    (globalThis as { buzz?: unknown }).buzz = {
      evalBuzz: (src: string) => {
        programs.push(src);
        return { ok: true, result: "svg\n" + RELAID, output: "", diag: null };
      },
    };
    const { host, q, instance } = mountSurface();
    await settle();
    loadRuntime(host);
    await settle();
    assert.equal(requests.at(-1), "/api/v1/diagrams/source");
    assert.match(q(".console-diagrams__runtime-status").textContent ?? "", /Runtime loaded/);

    const before = requests.length;
    q<HTMLInputElement>('input[name="focus"]').value = "app";
    q<HTMLFormElement>("form").dispatchEvent(new Event("submit", { cancelable: true }));
    await settle();
    assert.equal(requests.length, before, "no round trip");
    assert.equal(programs.length, 1);
    const program = programs[0];
    assert.ok(program.startsWith(FIGURE_SRC + "\n"), "the module's own source, then the driver");
    assert.match(program, /final f = of\("projects"\)\.title\("Workspace projects"\)/);
    assert.match(
      program,
      /f\.unscoped\(why: "served from the workspace graph: focus app, depth 1"\);/,
    );
    assert.match(
      program,
      /final a0 = external\("app", link: "https:\/\/github\.com\/acme\/widgets\/blob\/abc\/app", look: Look\.plain\);\n {4}f\.actor\(a0\);/,
    );
    assert.match(
      program,
      /final a1 = external\("lib", link: "[^"]*\/libs\/lib", look: Look\.plain\);/,
    );
    assert.doesNotMatch(program, /"tools"/, "the lens cut tools before layout");
    assert.match(program, /f\.flowAcross\(a0, dst: a1\);/, "edges come from data-edge");
    assert.match(
      program,
      /return f\.svg\(Theme\.page, anchorHref: "https:\/\/github\.com\/acme\/widgets\/blob\/abc\/\\\{path\\\}"\);/,
    );
    const drawn = [...host.querySelectorAll(".console-diagrams__frame [data-node]")].map((n) =>
      n.getAttribute("data-node"),
    );
    assert.deepEqual(drawn, ["external:app", "external:lib"]);
    assert.deepEqual(
      [...host.querySelectorAll(".console-diagrams__node button")].map((b) => b.textContent),
      ["app", "lib"],
    );
    instance.deactivate();
  });

  test("an import figure lays out in the page from Dir records", async () => {
    importsIndexed = true;
    location.hash = "#diagram=imports";
    const programs: string[] = [];
    (globalThis as { buzz?: unknown }).buzz = {
      evalBuzz: (src: string) => {
        programs.push(src);
        return { ok: true, result: "svg\n" + IMPORTS_SVG, output: "", diag: null };
      },
    };
    const { host, q, instance } = mountSurface();
    await settle();
    loadRuntime(host);
    await settle();
    q<HTMLInputElement>('input[name="scope"]').value = "internal/a, internal/b";
    q<HTMLFormElement>("form").dispatchEvent(new Event("submit", { cancelable: true }));
    await settle();
    assert.equal(programs.length, 1);
    const program = programs[0];
    assert.match(program, /fun serveDir\(path: str, imports: \[str\]\) > magus\\Dir \{/);
    assert.match(
      program,
      /f\.box\(serveDir\("internal\/a", imports: \["internal\/b"\]\), label: "internal\/a"\);/,
    );
    assert.match(
      program,
      /f\.box\(serveDir\("internal\/b", imports: \[<str>\]\), label: "internal\/b"\);/,
    );
    assert.doesNotMatch(program, /internal\/c/, "the scope cut c before layout");
    assert.match(program, /f\.edgesFromGraph\(\);/);
    assert.doesNotMatch(program, /external\(|unscoped/);
    instance.deactivate();
  });

  test("a runtime refusal is shown like the server's", async () => {
    (globalThis as { buzz?: unknown }).buzz = {
      evalBuzz: () => ({
        ok: true,
        result: "findings\ndiagram: over budget",
        output: "",
        diag: null,
      }),
    };
    const { host, q, instance } = mountSurface();
    await settle();
    loadRuntime(host);
    await settle();
    q<HTMLInputElement>('input[name="scope"]').value = "libs";
    q<HTMLFormElement>("form").dispatchEvent(new Event("submit", { cancelable: true }));
    await settle();
    assert.match(q(".console-diagrams__notice").textContent ?? "", /over budget/);
    instance.deactivate();
  });
});

// ---- the runtime's program ------------------------------------------------------------------

test("buzzString escapes exactly as the handler's buzzString does", () => {
  // The same case as internal/handler/diagram/render_test.go's TestDiagramBuzzStringEscapes.
  assert.equal(buzzString('a"b\\c{d}\n\t\x07'), '"a\\"b\\\\c\\{d\\}\\n\\t\\007"');
});

test("figure ids are the handler's sanitized ids", () => {
  assert.equal(figureId("targets:libs/lib"), "targets-libs-lib");
  assert.equal(figureId("..."), "root");
});

test("anchor links are the handler's: no line fragment, {path} filled once", () => {
  assert.equal(anchorTemplate(BLOB + "{path}#L{line}"), BLOB + "{path}");
  assert.equal(anchorTemplate(""), "");
  assert.equal(linkTo(BLOB + "{path}", "libs/lib"), BLOB + "libs/lib");
  assert.equal(linkTo(BLOB + "{path}", "odd/{line}"), BLOB + "odd/{line}");
  assert.equal(linkTo("", "libs/lib"), "");
});

test("server rows take the ids the figure draws", () => {
  const rows = [
    { id: "libs-core", anchor: "libs/core", label: "core" },
    { id: "app-core", anchor: "app/core", label: "core" },
    { id: "tools", anchor: "tools", label: "tools" },
  ];
  assert.deepEqual(
    drawnNodes(rows, "flow").map((n) => n.id),
    ["external:core (libs/core)", "external:core (app/core)", "external:tools"],
  );
  assert.deepEqual(
    drawnNodes(rows, IMPORTS).map((n) => n.id),
    ["libs/core", "app/core", "tools"],
  );
});

test("the program is the module's own source, then the driver", () => {
  assert.equal(
    programFor({ [FIGURE_PATH]: FIGURE_SRC }, "return 1;\n"),
    FIGURE_SRC + "\nreturn 1;\n",
  );
  assert.throws(
    () => programFor({ "libs/diagram/flow.buzz": "" }, ""),
    /missing libs\/figure\/figure\.buzz/,
  );
});

test("the actor driver quotes every value and returns one marked string", () => {
  const driver = driverFor(
    {
      nodes: [
        { id: "external:a", anchor: "x/y", label: 'say "{hi}"' },
        { id: "external:b", anchor: "z", label: "b" },
      ],
      edges: [["external:a", "external:b"]],
    },
    { id: "targets:app", title: "T", claim: "flow", anchorHref: "" },
    "scope x",
  );
  assert.match(driver, /final f = of\("targets-app"\)\.title\("T"\)\.desc\("scope x"\);/);
  assert.match(driver, /external\("say \\"\\\{hi\\\}\\"", link: "", look: Look\.plain\)/);
  assert.match(driver, /f\.flowAcross\(a0, dst: a1\);/);
  assert.match(driver, /return f\.svg\(Theme\.page, anchorHref: ""\);/);
  assert.match(driver, /var served = "svg\\n";/);
  assert.match(driver, /return served;\n$/);
  assert.throws(
    () =>
      driverFor(
        { nodes: [{ id: "external:a", anchor: "a", label: "a" }], edges: [["external:a", "gone"]] },
        { id: "projects", title: "P", claim: "flow", anchorHref: "" },
        "",
      ),
    /edge external:a->gone to no node/,
  );
});

test("a relayout result is a figure, a refusal or a failure", () => {
  assert.deepEqual(parseRelayout({ ok: true, result: "svg\n<svg/>" }), {
    kind: "ok",
    svg: "<svg/>",
  });
  assert.deepEqual(parseRelayout({ ok: true, result: "findings\nover" }), {
    kind: "refused",
    detail: "over",
  });
  assert.deepEqual(parseRelayout({ ok: false, diag: { msg: "boom", line: 3, col: 1 } }), {
    kind: "failed",
    detail: "boom (line 3)",
  });
  assert.deepEqual(parseRelayout({ ok: true, result: "[a, b]" }), {
    kind: "failed",
    detail: "the driver returned something that is not a figure",
  });
});
