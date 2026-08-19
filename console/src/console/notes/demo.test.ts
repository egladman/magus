// demo.test.ts - the sample notes the Notes surface shows without a daemon. demoNotes is pure
// and DOM-free, so it runs directly under node. Run: `pnpm run test`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { AnchorStatus, Scope, Staleness } from "../../gen/magus/notes/v1alpha1/notes_pb";
import { demoNotes } from "./demo";

// A demo of five healthy notes shows nothing the empty state did not. These four properties are
// what makes it a demo of the SURFACE rather than a screenshot of a list.
test("the sample set exercises what the surface renders", () => {
  const { notes, stores } = demoNotes();

  assert.deepEqual(
    [...new Set(stores.map((s) => s.scope))].sort(),
    [Scope.SHARED, Scope.PRIVATE].sort(),
    "both stores, so the scope banner has something to distinguish",
  );
  assert.ok(
    stores.some((s) => s.issues.length > 0),
    "a store carrying a repair warning",
  );

  const verdicts = new Set(notes.flatMap((n) => n.anchors).map((a) => a.status));
  for (const want of [
    AnchorStatus.RESOLVES,
    AnchorStatus.DANGLING,
    AnchorStatus.DRIFTED,
    AnchorStatus.UNVERIFIED,
  ]) {
    assert.ok(verdicts.has(want), `anchor verdict ${want} is represented`);
  }

  const staleness = new Set(notes.map((n) => n.staleness));
  assert.ok(staleness.has(Staleness.OUTRUN), "an outrun note");
  assert.ok(staleness.has(Staleness.PETRIFIED), "a petrified note");
});

// Every card renders these, and a blank one reads as a broken surface rather than as sample data.
test("every sample note is renderable", () => {
  const { notes, body } = demoNotes();
  assert.ok(notes.length > 0);
  for (const n of notes) {
    assert.ok(n.name, "a name, which is also the body key");
    assert.ok(n.title, `${n.name} has a title`);
    assert.ok(n.path, `${n.name} has a path the card can show`);
    assert.ok(n.anchors.length > 0, `${n.name} is anchored; an unanchored note is unfindable`);
    assert.ok(body(n.name).length > 0, `${n.name} expands to prose`);
  }
});

// Staleness is measured in days behind, and the label renders that number. A note flagged as
// behind by zero days is a contradiction the reader would have to resolve.
test("a note flagged as behind says how far behind", () => {
  for (const n of demoNotes().notes) {
    if (n.staleness === Staleness.OUTRUN || n.staleness === Staleness.PETRIFIED) {
      assert.ok(n.outrunDays > 0, `${n.name} carries a day count`);
    } else {
      assert.equal(n.outrunDays, 0, `${n.name} claims no divergence it did not measure`);
    }
  }
});

test("body resolves by name and is empty for an unknown one", () => {
  const { body } = demoNotes();
  assert.match(body("two-caches-and-why-they-pair"), /force-push/);
  assert.equal(body("no-such-note"), "");
});
