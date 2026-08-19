// demo.ts - the Notes demo, which is this workspace's REAL shared notes rather than a fixture.
//
// Every other surface synthesizes its demo, and this one must not. A note's only provenance is
// the person who wrote it, so a plausible-looking sample note is prose no person wrote, shown in
// the one surface whose entire claim is the opposite (notes/what-belongs-in-a-note.md). The
// export the root notes-generate target commits is the way out: the demo is the notes git already
// attributes, and it grows as the store does.
//
// What the export CANNOT carry is anchor verdicts and staleness. Both are measured against a live
// workspace by the daemon, and a static file has no workspace to measure. They are left
// UNVERIFIED and UNMEASURED here rather than filled in with something reassuring - the surface
// already renders those two as "no answer" instead of "fine", which is the honest reading and the
// reason it is safe to leave them alone.

import { create } from "@bufbuild/protobuf";
import {
  Scope,
  AnchorKind,
  AnchorStatus,
  Staleness,
  NoteSchema,
  AnchorSchema,
  StoreStatusSchema,
  type Note,
  type Anchor,
  type StoreStatus,
} from "../../gen/magus/notes/v1alpha1/notes_pb";

// The shape `magus notes ls --shared --reproducible -o json` writes. Declared here rather than
// generated because it is the CLI's own output struct, not a proto message: the two are coupled
// by the notes-generate target, and console/magusfile.buzz names gen/notes.json as a source so a
// change to either side rebuilds this bundle.
interface ExportedAnchor {
  kind?: string;
  target?: string;
}
interface ExportedNote {
  name?: string;
  title?: string;
  tags?: string[];
  anchors?: ExportedAnchor[];
  body?: string;
  scope?: string;
  path?: string;
}
interface ExportedStore {
  scope?: string;
  path?: string;
}
interface NotesExport {
  stores?: ExportedStore[];
  notes?: ExportedNote[];
  issues?: { message?: string }[];
}

// The CLI writes anchor kinds as the words a note's frontmatter uses; the surface renders the
// proto enum. An unknown word maps to UNSPECIFIED, which renders as a bare "anchor" rather than
// silently claiming a kind the export did not say.
const ANCHOR_KIND: Record<string, AnchorKind> = {
  symbol: AnchorKind.SYMBOL,
  file: AnchorKind.FILE,
  project: AnchorKind.PROJECT,
  target: AnchorKind.TARGET,
  note: AnchorKind.NOTE,
};

export interface DemoNotes {
  stores: StoreStatus[];
  notes: Note[];
  // body returns the prose the export carried, so the card's Read button resolves without a
  // daemon. Synchronous data behind the surface's async loadBody signature.
  body(name: string): string;
}

function adaptAnchor(a: ExportedAnchor): Anchor {
  return create(AnchorSchema, {
    kind: ANCHOR_KIND[a.kind ?? ""] ?? AnchorKind.UNSPECIFIED,
    target: a.target ?? "",
    status: AnchorStatus.UNVERIFIED,
    detail: "Anchors resolve against a live workspace; this demo is a committed export.",
  });
}

function adaptNote(n: ExportedNote): Note {
  return create(NoteSchema, {
    name: n.name ?? "",
    title: n.title ?? "",
    tags: n.tags ?? [],
    path: n.path ?? "",
    scope: Scope.SHARED,
    anchors: (n.anchors ?? []).map(adaptAnchor),
    staleness: Staleness.UNMEASURED,
  });
}

// adaptExport turns the committed listing into what the surface renders. Only the SHARED store is
// exported (notes-generate passes --shared), so nothing here can present a private note as a
// committed one - the mistake the scope banner exists to prevent.
export function adaptExport(raw: NotesExport): DemoNotes {
  const notes = (raw.notes ?? []).map(adaptNote);
  const bodies = new Map<string, string>();
  for (const n of raw.notes ?? []) bodies.set(n.name ?? "", n.body ?? "");
  const stores = (raw.stores ?? []).map((s) =>
    create(StoreStatusSchema, {
      scope: Scope.SHARED,
      declared: true,
      path: s.path ?? "",
      issues: (raw.issues ?? []).map((i) => i.message ?? ""),
    }),
  );
  return { stores, notes, body: (name) => bodies.get(name) ?? "" };
}

// loadDemoNotes fetches the committed export. Resolved against THIS bundle rather than the
// document: the console mounts this surface into a page at a different path, where a
// document-relative "./notes.json" would miss. Same reasoning as the graph surface's demo fetch.
export async function loadDemoNotes(): Promise<DemoNotes> {
  const r = await fetch(new URL("./notes.json", import.meta.url));
  if (!r.ok) throw new Error("HTTP " + r.status);
  return adaptExport((await r.json()) as NotesExport);
}
