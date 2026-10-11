// main-dom.test.ts - the Diagrams app mounted against a fake server, and the runtime's
// program. Pinned here:
//
//   - THE STATIC RENDER IS THE PAGE. The server's SVG is inline, linked and listed as soon as it
//     arrives, with no runtime loaded.
//   - A REFUSAL IS WORDS, NOT AN EMPTY FIGURE. 422 and 409 bodies become the inline notice.
//   - THE LENS IS ADDRESSABLE. Applying one re-requests the figure and writes the fragment.
//   - THE RUNTIME IS EXPLICIT. Nothing loads it but its control; once loaded, a lens change lays
//     out in the page from the declaration the server served, with no second render request.
//   - THE RECORD IS THE HANDLER'S. The page hands figure\draw the Figure record
//     internal/handler/diagram's buildFigure builds: Dir boxes for the import figure, actors for
//     the rest. No Buzz source is written.

import assert from "node:assert/strict";
import { afterEach, beforeEach, describe, test } from "node:test";
import { setDefaultHost } from "../../../lib/settings";
import { activate } from "./main";
import {
  anchorTemplate,
  drawnNodes,
  buildFigure,
  figureId,
  linkTo,
  toRelayout,
  runtimeFrom,
  IMPORTS,
  type DrawResult,
  type Figure,
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

// fakeRuntime stands in for buzz.wasm's globalThis.buzz: it records each figure record the page
// hands drawFigure, decoded from the JSON that crosses into Go, and answers with result.
function fakeRuntime(result: DrawResult): { figures: Figure[]; hrefs: string[] } {
  const figures: Figure[] = [];
  const hrefs: string[] = [];
  (globalThis as { buzz?: unknown }).buzz = {
    drawFigure: (json: string, anchorHref: string) => {
      figures.push(JSON.parse(json) as Figure);
      hrefs.push(anchorHref);
      return result;
    },
  };
  return { figures, hrefs };
}

const drew = (svg: string): DrawResult => ({ ok: true, svg, findings: "", diag: null });

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
describe("the Diagrams app", () => {
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

  function mountApp() {
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
    const { host, instance, q } = mountApp();
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
    const { q, instance } = mountApp();
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
    const { q, instance } = mountApp();
    await settle();
    assert.match(q(".console-diagrams__notice").textContent ?? "", /run magus graph build/);
    instance.deactivate();
  });

  test("the lens form re-requests the figure and the fragment carries the view", async () => {
    const { q, instance } = mountApp();
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
    const { q, instance } = mountApp();
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
    const rt = fakeRuntime(drew(RELAID));
    const { host, q, instance } = mountApp();
    await settle();
    const loaded = requests.length;
    loadRuntime(host);
    await settle();
    assert.equal(requests.length, loaded, "the runtime embeds magus/figure; nothing is fetched");
    assert.match(q(".console-diagrams__runtime-status").textContent ?? "", /Runtime loaded/);

    const before = requests.length;
    q<HTMLInputElement>('input[name="focus"]').value = "app";
    q<HTMLFormElement>("form").dispatchEvent(new Event("submit", { cancelable: true }));
    await settle();
    assert.equal(requests.length, before, "no round trip");
    assert.equal(rt.figures.length, 1);
    const app = { name: "app", sub: "", tag: "", link: BLOB + "app", look: "plain" };
    const lib = { name: "lib", sub: "", tag: "", link: BLOB + "libs/lib", look: "plain" };
    const box = { dir: null, group: null, label: "", sub: "", tag: "", focal: false, look: null };
    assert.deepEqual(rt.figures[0], {
      id: "projects",
      title: "Workspace projects",
      eyebrow: "",
      desc: "focus app, depth 1",
      direction: "across",
      generated: false,
      unscopedWhy: "served from the workspace graph: focus app, depth 1",
      graphEdges: false,
      boxes: [
        { ...box, actor: app },
        { ...box, actor: lib },
      ],
      scopes: [],
      exclusions: [],
      hiddenEdges: [],
      edgeMarks: [],
      flows: [
        {
          src: { dir: null, actor: app },
          dst: { dir: null, actor: lib },
          label: "",
          stroke: null,
        },
      ],
      zones: [],
      alignments: [],
      legends: [],
    });
    assert.deepEqual(rt.hrefs, [BLOB + "{path}"]);
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
    const rt = fakeRuntime(drew(IMPORTS_SVG));
    const { host, q, instance } = mountApp();
    await settle();
    loadRuntime(host);
    await settle();
    q<HTMLInputElement>('input[name="scope"]').value = "internal/a, internal/b";
    q<HTMLFormElement>("form").dispatchEvent(new Event("submit", { cancelable: true }));
    await settle();
    assert.equal(rt.figures.length, 1);
    const f = rt.figures[0];
    assert.equal(f.graphEdges, true);
    assert.equal(f.unscopedWhy, "");
    assert.deepEqual(
      f.boxes.map((b) => [b.label, b.dir?.path, b.dir?.imports, b.dir?.importsIndexed, b.actor]),
      [
        ["internal/a", "internal/a", ["internal/b"], true, null],
        ["internal/b", "internal/b", [], true, null],
      ],
      "the scope cut c before layout, and each box carries the imports the SVG drew",
    );
    assert.deepEqual(f.flows, []);
    instance.deactivate();
  });

  test("a runtime refusal is shown like the server's", async () => {
    fakeRuntime({ ok: false, svg: "", findings: "diagram: over budget", diag: null });
    const { host, q, instance } = mountApp();
    await settle();
    loadRuntime(host);
    await settle();
    q<HTMLInputElement>('input[name="scope"]').value = "libs";
    q<HTMLFormElement>("form").dispatchEvent(new Event("submit", { cancelable: true }));
    await settle();
    assert.match(q(".console-diagrams__notice").textContent ?? "", /over budget/);
    instance.deactivate();
  });

  describe("a #figure= link", () => {
    const linkHash = (doc: unknown): string =>
      "#figure=" + Buffer.from(JSON.stringify(doc), "utf8").toString("base64url");
    const DEPS = {
      v: 2,
      kind: "deps",
      title: "How far this change reaches",
      nodes: [
        { id: "libs/lib", label: "lib", seed: true },
        { id: "app", label: "app", seed: false },
      ],
      edges: [["libs/lib", "app"]],
    };

    test("is drawn from the fragment alone: no request, the runtime's figure, the seed marked", async () => {
      location.hash = linkHash(DEPS);
      const rt = fakeRuntime(drew(RELAID));
      const { host, q, instance } = mountApp();
      await settle();
      assert.deepEqual(requests, [], "a link needs no server");
      assert.equal(rt.figures.length, 1);
      assert.deepEqual(
        rt.figures[0].boxes.map((b) => [b.actor?.name, b.actor?.tag, b.actor?.look]),
        [
          ["lib", "edited", "focal"],
          ["app", "", "plain"],
        ],
      );
      assert.deepEqual(
        rt.figures[0].flows.map((f) => [f.src.actor?.name, f.dst.actor?.name]),
        [["lib", "app"]],
        "an edge runs dependency to dependent",
      );
      assert.equal(
        q<SVGSVGElement>(".console-diagrams__frame svg").getAttribute("role"),
        "graphics-document",
      );
      assert.equal(q(".console-diagrams__caption").textContent, "How far this change reaches");
      assert.deepEqual(
        [...host.querySelectorAll<HTMLElement>(".console-diagrams__node")].map(
          (li) => li.dataset.nodeId,
        ),
        ["external:lib", "external:app"],
      );
      assert.equal(q<HTMLElement>(".console-diagrams__bar").hidden, true);
      assert.equal(host.querySelector(".console-diagrams__notice"), null);
      instance.deactivate();
    });

    test("that cannot be read is an inline notice naming the problem", async () => {
      location.hash = linkHash({ ...DEPS, v: 1 });
      const rt = fakeRuntime(drew(RELAID));
      const { host, q, instance } = mountApp();
      await settle();
      const note = q(".console-diagrams__notice");
      assert.match(note.textContent ?? "", /could not be read/);
      assert.match(note.textContent ?? "", /version 1 and this console reads version 2/);
      assert.equal(rt.figures.length, 0);
      assert.equal(host.querySelector(".console-diagrams__frame svg"), null);
      instance.deactivate();
    });

    test("that is not base64url is an inline notice", async () => {
      location.hash = "#figure=!!not-a-link!!";
      const { q, instance } = mountApp();
      await settle();
      assert.match(q(".console-diagrams__notice").textContent ?? "", /not base64url/);
      instance.deactivate();
    });

    test("the runtime declines to draw is an inline notice with its finding", async () => {
      location.hash = linkHash(DEPS);
      fakeRuntime({ ok: false, svg: "", findings: "figure: over budget", diag: null });
      const { host, q, instance } = mountApp();
      await settle();
      assert.match(q(".console-diagrams__notice").textContent ?? "", /over budget/);
      assert.equal(host.querySelector(".console-diagrams__frame svg"), null);
      instance.deactivate();
    });

    test("replaced in the address bar is drawn again", async () => {
      location.hash = linkHash(DEPS);
      const rt = fakeRuntime(drew(RELAID));
      const { q, instance } = mountApp();
      await settle();
      location.hash = linkHash({ ...DEPS, title: "Another" });
      window.dispatchEvent(new HashChangeEvent("hashchange"));
      await settle();
      assert.equal(rt.figures.length, 2);
      assert.equal(q(".console-diagrams__caption").textContent, "Another");
      instance.deactivate();
    });
  });
});

// What a reader without the pointer and without the colours gets from the Figures page: controls that
// only enable when they have something to act on, a name and a description for the frame, a stand-in
// for a frame with nothing in it, and notices that are announced once.
describe("the Figures page, for a reader who cannot see it", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    document.body.replaceChildren();
    document.documentElement.dataset.motion = "reduced";
    location.hash = "";
    requests = [];
    importsIndexed = false;
    serve();
  });

  afterEach(() => {
    setDefaultHost("");
    globalThis.fetch = realFetch;
    delete document.documentElement.dataset.motion;
  });

  function mountApp() {
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

  const controlNames = [
    "Fit",
    "Zoom out",
    "Zoom in",
    "Focus",
    "Clear focus",
    "Load interactive runtime",
  ];
  function control(host: HTMLElement, name: string): HTMLButtonElement {
    const b = [
      ...host.querySelectorAll<HTMLButtonElement>(".console-diagrams__controls button"),
    ].find((x) => x.textContent === name);
    assert.ok(b, name);
    return b;
  }

  test("with no server there is an empty state naming the command, and nothing to press", async () => {
    setDefaultHost("");
    const { host, q, instance } = mountApp();
    await settle();
    assert.deepEqual(requests, []);
    const state = q<HTMLElement>(".console-diagrams__state");
    assert.equal(state.hidden, false);
    assert.ok(state.querySelector(".pf-v6-c-empty-state"), "a PF empty state");
    assert.equal(state.querySelector("code")?.textContent, "magus server start");
    assert.ok(!(state.textContent ?? "").includes("`"), "a command is a <code>, not backticks");
    assert.equal(q<HTMLElement>(".console-diagrams__frame").hidden, true);
    for (const name of controlNames) assert.equal(control(host, name).disabled, true, name);
    assert.equal(q<HTMLSelectElement>("select").disabled, true);
    assert.equal(q<HTMLInputElement>('input[name="focus"]').disabled, true);
    instance.deactivate();
  });

  test("a figure enables its controls and takes the stand-in away", async () => {
    setDefaultHost(HOST);
    const { host, q, instance } = mountApp();
    await settle();
    assert.equal(q<HTMLElement>(".console-diagrams__state").hidden, true);
    assert.equal(q<HTMLElement>(".console-diagrams__frame").hidden, false);
    for (const name of ["Fit", "Zoom out", "Zoom in", "Focus", "Load interactive runtime"])
      assert.equal(control(host, name).disabled, false, name);
    assert.equal(control(host, "Clear focus").disabled, true, "nothing is focused yet");
    assert.equal(q<HTMLInputElement>('input[name="focus"]').disabled, false);
    instance.deactivate();
  });

  test("the figure controls are a group, and the frame is named Figure with its keys described", async () => {
    setDefaultHost(HOST);
    const { q, instance } = mountApp();
    await settle();
    assert.equal(q(".console-diagrams__controls").getAttribute("role"), "group");
    const frame = q<HTMLElement>(".console-diagrams__frame");
    assert.equal(frame.getAttribute("aria-label"), "Figure");
    assert.match(frame.getAttribute("aria-keyshortcuts") ?? "", /\bf\b/);
    const described = document.getElementById(frame.getAttribute("aria-describedby") ?? "");
    assert.ok(described, "the keys live in a described-by element");
    assert.match(described.textContent ?? "", /Escape clears focus/);
    instance.deactivate();
  });

  test("the lens field that centers the drawing is not called Focus", async () => {
    setDefaultHost(HOST);
    const { host, instance } = mountApp();
    await settle();
    const labels = [...host.querySelectorAll(".console-diagrams__field-label")].map(
      (l) => l.textContent,
    );
    assert.deepEqual(labels, ["Figure", "Scope", "Center on", "Depth"]);
    instance.deactivate();
  });

  test("a refusal is announced once: the host is no live region and the alert carries the role", async () => {
    setDefaultHost(HOST);
    const { q, instance } = mountApp();
    await settle();
    const picker = q<HTMLSelectElement>("select");
    picker.value = "big";
    picker.dispatchEvent(new Event("change"));
    await settle();
    assert.equal(q(".console-diagrams__notices").hasAttribute("aria-live"), false);
    const note = q(".console-diagrams__notice");
    assert.equal(note.getAttribute("role"), "status");
    assert.match(note.textContent ?? "", /Warning alert/, "the severity is a word, not a hue");
    assert.match(q(".console-diagrams__state").textContent ?? "", /The notice above says why/);
    instance.deactivate();
  });

  test("while the first figure draws the frame holds a spinner, not a blank", async () => {
    setDefaultHost(HOST);
    let release: (r: Response) => void = () => {};
    const inner = globalThis.fetch;
    globalThis.fetch = (async (input: string | URL | Request) => {
      if (String(input).endsWith("/api/v1/diagrams/projects"))
        return new Promise<Response>((resolve) => {
          release = resolve;
        });
      return inner(input);
    }) as typeof fetch;
    const { q, instance } = mountApp();
    await settle();
    const state = q<HTMLElement>(".console-diagrams__state");
    assert.equal(state.hidden, false);
    assert.ok(state.querySelector(".pf-v6-c-spinner"), "a PF spinner");
    release(await inner("http://" + HOST + "/api/v1/diagrams/projects"));
    await settle();
    assert.equal(q<HTMLElement>(".console-diagrams__state").hidden, true);
    instance.deactivate();
  });
});

// ---- the runtime's record -------------------------------------------------------------------

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

test("the actor record carries every value as data, however it is spelled", () => {
  const f = buildFigure(
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
  assert.equal(f.id, "targets-app");
  assert.equal(f.desc, "scope x");
  assert.equal(f.boxes[0].actor?.name, 'say "{hi}"', "no quoting: the label crosses as a value");
  assert.equal(f.boxes[0].actor?.look, "plain");
  assert.deepEqual(
    f.flows.map((fl) => [fl.src.actor?.name, fl.dst.actor?.name]),
    [['say "{hi}"', "b"]],
  );
  assert.throws(
    () =>
      buildFigure(
        { nodes: [{ id: "external:a", anchor: "a", label: "a" }], edges: [["external:a", "gone"]] },
        { id: "projects", title: "P", claim: "flow", anchorHref: "" },
        "",
      ),
    /edge external:a->gone to no node/,
  );
});

test("a draw result is a figure, a refusal or a failure", () => {
  assert.deepEqual(toRelayout(drew("<svg/>")), { kind: "ok", svg: "<svg/>" });
  assert.deepEqual(toRelayout({ ok: false, svg: "", findings: "over", diag: null }), {
    kind: "refused",
    detail: "over",
  });
  assert.deepEqual(
    toRelayout({ ok: false, svg: "", findings: "", diag: { msg: "boom", line: 3, col: 1 } }),
    { kind: "failed", detail: "boom (line 3)" },
  );
  assert.deepEqual(toRelayout({ ok: false, svg: "", findings: "", diag: null }), {
    kind: "failed",
    detail: "the runtime drew nothing",
  });
});

test("the runtime is globalThis.buzz with drawFigure, and the record crosses as JSON", () => {
  assert.equal(runtimeFrom({ buzz: { evalBuzz: () => null } }), null, "an older wasm");
  let sent = "";
  const rt = runtimeFrom({
    buzz: {
      drawFigure: (json: string) => {
        sent = json;
        return { ok: true, svg: "<svg/>", findings: "", diag: null };
      },
    },
  });
  assert.ok(rt);
  const meta = { id: "p", title: "P", claim: "flow", anchorHref: "" };
  const f = buildFigure({ nodes: [], edges: [] }, meta, "");
  assert.deepEqual(rt.drawFigure(f, ""), drew("<svg/>"));
  assert.deepEqual(JSON.parse(sent), f);
  const odd = runtimeFrom({ buzz: { drawFigure: () => 7 } });
  assert.equal(odd?.drawFigure(f, "").diag?.msg, "the runtime returned nothing");
});
