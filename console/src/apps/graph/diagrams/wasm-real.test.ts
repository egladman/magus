// wasm-real.test.ts - a figure relaid out through the real buzz.wasm, loaded with Go's
// wasm_exec.js in node: the path the Diagrams runtime control takes, with no fake runtime. It
// SKIPS when the playground wasm is not built; build it with `magus run build_playground docs`.

import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { test } from "node:test";
import { runInThisContext } from "node:vm";
import {
  figureFor,
  figureForLink,
  relayoutOf,
  startGo,
  type Figure,
  type GoConstructor,
  type Legend,
} from "./wasm";

// The test script runs from console/, so this is docs' build_playground output.
const PLAYGROUND = resolve(process.cwd(), "../docs/gen/playground");
const WASM = resolve(PLAYGROUND, "buzz.wasm");
const EXEC = resolve(PLAYGROUND, "wasm_exec.js");
const SKIP =
  existsSync(WASM) && existsSync(EXEC)
    ? false
    : WASM +
      " is not built, so the real runtime is untested here; build it with" +
      " `magus run build_playground docs`";

async function relay(): Promise<void> {
  const g = globalThis as { document?: unknown; Go?: unknown; buzz?: unknown };
  // main() looks for the playground's editor; a document with none leaves it serving
  // globalThis.buzz alone, as on a docs page.
  const stubbed = g.document === undefined;
  if (stubbed) g.document = { getElementById: () => null };
  try {
    runInThisContext(readFileSync(EXEC, "utf8"), { filename: EXEC });
    const rt = await startGo(g.Go as GoConstructor, async () => readFileSync(WASM), 60_000);

    // The runtime resolves magus/figure as a module, not as source handed to it.
    const evalBuzz = (g.buzz as { evalBuzz: (src: string) => { ok: boolean; result: string } })
      .evalBuzz;
    const imported = evalBuzz('import "magus/figure";\nreturn figure\\of("served").id;');
    assert.deepEqual([imported.ok, imported.result], [true, "served"], JSON.stringify(imported));

    const meta = { id: "projects", title: "Projects", claim: "flow", anchorHref: "/code/{path}" };
    const actors = figureFor(
      {
        nodes: [
          { id: "external:app", anchor: "app", label: "app" },
          { id: "external:lib", anchor: "libs/lib", label: "lib" },
        ],
        edges: [["external:app", "external:lib"]],
      },
      meta,
      "",
    );
    const drawn = relayoutOf(rt.drawFigure(actors, meta.anchorHref));
    assert.equal(drawn.kind, "ok", JSON.stringify(drawn));
    const svg = drawn.kind === "ok" ? drawn.svg : "";
    assert.ok(svg.startsWith("<svg"), svg.slice(0, 200));
    assert.match(svg, /data-node="external:app"/);
    assert.match(svg, /data-node="external:lib"/);
    assert.match(svg, /data-edge="external:app->external:lib"/);
    assert.match(svg, /href="\/code\/libs\/lib"/);

    // A shared link's graph: actors only, the edited node accented and tagged, an optional edge
    // dashed and labelled while a plain one stays solid.
    const linkDrawn = relayoutOf(
      rt.drawFigure(
        figureForLink({
          title: "How far this change reaches",
          nodes: [
            { id: "libs/lib", label: "lib", seed: true },
            { id: "app", label: "app", seed: false },
            { id: "tool", label: "tool", seed: false },
          ],
          edges: [
            { from: "libs/lib", to: "app", stroke: "plain", label: "" },
            { from: "app", to: "tool", stroke: "optional", label: "after" },
          ],
        }),
        "",
      ),
    );
    assert.equal(linkDrawn.kind, "ok", JSON.stringify(linkDrawn));
    const linkSvg = linkDrawn.kind === "ok" ? linkDrawn.svg : "";
    assert.match(linkSvg, />EDITED</);
    const solid = /<path data-edge="external:lib->external:app"[^>]*>/.exec(linkSvg)?.[0];
    assert.ok(solid, "the plain edge is drawn");
    assert.doesNotMatch(solid, /stroke-dasharray/);
    assert.match(linkSvg, /<path data-edge="external:app->external:tool"[^>]*stroke-dasharray/);
    assert.match(linkSvg, />AFTER</);

    const importsMeta = { id: "imports", title: "Imports", claim: "imports", anchorHref: "" };
    const imports = figureFor(
      {
        nodes: [
          { id: "internal/a", anchor: "internal/a", label: "a" },
          { id: "internal/b", anchor: "internal/b", label: "b" },
        ],
        edges: [["internal/a", "internal/b"]],
      },
      importsMeta,
      "",
    );
    const graphed = relayoutOf(rt.drawFigure(imports, ""));
    assert.equal(graphed.kind, "ok", JSON.stringify(graphed));
    assert.match(graphed.kind === "ok" ? graphed.svg : "", /data-edge="internal\/a->internal\/b"/);

    const refused = relayoutOf(rt.drawFigure({ ...actors, unscopedWhy: "" }, ""));
    assert.deepEqual(refused, {
      kind: "refused",
      detail:
        'figure "projects": draws no directory; draw a box() or group(), or say why in unscoped(why:)',
    });

    // Enums cross as case names: a real one is decoded into its case, an unknown one refused.
    const down = relayoutOf(rt.drawFigure({ ...actors, direction: "down" }, ""));
    assert.equal(down.kind, "ok", JSON.stringify(down));
    const legend = { look: "shiny", label: "x" } as unknown as Legend;
    const shiny = relayoutOf(rt.drawFigure({ ...actors, legends: [legend] }, ""));
    assert.deepEqual(shiny, {
      kind: "refused",
      detail: 'figure "projects": legends[0].look is "shiny", which names no Look case',
    });

    // A member the record does not declare is refused before any Buzz runs.
    const drifted = { ...actors, titleText: "old" } as Figure;
    const strict = relayoutOf(rt.drawFigure(drifted, ""));
    assert.equal(strict.kind, "failed");
    assert.match(strict.kind === "failed" ? strict.detail : "", /^figure record: .*titleText/);
  } finally {
    delete g.buzz;
    delete g.Go;
    if (stubbed) delete g.document;
  }
}

test("a figure relays out through the real buzz.wasm", { skip: SKIP, timeout: 120_000 }, relay);
