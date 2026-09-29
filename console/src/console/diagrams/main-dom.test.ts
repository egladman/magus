// main-dom.test.ts - the Diagrams surface mounted against a fake server, and the runtime's
// program assembly. Pinned here:
//
//   - THE STATIC RENDER IS THE PAGE. The server's SVG is inline, linked and listed as soon as it
//     arrives, with no runtime loaded.
//   - A REFUSAL IS WORDS, NOT AN EMPTY FIGURE. 422 and 409 bodies become the inline notice.
//   - THE LENS IS ADDRESSABLE. Applying one re-requests the figure and writes the fragment.
//   - THE RUNTIME IS EXPLICIT. Nothing loads it but its control; once loaded, a lens change lays
//     out in the page from the declaration the server served, with no second render request.

import assert from "node:assert/strict";
import { afterEach, beforeEach, describe, test } from "node:test";
import { setDefaultHost } from "../../lib/settings";
import { activate } from "./main";
import {
  assembleProgram,
  buzzString,
  driverFor,
  flowId,
  parseRelayout,
  stripModule,
  FLOW_PATH,
  RENDERER_PATH,
} from "./wasm";

const HOST = "127.0.0.1:7391";
const realFetch = globalThis.fetch;

const SVG =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 480 280" width="480" height="280" role="img">' +
  '<path data-edge="app->libs-lib" d="M 0 0 L 1 1"/>' +
  '<g data-node="app" data-anchor="app"><rect x="32" y="20" width="160" height="56"/></g>' +
  '<g data-node="libs-lib" data-anchor="libs/lib"><rect x="32" y="120" width="160" height="56"/></g>' +
  '<g data-node="tools" data-anchor="tools"><rect x="288" y="20" width="160" height="56"/></g>' +
  "</svg>";

const RELAID =
  '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 240 200"><path data-edge="app->libs-lib" d="M 0 0 L 1 1"/>' +
  '<g data-node="app" data-anchor="app"><rect x="32" y="20" width="160" height="56"/></g>' +
  '<g data-node="libs-lib" data-anchor="libs/lib"><rect x="32" y="120" width="160" height="56"/></g></svg>';

const FLOW_SRC = [
  "// flow.buzz",
  "namespace flow;",
  'import "std";',
  'import "libs/diagram/diagram" as _;',
  "fun imin(a: int, b: int) > int { return a; }",
  "export fun flow(id: str) > Flow { return Flow{}; }",
  'test "flow lays out" {',
  "    std\\assert(true);",
  "}",
].join("\n");

const RENDERER_SRC = [
  "namespace diagram;",
  'import "std";',
  'import "assert";',
  "fun imin(a: int, b: int) > int { if (a < b) { return a; } return b; }",
  "fun clamp(n: int) > int { return imin(n, b: 4); }",
  "export fun cssVarPalette() > Palette { return Palette{}; }",
  'test "renderer" { assert\\equal(1, 1, "one"); }',
].join("\n");

let requests: string[] = [];

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
          { id: "imports", kind: "imports", title: "Package imports", indexed: false },
          { id: "big", kind: "projects", title: "Too big" },
        ],
      });
    if (path === "/api/v1/diagrams/source")
      return json({ files: { [FLOW_PATH]: FLOW_SRC, [RENDERER_PATH]: RENDERER_SRC } });
    if (path === "/api/v1/diagrams/imports")
      return new Response("diagram: the import graph is not indexed; run magus graph build\n", {
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
        source_url: "https://github.com/acme/widgets/blob/abc/{path}#L{line}",
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

  test("the server's figure is inline, linked and listed, with no runtime loaded", async () => {
    const { host, instance, q } = mountSurface();
    await settle();
    assert.deepEqual(requests, ["/api/v1/diagrams", "/api/v1/diagrams/projects"]);
    const svg = q<SVGSVGElement>(".console-diagrams__frame svg");
    assert.equal(svg.getAttribute("role"), "graphics-document");
    assert.equal(
      svg.querySelector('[data-node="libs-lib"]')?.getAttribute("href"),
      "https://github.com/acme/widgets/blob/abc/libs/lib",
    );
    assert.deepEqual(
      [...host.querySelectorAll(".console-diagrams__node button")].map((b) => b.textContent),
      ["app", "lib", "tools"],
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
    const runtime = [...host.querySelectorAll("button")].find(
      (b) => b.textContent === "Load interactive runtime",
    );
    assert.ok(runtime);
    runtime.click();
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
    assert.match(program, /f\.node\("app", label: "app", anchor: "app"\);/);
    assert.match(program, /f\.node\("libs-lib", label: "lib", anchor: "libs\/lib"\);/);
    assert.doesNotMatch(program, /f\.node\("tools"/, "the lens cut tools before layout");
    assert.match(
      program,
      /f\.edge\("app", dst: "libs-lib", claim: "flow"\);/,
      "edges come from data-edge",
    );
    const drawn = [...host.querySelectorAll(".console-diagrams__frame [data-node]")].map((n) =>
      n.getAttribute("data-node"),
    );
    assert.deepEqual(drawn, ["app", "libs-lib"]);
    assert.deepEqual(
      [...host.querySelectorAll(".console-diagrams__node button")].map((b) => b.textContent),
      ["app", "lib"],
    );
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
    [...host.querySelectorAll("button")]
      .find((b) => b.textContent === "Load interactive runtime")
      ?.click();
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

test("flow ids are the handler's sanitized ids", () => {
  assert.equal(flowId("targets:libs/lib"), "targets-libs-lib");
  assert.equal(flowId("..."), "root");
});

test("a module becomes program text: no namespace, no imports, no tests", () => {
  const out = stripModule(FLOW_SRC);
  assert.doesNotMatch(out, /namespace|import|test "/);
  assert.match(out, /export fun flow/);
  assert.doesNotMatch(stripModule(RENDERER_SRC), /assert\\equal/);
});

test("the program inlines the renderer and renames the helpers both files define", () => {
  const program = assembleProgram(
    { [FLOW_PATH]: FLOW_SRC, [RENDERER_PATH]: RENDERER_SRC },
    "return 1;\n",
  );
  assert.equal(program.match(/import "std";/g)?.length, 1);
  assert.match(program, /fun diagram_imin\(a: int, b: int\)/);
  assert.match(
    program,
    /return diagram_imin\(n, b: 4\)/,
    "the renderer's own call follows the rename",
  );
  assert.match(program, /fun imin\(a: int, b: int\) > int \{ return a; \}/, "flow keeps its own");
  assert.ok(
    program.indexOf("cssVarPalette") < program.indexOf("export fun flow"),
    "the renderer comes first",
  );
  assert.throws(
    () => assembleProgram({ [FLOW_PATH]: FLOW_SRC }, ""),
    /missing libs\/diagram\/diagram\.buzz/,
  );
});

test("the driver declares the cut and returns one marked string", () => {
  const driver = driverFor(
    { nodes: [{ id: "a", anchor: "x/y", label: 'say "{hi}"' }], edges: [["a", "a"]] },
    { id: "targets:app", title: "T", claim: "imports", anchorHref: "" },
    "scope x",
  );
  assert.match(driver, /flow\("targets-app"\)\.title\("T"\)\.desc\("scope x"\)/);
  assert.match(driver, /f\.node\("a", label: "say \\"\\\{hi\\\}\\"", anchor: "x\/y"\);/);
  assert.match(driver, /claim: "imports"/);
  assert.match(driver, /return f\.svg\(cssVarPalette\(\)\);/);
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
});
