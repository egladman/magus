// demo.ts - sample notes for the Notes surface, so it can be seen without a daemon.
//
// These are INVENTED, and the surface says so out loud - loadDemo raises the shell's "demo
// data" tag in the status bar, beside the connection state, for as long as they are on screen.
// That disclosure is not politeness, it is the condition on this file existing at all. A
// note's only provenance is the person who wrote it - nothing in the repository
// corroborates one later, which is why agents may read notes and never write them
// (notes/what-belongs-in-a-note.md). Sample prose shown unlabeled in THIS surface would be
// the one lie the store cannot survive, because a reader takes what they see here as
// something a colleague wrote. Labeled, it is a screenshot with the lights on.
//
// So: if the disclosure goes, this file goes with it.
//
// The set exercises the rendering rather than looking full - both scopes, every anchor
// verdict, both staleness tiers, a multi-anchor note, and a store carrying a repair warning.
// Six identical healthy notes would show nothing the empty state did not.
//
// They are notes from ACME, the fictional monorepo every other showcase inhabits
// (demo-scenario.ts), and about the same change: one shared library grew an audience list and
// took a Go token verifier and a TypeScript web client down with it. They used to be notes
// about magus's own cache and lockfile, which meant a reader who clicked from Diff to Notes
// met a different fictional company one tab over - the surfaces were each coherent and the
// product was not.
//
// The timeline is why two of these are stale. verify.go changed 92 minutes ago in the very
// changeset the Diff surface is showing, so the note anchored to it reads DRIFTED here. The
// staleness tiers are demonstrated BY the story rather than beside it.

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
} from "@wire/notes/v1alpha1/notes_pb";

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
  // The payoff note. A reader meets this change in the Diff surface - Claims growing an audience
  // list - and finds here the colleague who wrote down why, which is the entire argument for the
  // notes store existing. Recent, healthy, both anchors resolving: the shape a note is supposed to
  // have, shown before the four ways one can go wrong.
  {
    name: "why-claims-carries-an-audience-list",
    editedDaysAgo: 2,
    title: "Why Claims carries an audience list, not one string",
    scope: Scope.SHARED,
    path: "notes/why-claims-carries-an-audience-list.md",
    tags: ["auth", "decisions"],
    anchors: [
      { kind: AnchorKind.PROJECT, target: "libs/authkit", status: AnchorStatus.RESOLVES },
      { kind: AnchorKind.SYMBOL, target: "m authkit/Claims#", status: AnchorStatus.RESOLVES },
    ],
    body:
      "The single `Audience string` was a stub from the first week and it was never populated.\n" +
      "Every caller that needed to know who a token was for read the issuer instead, which is a\n" +
      "different question and happens to agree until it does not.\n\n" +
      "A list, because the gateway now mints one token accepted by both identity and ledger, and\n" +
      "a single value cannot say that. The cost is real and worth writing down: every consumer\n" +
      "that read the field has to change with it, and two of them are not Go.",
  },
  // DRIFTED + OUTRUN, and the story is what caused it: verify.go changed 92 minutes ago in this
  // very changeset, so the fingerprint this note recorded no longer matches. The surface's staleness
  // tiers are demonstrated by the timeline rather than by an unrelated coincidence.
  {
    name: "verification-asserts-on-audience",
    editedDaysAgo: 34,
    title: "Verification asserts on audience, and that is deliberate",
    scope: Scope.SHARED,
    path: "notes/verification-asserts-on-audience.md",
    tags: ["auth", "identity"],
    anchors: [
      {
        kind: AnchorKind.SYMBOL,
        target: "m identity/token/Verify().",
        status: AnchorStatus.DRIFTED,
        detail:
          "The anchored code still exists and no longer matches the fingerprint recorded here.",
      },
    ],
    staleness: Staleness.OUTRUN,
    outrunDays: 31,
    body:
      "A token this service will accept has to name this service. Dropping the assertion makes\n" +
      "every service in the mesh a valid audience for every other one, which is the failure you\n" +
      "find in an incident review rather than in a test.\n\n" +
      "So the assertion stays even though it is the thing that breaks loudly whenever the claims\n" +
      "contract moves. Breaking loudly is the feature.",
  },
  // DANGLING + PETRIFIED. Worth keeping precisely because the file is gone - the note is the only
  // record that the idea was tried, and it is what the next person to have it will find.
  {
    name: "rejected-a-second-service-token",
    editedDaysAgo: 240,
    title: "Rejected: a second token type for service-to-service",
    scope: Scope.SHARED,
    path: "notes/rejected-a-second-service-token.md",
    tags: ["auth", "decisions"],
    anchors: [
      {
        kind: AnchorKind.FILE,
        target: "services/identity/internal/token/s2s.go",
        status: AnchorStatus.DANGLING,
        detail: "Nothing in the workspace resolves this anchor any more.",
      },
    ],
    staleness: Staleness.PETRIFIED,
    outrunDays: 211,
    body:
      "A separate S2S token was built and deleted. It worked, and it doubled every code path that\n" +
      "touches auth: two mint calls, two verify calls, two rotation schedules, two ways to be\n" +
      "wrong.\n\n" +
      "The audience list does the same job inside the type that already exists. The next person to\n" +
      "propose this should find this note rather than the empty space where s2s.go was.",
  },
  // The multi-anchor note, and the one that explains the second half of the blast radius: why a Go
  // contract change took down a TypeScript typecheck. BODY_CHANGED is the subtle verdict - the
  // declaration held, the implementation moved - and the NOTE anchor makes the store a graph rather
  // than a list.
  {
    name: "the-dashboard-reads-claims-directly",
    editedDaysAgo: 9,
    title: "The dashboard reads claims directly, so auth changes reach it",
    scope: Scope.SHARED,
    path: "notes/the-dashboard-reads-claims-directly.md",
    tags: ["auth", "dashboard", "gotcha"],
    anchors: [
      { kind: AnchorKind.TARGET, target: "typecheck", status: AnchorStatus.RESOLVES },
      {
        kind: AnchorKind.SYMBOL,
        target: "m dashboard/session/parseClaims().",
        status: AnchorStatus.BODY_CHANGED,
        detail:
          "This changed inside a declaration that did not, which usually leaves prose about the interface standing. Re-read only if the note claims something about the implementation.",
      },
      {
        kind: AnchorKind.NOTE,
        target: "why-claims-carries-an-audience-list",
        status: AnchorStatus.RESOLVES,
      },
    ],
    body:
      "The web client does not go through a Go client library - it decodes the token itself and\n" +
      "reads the claims off it. That was a deliberate call (one less generated dependency for the\n" +
      "browser bundle) and this is the bill for it: a change to the claims contract is a change to\n" +
      "apps/dashboard, and nothing in either project's imports says so.\n\n" +
      "If you are here because typecheck broke and the diff looks like it only touched Go, this\n" +
      "is why.",
  },
  // PRIVATE + UNVERIFIED. The store for reasoning nobody else needs, and the one anchor verdict
  // that means "not measured yet" rather than "measured and wrong".
  {
    name: "my-local-acme-setup",
    editedDaysAgo: 3,
    title: "My local acme setup",
    scope: Scope.PRIVATE,
    path: "/Users/you/notes/my-local-acme-setup.md",
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
      "Nothing here is committed and nothing attributes it. Which postgres the ledger tests\n" +
      "actually want, and why the documented one hangs on this machine.",
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
