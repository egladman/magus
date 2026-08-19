// demo.test.ts - the committed-export -> surface mapping. adaptExport is pure and DOM-free, so
// it runs directly under node. Run: `pnpm run test`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { AnchorKind, AnchorStatus, Scope, Staleness } from "../../gen/magus/notes/v1/notes_pb";
import { adaptExport } from "./demo";

const listing = {
  stores: [{ scope: "shared", path: "notes" }],
  notes: [
    {
      name: "what-belongs-in-a-note",
      title: "What belongs in a note",
      tags: ["conventions"],
      anchors: [{ kind: "project", target: "." }],
      body: "Two stores, and the difference is provenance.",
      scope: "shared",
      path: "notes/what-belongs-in-a-note.md",
    },
  ],
  issues: [],
};

test("adaptExport carries the fields a card renders", () => {
  const { notes } = adaptExport(listing);
  const n = notes[0];
  assert.ok(n);
  assert.equal(n.name, "what-belongs-in-a-note");
  assert.equal(n.title, "What belongs in a note");
  assert.deepEqual(n.tags, ["conventions"]);
  // The path is what the card shows a reader so they can open the file; it came from the
  // export rather than being derived here, and losing it renders an empty <code>.
  assert.equal(n.path, "notes/what-belongs-in-a-note.md");
  assert.equal(n.anchors[0]?.kind, AnchorKind.PROJECT);
  assert.equal(n.anchors[0]?.target, ".");
});

// The two facts a static export cannot know must come out as "no answer", never as a pass. A
// green anchor or a fresh badge in the demo would be the surface asserting a verification that
// never ran, which is the one thing the notes store's whole design refuses to do.
test("adaptExport claims no verdict it cannot have measured", () => {
  const { notes } = adaptExport(listing);
  const n = notes[0];
  assert.ok(n);
  assert.equal(n.anchors[0]?.status, AnchorStatus.UNVERIFIED);
  assert.equal(n.staleness, Staleness.UNMEASURED);
});

// notes-generate exports the shared store only, so nothing the demo renders can be attributed
// to a store that was never committed.
test("adaptExport marks the store shared and declared", () => {
  const { stores } = adaptExport(listing);
  const s = stores[0];
  assert.ok(s);
  assert.equal(s.scope, Scope.SHARED);
  assert.equal(s.declared, true);
  assert.equal(s.path, "notes");
});

test("adaptExport resolves a body by note name", () => {
  const { body } = adaptExport(listing);
  assert.match(body("what-belongs-in-a-note"), /provenance/);
  assert.equal(body("no-such-note"), "");
});

// An empty export is the declared-and-empty store, which the surface says out loud. It must not
// throw its way into the "could not load" arm, which would blame the export for being correct.
test("adaptExport survives a listing with no notes", () => {
  const { stores, notes } = adaptExport({ stores: [{ scope: "shared", path: "notes" }] });
  assert.equal(notes.length, 0);
  assert.equal(stores.length, 1);
});
