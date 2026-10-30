// demo.ts - the Figures mode's showcase of the acme monorepo.
// It draws from plain data and sends no request.

import { MagusError, fail, ok, type Result } from "../../../gen/magus/types";
import { WORKSPACE_ROOT } from "../../../desktop/demo-scenario";
import type { DiagramEntry, RenderedDiagram } from "./api";

interface DemoFigure {
  readonly entry: DiagramEntry;
  readonly title: string;
  // The import figure keys boxes by directory; the other draws actors.
  readonly byPath: boolean;
}

const CONSUMERS = [
  "apps/dashboard",
  "apps/admin",
  "services/gateway",
  "services/identity",
  "services/ledger",
  "services/catalog",
  "tools/migrate",
] as const;
const LIBS = ["libs/authkit", "libs/protocol", "libs/ui"] as const;
const EDGES: readonly (readonly [string, string])[] = [
  ["apps/dashboard", "libs/authkit"],
  ["apps/dashboard", "libs/protocol"],
  ["apps/dashboard", "libs/ui"],
  ["apps/admin", "libs/protocol"],
  ["apps/admin", "libs/ui"],
  ["services/gateway", "libs/authkit"],
  ["services/gateway", "libs/protocol"],
  ["services/identity", "libs/authkit"],
  ["services/ledger", "libs/protocol"],
  ["services/catalog", "libs/protocol"],
  ["tools/migrate", "libs/protocol"],
];

const FIGURES: readonly DemoFigure[] = [
  {
    entry: { id: "imports", kind: "imports", title: "Imports across acme", indexed: true },
    title: "Imports across " + WORKSPACE_ROOT + " (demo)",
    byPath: true,
  },
  {
    entry: { id: "projects", kind: "projects", title: "acme projects" },
    title: "Projects in " + WORKSPACE_ROOT + " and what they build on (demo)",
    byPath: false,
  },
];

const BOX_W = 200;
const BOX_H = 40;
const ROW = 64;
const LIB_X = 352;
const PAD = 32;

function escapeXml(s: string): string {
  return s.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");
}

function draw(fig: DemoFigure): string {
  const key = (name: string): string => (fig.byPath ? name : "external:" + name);
  const place = new Map<string, { x: number; y: number }>();
  CONSUMERS.forEach((n, i) => place.set(n, { x: PAD, y: PAD + i * ROW }));
  const libTop = PAD + ((CONSUMERS.length - LIBS.length) * ROW) / 2;
  LIBS.forEach((n, i) => place.set(n, { x: LIB_X, y: libTop + i * ROW }));

  const paths = EDGES.flatMap(([from, to]) => {
    const a = place.get(from);
    const b = place.get(to);
    if (!a || !b) return [];
    const x1 = a.x + BOX_W;
    const y1 = a.y + BOX_H / 2;
    const y2 = b.y + BOX_H / 2;
    const mid = (x1 + b.x) / 2;
    const d = `M ${x1} ${y1} C ${mid} ${y1}, ${mid} ${y2}, ${b.x} ${y2}`;
    return [
      `<path data-edge="${key(from)}->${key(to)}" d="${d}" fill="none" ` +
        'style="stroke:var(--magus-diagram-rule, #8a8d90);stroke-width:1.5"/>',
    ];
  });

  const boxes = [...place].map(([name, p]) => {
    const anchor = fig.byPath ? ` data-anchor="${name}"` : "";
    return (
      `<g data-node="${key(name)}"${anchor}>` +
      `<rect x="${p.x}" y="${p.y}" width="${BOX_W}" height="${BOX_H}" rx="6" ` +
      'style="fill:var(--magus-diagram-fill, #f2f2f2);' +
      'stroke:var(--magus-diagram-rule, #8a8d90);stroke-width:1.5"/>' +
      `<text x="${p.x + BOX_W / 2}" y="${p.y + BOX_H / 2 + 5}" text-anchor="middle" ` +
      'font-size="14" style="fill:var(--magus-diagram-ink, #151515)">' +
      `${escapeXml(name)}</text></g>`
    );
  });

  const width = LIB_X + BOX_W + PAD;
  const height = PAD * 2 + (CONSUMERS.length - 1) * ROW + BOX_H;
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${width} ${height}">` +
    paths.join("") +
    boxes.join("") +
    "</svg>"
  );
}

export function demoDiagramList(): DiagramEntry[] {
  return FIGURES.map((f) => f.entry);
}

// renderDemoDiagram answers with the figure a server would draw for id.
// The lens does not apply: the demo has one fixed cut of each figure.
export function renderDemoDiagram(id: string): Result<RenderedDiagram> {
  const fig = FIGURES.find((f) => f.entry.id === id);
  if (!fig) {
    return fail(new MagusError({ code: "", message: "the demo has no figure " + id }));
  }
  return ok({
    id,
    title: fig.title,
    svg: draw(fig),
    nodes: [...CONSUMERS, ...LIBS].map((name) => ({ id: name, anchor: name, label: name })),
    sourceUrl: "",
  });
}
