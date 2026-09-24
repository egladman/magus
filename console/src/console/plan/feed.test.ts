// feed.test.ts - the drawer's live feed, the half of the Jobs sheet that answers "is it
// going" rather than "is it done".
//
// rowOf is pure and DOM-free, so the projection runs directly under node. What is worth
// pinning is the READING each row produces: a deny and an ask are the two rows a person is
// scanning for, a contested path is attributed to nobody and must still say so to both
// claimants, and a kind this console does not know has to render rather than vanish. Each of
// those fails SILENTLY - a feed that looks calm while a worker is blocked is worse than no
// feed, because it is believed.

import { test } from "node:test";
import assert from "node:assert/strict";
import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { ActivityEventSchema, Kind, Outcome } from "@wire/activity/v1alpha1/activity_pb";
import { rowOf } from "./feed";

function event(fields: MessageInitShape<typeof ActivityEventSchema>) {
  return create(ActivityEventSchema, { time: timestampFromMs(1_700_000_000_000), ...fields });
}

// Off the EVENT, not out of a payload blob: a two-hundred-row feed that had to fetch a body to
// say "deny" would cost two hundred round trips to render one screen.
test("rowOf reads the guard's verdict off the event", () => {
  const row = rowOf(event({ kind: Kind.AGENT_COMMAND, action: "Bash", preview: "guard: deny" }));
  assert.equal(row.kind, "tool");
  assert.equal(row.mark, "deny");
  assert.equal(row.bad, true);
});

// These two are the whole reason the feed exists: a deny or an ask is a worker stuck on
// something a person can unstick, and a pass is a worker getting on with it. An observation the
// guard judged at all carries no verdict, and must not be dressed up as one.
test("rowOf marks a deny and an ask, and leaves a pass alone", () => {
  assert.equal(rowOf(event({ kind: Kind.AGENT_COMMAND, preview: "guard: ask" })).bad, true);
  assert.equal(rowOf(event({ kind: Kind.AGENT_COMMAND, preview: "guard: pass" })).bad, false);
  assert.equal(rowOf(event({ kind: Kind.AGENT_COMMAND, preview: "observed" })).mark, "");
});

// The path is attributed to NOBODY, which is the honest answer, and both jobs are still told.
// Rendering it as an ordinary edit would hide the one fact worth acting on: the plan has two
// live lanes over one file. Seen for real in this repository between pwa/job-watch and
// pwa/turns-capture, both of which declared internal/trail.
test("rowOf names both claimants of a contested path", () => {
  const row = rowOf(
    event({
      kind: Kind.FILE_CHANGE,
      action: "internal/trail/trail.go",
      contested: ["pwa/job-watch", "pwa/turns-capture"],
    }),
  );
  assert.equal(row.kind, "file");
  assert.equal(row.mark, "contested");
  assert.equal(row.note, "pwa/job-watch and pwa/turns-capture both declare this path");
  assert.equal(row.bad, true);
});

test("rowOf shows an ordinary file change without a mark", () => {
  const row = rowOf(event({ kind: Kind.FILE_CHANGE, action: "internal/job/feed.go" }));
  assert.equal(row.mark, "");
  assert.equal(row.bad, false);
});

test("rowOf separates a failed run from one that worked", () => {
  const failed = rowOf(
    event({
      kind: Kind.RUN,
      action: "magus run go-test .",
      outcome: Outcome.ERROR,
      error: "go exited 1",
      preview: "check",
    }),
  );
  assert.equal(failed.kind, "run");
  assert.equal(failed.mark, "failed");
  assert.equal(failed.note, "check: go exited 1");
  assert.equal(failed.bad, true);

  assert.equal(rowOf(event({ kind: Kind.RUN, preview: "gate lint" })).mark, "ok");
});

// A feed that silently binned what it could not classify would go quiet exactly when the server
// grew something new to say, and nothing on screen would admit it.
test("rowOf renders a kind it does not know rather than dropping it", () => {
  const row = rowOf(event({ kind: Kind.MEMORY, action: "memory.get", preview: "read" }));
  assert.equal(row.kind, "other");
  assert.equal(row.label, "memory.get");
});

// The timestamp comes off the wire, not off this machine's clock: a row stamped "now" on
// arrival would make a backfilled hour of history look like it all happened at once.
test("rowOf takes the time from the event", () => {
  const row = rowOf(event({ kind: Kind.FILE_CHANGE, action: "a.go" }));
  assert.equal(row.at, 1_700_000_000_000);
});
