import { test } from "node:test";
import assert from "node:assert/strict";
import { order, visibleFiles, stats, riskChips } from "./order";
import type { DiffFile } from "./parse";
import type { DiffAnnotation, DiffSession } from "./session";

function file(path: string, additions = 1, deletions = 0): DiffFile {
  return {
    path,
    oldPath: path,
    status: "modified",
    hunks: [],
    additions,
    deletions,
    binary: false,
  };
}

function ann(path: string, over: Partial<DiffAnnotation> = {}): DiffAnnotation {
  return { path, role: "source", reach: 0, surface: "unknown", ...over };
}

function session(files: DiffAnnotation[]): DiffSession {
  return {
    id: "rev1",
    base: "working",
    diff: { base: "working", files },
    cursor: { hunk: -1 },
  };
}

test("the server's order wins, not the patch's", () => {
  const files = [file("a.ts"), file("b.ts"), file("c.ts")];
  const cs = order(files, session([ann("c.ts"), ann("a.ts"), ann("b.ts")]));
  assert.deepEqual(
    cs.primary.map((o) => o.file.path),
    ["c.ts", "a.ts", "b.ts"],
  );
});

// The headline feature: a declared output is a machine's restatement of a change made
// elsewhere, so it leaves reading order entirely.
test("generated files are split out of the primary list", () => {
  const files = [file("src/x.ts"), file("gen/index.html"), file("MAGUS.md")];
  const cs = order(
    files,
    session([
      ann("src/x.ts"),
      ann("gen/index.html", { role: "output" }),
      ann("MAGUS.md", { role: "output" }),
    ]),
  );
  assert.deepEqual(
    cs.primary.map((o) => o.file.path),
    ["src/x.ts"],
  );
  assert.deepEqual(
    cs.generated.map((o) => o.file.path),
    ["gen/index.html", "MAGUS.md"],
  );
});

test("the generated group is hidden until expanded", () => {
  const files = [file("src/x.ts"), file("gen/out.js")];
  const cs = order(files, session([ann("src/x.ts"), ann("gen/out.js", { role: "output" })]));
  assert.deepEqual(
    visibleFiles(cs, false).map((f) => f.path),
    ["src/x.ts"],
  );
  assert.deepEqual(
    visibleFiles(cs, true).map((f) => f.path),
    ["src/x.ts", "gen/out.js"],
  );
});

// The one failure a review must not have: hiding a real change because an annotation was
// missing.
test("a file the review never mentioned is still shown, at the end", () => {
  const files = [file("known.ts"), file("mystery.ts")];
  const cs = order(files, session([ann("known.ts")]));
  assert.deepEqual(
    cs.primary.map((o) => o.file.path),
    ["known.ts", "mystery.ts"],
  );
  assert.equal(cs.primary[1]?.annotation, undefined);
});

// First paint happens before the annotations land, and must render the whole patch.
test("with no session at all every file is primary in patch order", () => {
  const files = [file("a.ts"), file("b.ts")];
  const cs = order(files, null);
  assert.deepEqual(
    cs.primary.map((o) => o.file.path),
    ["a.ts", "b.ts"],
  );
  assert.equal(cs.generated.length, 0);
});

test("an annotated path absent from the patch is skipped rather than faked", () => {
  const cs = order([file("a.ts")], session([ann("a.ts"), ann("scoped-out.ts")]));
  assert.equal(cs.primary.length, 1);
});

// Progress has to mean progress through what the reader is responsible for.
test("stats exclude generated files from the count", () => {
  const files = [file("a.ts", 5, 2), file("gen/x.js", 900, 900)];
  const cs = order(files, session([ann("a.ts"), ann("gen/x.js", { role: "output" })]));
  const s = stats(cs);
  assert.equal(s.files, 1);
  assert.equal(s.generated, 1);
  assert.equal(s.additions, 5, "a generated file's churn must not inflate the diff size shown");
  assert.equal(s.deletions, 2);
});

test("stats count public surface", () => {
  const cs = order(
    [file("a.ts"), file("b.ts")],
    session([ann("a.ts", { surface: "public" }), ann("b.ts", { surface: "internal" })]),
  );
  assert.equal(stats(cs).publicSurface, 1);
});

// "Nobody measured it" and "it has no tests" are different facts.
test("untested counts measured zero, never unmeasured", () => {
  const cs = order(
    [file("measured.ts"), file("unmeasured.ts")],
    session([
      ann("measured.ts", { coverage: { ratio: 0, covered_stmts: 0, total_stmts: 12 } }),
      ann("unmeasured.ts", { coverage: { ratio: 0, covered_stmts: 0, total_stmts: 0 } }),
    ]),
  );
  assert.equal(stats(cs).untested, 1);
});

test("risk chips state facts and name the API", () => {
  const chips = riskChips(
    ann("x.go", {
      surface: "public",
      reach: 43,
      coverage: { ratio: 0.62, covered_stmts: 62, total_stmts: 100 },
      symbols: [
        {
          id: "s",
          label: "Open",
          ref_count: 90,
          file_count: 43,
          external_file_count: 0,
          module_api: true,
        },
      ],
    }),
  );
  assert.deepEqual(
    chips.map((c) => c.text),
    ["public surface", "43 referents", "62% covered"],
  );
  assert.match(chips[0]?.title ?? "", /Open/);
});

// The circling detector. Hot AND accelerating is the combination worth interrupting for;
// either alone is ordinary.
test("a hot, accelerating file reads as rising", () => {
  const chips = riskChips(
    ann("hot.go", { churn: { commits: 40, authors: 3, score: 900, rank: 2, project_trend: 12 } }),
  );
  const churn = chips.find((c) => c.text.includes("commit"));
  assert.equal(churn?.text, "40 commits, 3 authors and rising");
  assert.equal(churn?.tone, "danger");
  assert.match(churn?.title ?? "", /why it keeps changing/);
});

// A deep rank is honest data and meaningless display - a field that is usually noise is a
// field the reader learns to skip.
test("a deeply ranked file reports its commits without the rank", () => {
  const chips = riskChips(
    ann("cold.go", { churn: { commits: 2, score: 4, rank: 1278, project_trend: 5 } }),
  );
  const churn = chips.find((c) => c.text.includes("commit"));
  assert.equal(churn?.text, "2 commits");
  assert.equal(churn?.tone, "neutral");
  assert.doesNotMatch(churn?.title ?? "", /1278/);
});

test("a hot file in a cooling project is not rising", () => {
  const chips = riskChips(
    ann("hot.go", { churn: { commits: 30, score: 500, rank: 3, project_trend: -8 } }),
  );
  const churn = chips.find((c) => c.text.includes("commit"));
  assert.doesNotMatch(churn?.text ?? "", /rising/);
  assert.equal(churn?.tone, "warn");
});

test("one commit is singular", () => {
  const chips = riskChips(ann("new.go", { churn: { commits: 1, score: 1 } }));
  assert.equal(chips.find((c) => c.text.includes("commit"))?.text, "1 commit");
});

// Nil churn means nobody measured, which must not render as a quiet file.
test("no churn data renders no churn chip", () => {
  const chips = riskChips(ann("x.go", { reach: 3 }));
  assert.equal(chips.filter((c) => c.text.includes("commit")).length, 0);
});

test("no annotation yields no chips rather than empty placeholders", () => {
  assert.deepEqual(riskChips(undefined), []);
});

// A file nobody measured must not render a coverage chip at all - a "0% covered" badge on
// unmeasured code is the fastest way to make the whole rail untrustworthy.
test("unmeasured coverage renders no coverage chip", () => {
  const chips = riskChips(
    ann("x.go", { coverage: { ratio: 0, covered_stmts: 0, total_stmts: 0 } }),
  );
  assert.equal(chips.filter((c) => c.text.includes("covered")).length, 0);
});

// Read state is a FINDING, not a progress bar. "stale" is the one nothing else can tell the
// reader: those files look finished, so they are the ones a scroll will skip.
test("a file that changed after it was read leads its chips", () => {
  const chips = riskChips(ann("x.go", { read_state: "stale", surface: "public" }));
  assert.equal(chips[0]?.text, "changed since read");
  assert.equal(chips[0]?.tone, "danger");
  assert.match(chips[0]?.title ?? "", /not the version you are about to land/);
});

test("a file covered by a receipt says so quietly", () => {
  const chips = riskChips(ann("x.go", { read_state: "read" }));
  assert.equal(chips[0]?.text, "read");
  assert.equal(chips[0]?.tone, "ok");
});

// Most files in most changesets are unread, so a chip there would sit on nearly every row and
// teach the eye to skip the rail that carries public-surface and reach. An ABSENT state is a
// different claim again - nobody checked - and must not render as unread either.
test("unread and unmeasured show no chip at all", () => {
  assert.deepEqual(
    riskChips(ann("x.go", { read_state: "unread" })).map((c) => c.text),
    [],
  );
  assert.deepEqual(
    riskChips(ann("x.go", {})).map((c) => c.text),
    [],
  );
});
