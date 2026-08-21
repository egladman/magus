// demo.ts - sample notes for the Notes surface, so it can be seen without a daemon.
//
// These are INVENTED, and the surface says so out loud - loadDemo raises the shell's "demo
// data" tag in the status bar, beside the connection state, for as long as they are on screen.
// That disclosure is not politeness, it is the condition on this file existing at all. A
// note's only provenance is the person who wrote it - nothing in the repository
// corroborates one later, which is why agents may read notes and never write them
// (notes/what-belongs-in-a-note.md). Sample prose shown unlabelled in THIS surface would be
// the one lie the store cannot survive, because a reader takes what they see here as
// something a colleague wrote. Labelled, it is a screenshot with the lights on.
//
// So: if the disclosure goes, this file goes with it.
//
// The set exercises the rendering rather than looking full - both scopes, every anchor
// verdict, both staleness tiers, a multi-anchor note, and a store carrying a repair warning.
// Six identical healthy notes would show nothing the empty state did not.

import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
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

interface AnchorSpec {
  kind: AnchorKind;
  target: string;
  status: AnchorStatus;
  detail?: string;
}

interface NoteSpec {
  name: string;
  title: string;
  scope: Scope;
  path: string;
  tags: string[];
  anchors: AnchorSpec[];
  body: string;
  staleness?: Staleness;
  outrunDays?: number;
  // editedDaysAgo is a RELATIVE age, resolved against the clock when demoNotes() runs. A fixed
  // date would drift into "4 years ago" and make the sample set read as an abandoned store.
  // It is not outrunDays: how far a note's subject ran ahead of its prose and when the file was
  // last touched are different measurements, and the surface shows them in different places.
  editedDaysAgo: number;
}

// KIND_SLUG spells a node id the way the graph does. AnchorKind is a protobuf enum, so it is a
// NUMBER at runtime: building an id by concatenating it produced "3:." where a real daemon sends
// "project:.", which reads as a broken id rather than as sample data.
const KIND_SLUG: Record<number, string> = {
  [AnchorKind.SYMBOL]: "symbol",
  [AnchorKind.FILE]: "file",
  [AnchorKind.PROJECT]: "project",
  [AnchorKind.TARGET]: "target",
  [AnchorKind.NOTE]: "note",
};

function anchor(spec: AnchorSpec): Anchor {
  return create(AnchorSchema, {
    kind: spec.kind,
    target: spec.target,
    status: spec.status,
    // A node id only for one that resolves: the surface prints it beside the anchor so a
    // reader can carry it to the Graph Explorer, and a dangling anchor has nothing to carry.
    nodeId: spec.status === AnchorStatus.RESOLVES ? KIND_SLUG[spec.kind] + ":" + spec.target : "",
    detail: spec.detail ?? "",
  });
}

const NOTES: NoteSpec[] = [
  {
    name: "why-the-cache-keys-on-declared-inputs",
    editedDaysAgo: 6,
    title: "Why the cache keys on declared inputs, not the tree",
    scope: Scope.SHARED,
    path: "notes/why-the-cache-keys-on-declared-inputs.md",
    tags: ["cache", "decisions"],
    anchors: [
      { kind: AnchorKind.PROJECT, target: ".", status: AnchorStatus.RESOLVES },
      { kind: AnchorKind.SYMBOL, target: "m cache/Key().", status: AnchorStatus.RESOLVES },
    ],
    body:
      "Keying on the whole tree was tried first and abandoned. It is correct and it is useless:\n" +
      "every target misses on every commit, so the cache never pays for itself.\n\n" +
      "Declared inputs make the key a claim the magusfile can be wrong about, and that is the\n" +
      "trade. An undeclared read replays stale output, and it fails silently - the run is green\n" +
      "and the bytes are old. Written down because the code cannot show you the option that was\n" +
      "rejected, only the one that survived.",
  },
  {
    name: "two-caches-and-why-they-pair",
    editedDaysAgo: 41,
    title: "Two caches, and why they pair",
    scope: Scope.SHARED,
    path: "notes/two-caches-and-why-they-pair.md",
    tags: ["cache", "performance"],
    anchors: [
      {
        kind: AnchorKind.SYMBOL,
        target: "m cache/Store#Put().",
        status: AnchorStatus.DRIFTED,
        detail:
          "The anchored code still exists and no longer matches the fingerprint recorded here.",
      },
    ],
    staleness: Staleness.OUTRUN,
    outrunDays: 34,
    body:
      "The local cache and the remote one are not a hierarchy, they are a pair with different\n" +
      "failure modes. Local is fast and lies after a branch switch; remote is slow and lies after\n" +
      "a force-push. Reading both and comparing is what catches either one.",
  },
  {
    name: "rejected-a-second-lockfile-format",
    editedDaysAgo: 240,
    title: "Rejected: a second lockfile format",
    scope: Scope.SHARED,
    path: "notes/rejected-a-second-lockfile-format.md",
    tags: ["decisions"],
    anchors: [
      {
        kind: AnchorKind.FILE,
        target: "internal/lock/v2.go",
        status: AnchorStatus.DANGLING,
        detail: "Nothing in the workspace resolves this anchor any more.",
      },
    ],
    staleness: Staleness.PETRIFIED,
    outrunDays: 211,
    body:
      "Worth keeping precisely because the file it points at is gone. The v2 format was written,\n" +
      "measured and deleted: it shaved 40ms off a cold resolve and cost a migration nobody could\n" +
      "roll back. The next person to have this idea should find this note, not the empty space\n" +
      "where the code used to be.",
  },
  {
    name: "the-sandbox-is-off-by-default",
    editedDaysAgo: 12,
    title: "The sandbox is off by default, so the env passthrough is inert",
    scope: Scope.SHARED,
    path: "notes/the-sandbox-is-off-by-default.md",
    tags: ["sandbox", "gotcha"],
    anchors: [
      { kind: AnchorKind.TARGET, target: "test", status: AnchorStatus.RESOLVES },
      {
        kind: AnchorKind.SYMBOL,
        target: "m sandbox/Policy#Apply().",
        status: AnchorStatus.BODY_CHANGED,
        detail:
          "This changed inside a declaration that did not, which usually leaves prose about the interface standing. Re-read only if the note claims something about the implementation.",
      },
      {
        kind: AnchorKind.NOTE,
        target: "two-caches-and-why-they-pair",
        status: AnchorStatus.RESOLVES,
      },
    ],
    body:
      "Measured, not assumed: with the sandbox disabled no policy is attached and the child gets\n" +
      "the environment unscrubbed. So the sandbox.env passthrough does nothing as configured.\n" +
      "Do not delete it - it becomes load-bearing the moment the sandbox is enabled - but do not\n" +
      "credit it for fixing anything either.",
  },
  {
    name: "my-local-toolchain-pins",
    editedDaysAgo: 3,
    title: "My local toolchain pins",
    scope: Scope.PRIVATE,
    path: "/Users/you/notes/my-local-toolchain-pins.md",
    tags: ["local"],
    anchors: [
      {
        kind: AnchorKind.FILE,
        target: "mise.toml",
        status: AnchorStatus.UNVERIFIED,
        detail: "No fingerprint has been recorded for this anchor yet.",
      },
    ],
    body:
      "Nothing here is committed and nothing attributes it. This is the store for reasoning that\n" +
      "is only useful to me: which node build actually works on this machine, and why the obvious\n" +
      "one does not.",
  },
];

// Both stores DECLARED. An undeclared one renders as "this workspace declares no store", which
// is a different fact and not the one worth showing. The private store carries a warning
// because a store that can go wrong is part of what this surface is for.
const STORES: { scope: Scope; path: string; issues: string[] }[] = [
  { scope: Scope.SHARED, path: "notes", issues: [] },
  {
    scope: Scope.PRIVATE,
    path: "/Users/you/notes",
    issues: ["drafts/untitled.md: carries a magus block with no title"],
  },
];

export interface DemoNotes {
  stores: StoreStatus[];
  notes: Note[];
  // body resolves the prose the reading pane shows, with no daemon behind it.
  body(name: string): string;
}

function build(spec: NoteSpec): Note {
  return create(NoteSchema, {
    name: spec.name,
    title: spec.title,
    scope: spec.scope,
    path: spec.path,
    tags: spec.tags,
    anchors: spec.anchors.map(anchor),
    staleness: spec.staleness ?? Staleness.UNMEASURED,
    outrunDays: spec.outrunDays ?? 0,
    modifyTime: timestampFromMs(Date.now() - spec.editedDaysAgo * 86400000),
  });
}

export function demoNotes(): DemoNotes {
  const bodies = new Map(NOTES.map((n) => [n.name, n.body]));
  return {
    stores: STORES.map((s) =>
      create(StoreStatusSchema, {
        scope: s.scope,
        declared: true,
        path: s.path,
        issues: s.issues,
        // The live handler counts what it contributed to the response, and the scope filter
        // reads that rather than re-deriving it. A demo that left it at zero would render the
        // filter as "Shared 0" over four visible notes.
        noteCount: NOTES.filter((n) => n.scope === s.scope).length,
      }),
    ),
    notes: NOTES.map(build),
    body: (name) => bodies.get(name) ?? "",
  };
}
