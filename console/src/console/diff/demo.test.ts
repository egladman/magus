import { test } from "node:test";
import assert from "node:assert/strict";
import { demoSession, applyDemoOp } from "./demo";
import { fromWire } from "./parse";
import { DEMO_FILES } from "./gen/demo";
import { order, stats, visibleFiles } from "./order";
import { scenarioInsight, STORY_FILES } from "../demo-scenario";
import { buildRows, byHunk } from "./rows";

// demo.patch is data a person edits by hand, so what is pinned here is the ways it can go
// quietly wrong: an annotation for a path the patch does not carry (the file then renders with
// no chips and no ranking, and nothing says so), or a hunk index a comment cannot reach.
//
// It reads the GENERATED changeset rather than the patch, because that is what the showcase
// actually renders. A hand-counted @@ header no longer needs pinning here - the same Go reader
// the product uses produces this, so a header that disagrees with its body is its problem now.

const files = fromWire(DEMO_FILES);
const session = demoSession();

test("the generated changeset holds the files the showcase claims", () => {
  assert.deepEqual(
    files.map((f) => f.path),
    [
      "libs/authkit/claims.go",
      "libs/authkit/audience.go",
      "libs/authkit/testdata/claims.golden",
      "services/identity/internal/token/verify.go",
      "services/gateway/internal/mint/token.go",
      "services/identity/internal/token/legacy_audience.go",
      "apps/dashboard/src/api/session.ts",
      "services/identity/internal/token/verify_test.go",
      "services/gateway/internal/mint/token_test.go",
      "tools/migrate/backfill.sh",
      "docs/auth/tokens.md",
      "libs/protocol/gen/token_pb.go",
      "apps/dashboard/src/gen/session_pb.ts",
      "docs/gen/auth/tokens.html",
    ],
  );
  const byPath = new Map(files.map((f) => [f.path, f]));
  assert.equal(byPath.get("libs/authkit/audience.go")?.status, "added");
  assert.equal(
    byPath.get("services/identity/internal/token/legacy_audience.go")?.status,
    "deleted",
  );
  const doc = byPath.get("docs/auth/tokens.md");
  assert.equal(doc?.status, "renamed");
  assert.equal(doc?.oldPath, "docs/auth/jwt.md");
});

// The two files that carry NO hunks, for two different reasons. Both render as an empty entry
// unless the surface reads why they are empty, and "nothing changed" is false in both cases.
test("the changeset exercises the states that produce no hunks", () => {
  const byPath = new Map(files.map((f) => [f.path, f]));

  const golden = byPath.get("libs/authkit/testdata/claims.golden");
  assert.equal(golden?.binary, true, "a binary file the surface must name as binary");
  assert.deepEqual(golden?.hunks, []);

  const script = byPath.get("tools/migrate/backfill.sh");
  assert.equal(script?.binary, false, "a mode change is not a binary payload");
  assert.deepEqual(script?.hunks, []);
  assert.equal(script?.oldMode, "100644");
  assert.equal(script?.newMode, "100755");
});

// Every hand-written @@ header states a line count. Nothing in the reader verifies them - Go
// parses the body and takes the header's word for the numbers - so a header that has drifted
// from its body is invisible until a reader notices the gutter numbers are wrong, which is
// exactly the kind of detail the showcase is read closely for.
//
// It has caught two now: a hand-counted header, and the SCRIPT written to stop hand-counting
// them, which read the trailing newline as a context line.
test("every hunk header's counts match the lines under it", () => {
  const wrong: string[] = [];
  for (const f of files) {
    for (const h of f.hunks) {
      const olds = h.lines.filter((l) => l.kind === "context" || l.kind === "del").length;
      const news = h.lines.filter((l) => l.kind === "context" || l.kind === "add").length;
      if (olds !== h.oldCount || news !== h.newCount) {
        wrong.push(
          `${f.path} ${h.header} counts ${h.oldCount}/${h.newCount}, body ${olds}/${news}`,
        );
      }
    }
  }
  assert.deepEqual(wrong, []);
});

test("every annotated path is in the patch, and every patched path is annotated", () => {
  const patched = new Set(files.map((f) => f.path));
  const annotated = new Set((session.diff.files ?? []).map((a) => a.path));
  for (const p of annotated) assert.ok(patched.has(p), `annotated but not in the patch: ${p}`);
  for (const p of patched) assert.ok(annotated.has(p), `in the patch but not annotated: ${p}`);
});

test("every comment and suggestion anchors to a hunk that exists", () => {
  const hunks = new Map(files.map((f) => [f.path, f.hunks.length]));
  for (const c of session.comments ?? []) {
    assert.ok((hunks.get(c.path) ?? 0) > c.hunk, `comment ${c.id} anchors past ${c.path}`);
  }
  for (const s of session.suggestions ?? []) {
    assert.ok(hunks.has(s.path), `suggestion ${s.id} names a path outside the patch`);
    assert.ok((hunks.get(s.path) ?? 0) > s.hunk, `suggestion ${s.id} anchors past ${s.path}`);
  }
});

// The three things the showcase exists to show off: the generated fold, consequence order, and
// the evidence chips having something to say.
test("the demo changeset folds its generated files and leads with the widest one", () => {
  const cs = order(files, session);
  assert.deepEqual(
    cs.generated.map((o) => o.file.path),
    [
      "libs/protocol/gen/token_pb.go",
      "apps/dashboard/src/gen/session_pb.ts",
      "docs/gen/auth/tokens.html",
    ],
  );
  assert.equal(cs.primary[0]?.file.path, "libs/authkit/claims.go");
  const s = stats(cs);
  assert.equal(s.files, 11);
  assert.equal(s.generated, 3);
  assert.equal(s.publicSurface, 1);
  assert.equal(s.untested, 1);
  assert.ok(s.additions > 0 && s.deletions > 0);
});

// The whole point of the wiring: the payload reaches the row model the virtualizer paints.
test("demo mode builds rows from the demo payload", () => {
  const cs = order(files, session);
  const touches = new Map(
    (session.diff.files ?? [])
      .filter((a) => a.touches?.length)
      .map((a) => [a.path, a.touches ?? []]),
  );
  const rows = buildRows(
    visibleFiles(cs, false),
    "unified",
    byHunk(session.comments ?? []),
    touches,
  );
  assert.deepEqual(
    rows.filter((r) => r.kind === "file").map((r) => r.file.path),
    cs.primary.map((o) => o.file.path),
  );
  // A story row for each file the trail says an agent wrote, and a comment row under each
  // annotated hunk - both are rows, which is what makes them scroll with the code.
  assert.equal(rows.filter((r) => r.kind === "story").length, 4);
  assert.equal(rows.filter((r) => r.kind === "comment").length, 3);
  assert.ok(rows.some((r) => r.kind === "line" && r.line.text.includes("Audience Audience")));
});

test("the ranking key is present, so the surface may claim an order", () => {
  assert.ok((session.diff.files ?? []).some((f) => f.reach !== null && f.reach !== undefined));
});

test("applyDemoOp marks a hunk read and unread", () => {
  const on = applyDemoOp(session, { op: "viewed", digest: "abc123", on: true });
  assert.deepEqual(on.viewed, ["abc123"]);
  assert.deepEqual(applyDemoOp(on, { op: "viewed", digest: "abc123", on: false }).viewed, []);
});

test("applyDemoOp posts a comment as the human, whatever the payload says", () => {
  const after = applyDemoOp(session, {
    op: "comment",
    path: "libs/authkit/audience.go",
    hunk: 0,
    body: "Accepts is O(n) - fine for two audiences.",
  });
  const posted = (after.comments ?? []).at(-1);
  assert.equal(posted?.author, "human");
  assert.equal(posted?.path, "libs/authkit/audience.go");
  assert.equal(posted?.resolved, false);
  assert.equal((session.comments ?? []).length + 1, (after.comments ?? []).length);
});

test("applyDemoOp resolves a comment and answers a suggestion", () => {
  const resolved = applyDemoOp(session, { op: "resolve", id: "cm1", on: true });
  assert.equal((resolved.comments ?? []).find((c) => c.id === "cm1")?.resolved, true);

  const skipped = applyDemoOp(session, { op: "answer", id: "sg1", on: false });
  const sg1 = (skipped.suggestions ?? []).find((s) => s.id === "sg1");
  assert.equal(sg1?.declined, true);
  assert.equal(sg1?.accepted, false);
});

// The annotations here restate figures that demo-scenario.ts also publishes, and until now the
// only thing keeping them equal was a comment saying they were. That is the arrangement this
// session spent its day removing everywhere else: a stated invariant with nothing enforcing it.
//
// It matters because the two are shown side by side. The Insight surface reports libs/authkit as
// the workspace's top hotspot with 46 commits across 2 authors; the Diff surface annotates the
// same file in the same session. A reader who compares them and finds different numbers has
// caught the showcase lying, and will reasonably assume the product does too.
test("the diff annotations agree with the figures every other surface reports", () => {
  const insight = scenarioInsight(Date.now());
  const claims = insight.hotspots.find((h) => h.name === STORY_FILES.CLAIMS);
  assert.ok(claims, "the scenario no longer ranks the file the story turns on");

  const annotated = files.find((f) => f.path === STORY_FILES.CLAIMS);
  assert.ok(annotated, "the changeset no longer carries the file the story turns on");

  const note = (session.diff.files ?? []).find((a) => a.path === STORY_FILES.CLAIMS);
  assert.ok(note, "the annotations no longer cover the file the story turns on");
  assert.equal(note.churn?.commits, claims.churn, "churn disagrees with the Insight surface");
  assert.equal(
    note.churn?.authors,
    claims.authors,
    "author count disagrees with the Insight surface",
  );
  assert.equal(
    note.reach,
    claims.blastRadius,
    "reach disagrees with the blast radius the Insight surface reports",
  );
});
