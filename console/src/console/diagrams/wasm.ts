// wasm.ts - the Buzz runtime a figure upgrades to on request: the docs playground's wasm, loaded
// the way docs/src/site/buzz-runtime.ts loads it. It embeds magus/figure, so the page builds a
// figure\Figure record from the rows the server sent and hands it to buzz.drawFigure, which
// calls figure\draw exactly as internal/handler/diagram does. No Buzz source is written here.
// Once loaded, a lens change re-lays the figure out here instead of asking the server.
//
// Never loaded automatically: it is 4.2MB and a Go runtime, and the static render already is the
// figure. The explicit control is the whole policy.

import type { Declaration, DeclaredNode, Lens } from "./lens";
import { cutDeclaration, describeLens } from "./lens";

export interface BuzzDiag {
  readonly msg: string;
  readonly line: number;
  readonly col: number;
}

// DrawResult is buzz.drawFigure's answer: the SVG when ok, else figure's findings, or a diag
// when the record or the runtime failed.
export interface DrawResult {
  readonly ok: boolean;
  readonly svg: string;
  readonly findings: string;
  readonly diag: BuzzDiag | null;
}

export interface BuzzRuntime {
  drawFigure(figure: Figure, anchorHref: string): DrawResult;
}

// The figure\Figure record and its parts, field for field as magus/figure declares them and
// libs/figure/embed.go mirrors them. An enum crosses as its case's name, which the runtime
// decodes into the case; it refuses a member the record does not declare and a name no case
// holds.
export type Look = "plain" | "focal" | "store" | "external" | "input" | "optional" | "decision";
export type Stroke = "plain" | "focal" | "external" | "optional";
export type Direction = "across" | "down";
export type Axis = "rank" | "row";

export interface Figure {
  readonly id: string;
  readonly title: string;
  readonly eyebrow: string;
  readonly desc: string;
  readonly direction: Direction;
  readonly generated: boolean;
  // Why the figure draws no directory; "" for one that draws some.
  readonly unscopedWhy: string;
  readonly graphEdges: boolean;
  readonly boxes: readonly Box[];
  readonly scopes: readonly DirSet[];
  readonly exclusions: readonly Exclusion[];
  readonly hiddenEdges: readonly HiddenEdges[];
  readonly edgeMarks: readonly EdgeMark[];
  readonly flows: readonly Flow[];
  readonly zones: readonly Zone[];
  readonly alignments: readonly Alignment[];
  readonly legends: readonly Legend[];
}

// Box is one drawn box: exactly one of dir, group and actor is set.
export interface Box {
  readonly dir: Dir | null;
  readonly group: DirSet | null;
  readonly actor: Actor | null;
  readonly label: string;
  readonly sub: string;
  readonly tag: string;
  readonly focal: boolean;
  readonly look: Look | null;
}

export interface DirSet {
  readonly dirs: readonly Dir[];
  readonly named: string;
}

export interface Actor {
  readonly name: string;
  readonly sub: string;
  readonly tag: string;
  readonly link: string;
  // null is "external".
  readonly look: Look | null;
}

export interface Exclusion {
  readonly set: DirSet;
  readonly why: string;
}

export interface HiddenEdges {
  readonly src: DirSet;
  readonly dst: DirSet;
  readonly why: string;
}

export interface EdgeMark {
  readonly src: Dir;
  readonly dst: Dir;
  readonly label: string;
  readonly stroke: Stroke | null;
}

// End is one end of a Flow: exactly one of dir and actor is set.
export interface End {
  readonly dir: Dir | null;
  readonly actor: Actor | null;
}

export interface Flow {
  readonly src: End;
  readonly dst: End;
  readonly label: string;
  readonly stroke: Stroke | null;
}

export interface Zone {
  readonly label: string;
  readonly dirs: DirSet | null;
  readonly actors: readonly Actor[];
  readonly boundary: boolean;
}

export interface Alignment {
  readonly axis: Axis;
  readonly dirs: DirSet | null;
  readonly actors: readonly Actor[];
}

export interface Legend {
  readonly look: Look;
  readonly label: string;
}

// Dir is the magus\Dir record under its Buzz field names.
export interface Dir {
  readonly path: string;
  readonly id: string;
  readonly layer: string;
  readonly language: string;
  readonly imports: readonly string[];
  readonly importedBy: readonly string[];
  readonly importsIndexed: boolean;
  readonly calls: readonly DirCall[];
  readonly calledBy: readonly DirCall[];
  readonly children: readonly string[];
  readonly files: number;
}

export interface DirCall {
  readonly dir: string;
  readonly transport: string;
  readonly marker: string;
  readonly source: string;
}

export interface GoInstance {
  run(instance: WebAssembly.Instance): Promise<void> | void;
  importObject: WebAssembly.Imports;
}

export type GoConstructor = new () => GoInstance;

function hasFunction<K extends string>(
  v: unknown,
  key: K,
): v is Record<K, (...args: never[]) => unknown> {
  return (
    typeof v === "object" && v !== null && typeof (v as Record<string, unknown>)[key] === "function"
  );
}

function diagOf(v: unknown): BuzzDiag | null {
  if (typeof v !== "object" || v === null) return null;
  const d = v as { msg?: unknown; line?: unknown; col?: unknown };
  return typeof d.msg === "string"
    ? { msg: d.msg, line: Number(d.line) || 0, col: Number(d.col) || 0 }
    : null;
}

// runtimeFrom narrows globalThis.buzz, which the wasm's Go main() installs. A wasm built before
// drawFigure existed is no runtime here.
export function runtimeFrom(g: unknown): BuzzRuntime | null {
  if (typeof g !== "object" || g === null) return null;
  const buzz = (g as { buzz?: unknown }).buzz;
  if (!hasFunction(buzz, "drawFigure")) return null;
  const draw = buzz.drawFigure as (figure: string, anchorHref: string) => unknown;
  return {
    drawFigure: (figure: Figure, anchorHref: string): DrawResult => {
      const raw: unknown = draw(JSON.stringify(figure), anchorHref);
      if (typeof raw !== "object" || raw === null)
        return {
          ok: false,
          svg: "",
          findings: "",
          diag: { msg: "the runtime returned nothing", line: 0, col: 0 },
        };
      const r = raw as { ok?: unknown; svg?: unknown; findings?: unknown; diag?: unknown };
      return {
        ok: r.ok === true,
        svg: typeof r.svg === "string" ? r.svg : "",
        findings: typeof r.findings === "string" ? r.findings : "",
        diag: diagOf(r.diag),
      };
    },
  };
}

// wasmRoot is gen/wasm/ beside this bundle's gen/diagrams/, where the console build copies the
// playground's buzz.wasm and wasm_exec.js.
export function wasmRoot(moduleUrl: string = import.meta.url): string {
  return new URL("../wasm/", moduleUrl).href;
}

let loading: Promise<BuzzRuntime> | null = null;

export interface LoadOptions {
  readonly root?: string;
  // How long Go's main() may take to install globalThis.buzz after the module starts.
  readonly readyMs?: number;
}

// ensureBuzz resolves with the runtime, loading it at most once per page. A failed load is
// forgotten so the next click retries; the caller reports the failure.
export function ensureBuzz(opts: LoadOptions = {}): Promise<BuzzRuntime> {
  const ready = runtimeFrom(globalThis);
  if (ready) return Promise.resolve(ready);
  if (loading) return loading;
  const root = opts.root ?? wasmRoot();
  const readyMs = opts.readyMs ?? 5000;
  loading = new Promise<BuzzRuntime>((resolve, reject) => {
    const script = document.createElement("script");
    script.src = root + "wasm_exec.js";
    script.onerror = () =>
      reject(
        new Error(
          "could not load " +
            script.src +
            "; the console build ships it only when docs' build_playground ran first",
        ),
      );
    script.onload = () => {
      const Go = (globalThis as { Go?: unknown }).Go;
      if (typeof Go !== "function") {
        reject(new Error("wasm_exec.js loaded but defined no Go runtime"));
        return;
      }
      const url = root + "buzz.wasm";
      const bytes = async (): Promise<ArrayBuffer> => {
        const res = await fetch(url);
        if (!res.ok) throw new Error("could not load " + url + ": HTTP " + res.status);
        return res.arrayBuffer();
      };
      startGo(Go as GoConstructor, bytes, readyMs).then(resolve, reject);
    };
    document.head.append(script);
  });
  loading.catch(() => {
    // reported: the caller of ensureBuzz reports this same rejection; dropping it lets a retry reload
    loading = null;
  });
  return loading;
}

// startGo instantiates the wasm under Go and waits for main() to install globalThis.buzz,
// which it does asynchronously after run() returns.
export async function startGo(
  Go: GoConstructor,
  bytes: () => Promise<ArrayBuffer | Uint8Array>,
  readyMs: number,
): Promise<BuzzRuntime> {
  const go = new Go();
  const mod = await WebAssembly.instantiate(await bytes(), go.importObject);
  void go.run(mod.instance);
  const deadline = Date.now() + readyMs;
  for (;;) {
    const rt = runtimeFrom(globalThis);
    if (rt) return rt;
    if (Date.now() > deadline)
      throw new Error("the Buzz runtime started but never exposed drawFigure");
    await new Promise((r) => setTimeout(r, 30));
  }
}

// figureId is the handler's ids{}.of for the figure id: letters, digits, - and _ survive.
export function figureId(id: string): string {
  const base = id.replace(/[^A-Za-z0-9_-]/g, "-").replace(/^-+|-+$/g, "");
  return base || "root";
}

// anchorTemplate is the handler's: a node anchors a path, never a line.
export function anchorTemplate(sourceUrl: string): string {
  const line = "#L{line}";
  return sourceUrl.endsWith(line) ? sourceUrl.slice(0, -line.length) : sourceUrl;
}

// linkTo is the handler's linkTo: one pass, so an anchor holding {line} stays as written.
export function linkTo(anchorHref: string, anchor: string): string {
  if (!anchorHref) return "";
  return anchorHref.replace(/\{path\}|\{line\}/g, (m) => (m === "{path}" ? anchor : ""));
}

// actorNames is the handler's: figure keys an actor by name, so a label two nodes share takes
// the node's anchor. Aligned with nodes by index.
export function actorNames(nodes: readonly DeclaredNode[]): string[] {
  const count = new Map<string, number>();
  for (const n of nodes) count.set(n.label, (count.get(n.label) ?? 0) + 1);
  return nodes.map((n) =>
    (count.get(n.label) ?? 0) > 1 ? n.label + " (" + n.anchor + ")" : n.label,
  );
}

// IMPORTS is the claim of the handler's import figure, the one kind drawn from Dir records.
export const IMPORTS = "imports";

// drawnNodes re-keys the server's node rows by the id magus/figure draws as data-node: a box by
// its directory, an actor as external:<name>. The SVG's data-edge pairs use those ids, so the
// node list, focus and a lens cut need them too.
export function drawnNodes(nodes: readonly DeclaredNode[], claim: string): DeclaredNode[] {
  if (claim === IMPORTS) return nodes.map((n) => ({ ...n, id: n.anchor }));
  const names = actorNames(nodes);
  return nodes.map((n, i) => ({ ...n, id: "external:" + names[i] }));
}

export interface FigureMeta {
  readonly id: string;
  readonly title: string;
  // IMPORTS for the import figure; any other claim draws actors, as the handler's figureOf does.
  readonly claim: string;
  // The node link template, {path} filled per node, or "" for no links.
  readonly anchorHref: string;
}

const EMPTY_FIGURE: Figure = {
  id: "",
  title: "",
  eyebrow: "",
  desc: "",
  direction: "across",
  generated: false,
  unscopedWhy: "",
  graphEdges: false,
  boxes: [],
  scopes: [],
  exclusions: [],
  hiddenEdges: [],
  edgeMarks: [],
  flows: [],
  zones: [],
  alignments: [],
  legends: [],
};

const EMPTY_BOX: Box = {
  dir: null,
  group: null,
  actor: null,
  label: "",
  sub: "",
  tag: "",
  focal: false,
  look: null,
};

// figureFor is the handler's figureOf over decl, whose node ids are drawnNodes ids: the same
// Figure record, built from the same rows.
export function figureFor(decl: Declaration, meta: FigureMeta, desc: string): Figure {
  const base: Figure = {
    ...EMPTY_FIGURE,
    id: figureId(meta.id),
    title: meta.title,
    desc,
  };
  if (meta.claim === IMPORTS) {
    const anchor = new Map(decl.nodes.map((n) => [n.id, n.anchor]));
    const imports = new Map<string, string[]>();
    for (const [src, dst] of decl.edges)
      imports.set(src, [...(imports.get(src) ?? []), anchor.get(dst) ?? ""]);
    const boxes = decl.nodes.map(
      (n): Box => ({
        ...EMPTY_BOX,
        label: n.label,
        dir: {
          path: n.anchor,
          id: "dir:" + n.anchor,
          layer: "",
          language: "go",
          imports: imports.get(n.id) ?? [],
          importedBy: [],
          importsIndexed: true,
          calls: [],
          calledBy: [],
          children: [],
          files: 1,
        },
      }),
    );
    return { ...base, graphEdges: true, boxes };
  }
  const names = actorNames(decl.nodes);
  const actors = new Map<string, Actor>();
  decl.nodes.forEach((n, i) =>
    actors.set(n.id, {
      name: names[i],
      sub: "",
      tag: "",
      link: linkTo(meta.anchorHref, n.anchor),
      look: "plain",
    }),
  );
  const flows = decl.edges.map(([src, dst]): Flow => {
    const a = actors.get(src);
    const b = actors.get(dst);
    if (!a || !b)
      throw new Error("the declaration has an edge " + src + "->" + dst + " to no node");
    return {
      src: { dir: null, actor: a },
      dst: { dir: null, actor: b },
      label: "",
      stroke: null,
    };
  });
  return {
    ...base,
    unscopedWhy: "served from the workspace graph: " + desc,
    boxes: [...actors.values()].map((actor): Box => ({ ...EMPTY_BOX, actor })),
    flows,
  };
}

export type Relayout =
  | { readonly kind: "ok"; readonly svg: string }
  | { readonly kind: "refused"; readonly detail: string }
  | { readonly kind: "bad-lens"; readonly detail: string }
  | { readonly kind: "failed"; readonly detail: string };

export function relayoutOf(r: DrawResult): Relayout {
  if (r.ok) return { kind: "ok", svg: r.svg };
  if (r.findings) return { kind: "refused", detail: r.findings };
  const d = r.diag;
  return {
    kind: "failed",
    detail: d ? d.msg + (d.line ? " (line " + d.line + ")" : "") : "the runtime drew nothing",
  };
}

export interface RelayoutInput {
  readonly runtime: BuzzRuntime;
  // The whole figure as the server declared it, before any lens, keyed by drawnNodes ids.
  readonly decl: Declaration;
  readonly meta: FigureMeta;
  readonly lens: Lens;
}

// relayout cuts the declaration through the lens and draws it with figure\draw, in the page.
export function relayout(input: RelayoutInput): Relayout {
  const cut = cutDeclaration(input.decl, input.lens);
  if (!cut.ok) return { kind: "bad-lens", detail: cut.error };
  let figure: Figure;
  try {
    figure = figureFor(cut.decl, input.meta, describeLens(input.lens));
  } catch (e) {
    return { kind: "failed", detail: e instanceof Error ? e.message : String(e) };
  }
  return relayoutOf(input.runtime.drawFigure(figure, input.meta.anchorHref));
}
