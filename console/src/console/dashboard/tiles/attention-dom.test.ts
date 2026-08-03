// attention-dom.test.ts - the attention hero's failure surface: which targets are failing, what you
// can do about each one, and the commands offered for doing it.
//
// These are pinned by test rather than by looking at the board because a failure is INTERMITTENT in
// the demo feed - it depends on a flaky target being scheduled and then losing a coin flip - so
// "open it and check" verifies nothing on any particular run. The commands especially: a wrong
// `magus run` line is copied, pasted into a terminal, and fails there, which is worse than not
// offering one.

import assert from "node:assert/strict";
import { test } from "node:test";
import {
  countFailing,
  failingTargets,
  inspectCommand,
  reproduceCommand,
  verdictFor,
} from "./attention";
import type { StatusView } from "../state";

// statusWith builds the minimum StatusView the hero reads. Only the fields under test are
// meaningful; the rest satisfy the type.
function statusWith(targets: { label: string; state: string; ref?: string }[]): StatusView {
  return {
    health: { label: "healthy", cls: "ok" },
    pool: { capacity: 8, running: 1, queued: 0, mode: "" },
    cache: { hits: 0, misses: 0, errors: 0, hitRate: null, sizeBytes: 0 },
    runningTargets: [],
    runs: [
      {
        inv: "inv123",
        trigger: "ci",
        targets: targets.map((t) => ({
          project: t.label.includes(":") ? t.label.split(":")[0] : "",
          target: t.label.includes(":") ? t.label.split(":")[1] : t.label,
          label: t.label,
          state: t.state as never,
          terminal: t.state !== "running" && t.state !== "queued",
          startMs: 1,
          endMs: 2,
          outputRef: t.ref ?? "",
          durationMs: 1,
        })),
      },
    ],
    workspaces: [],
    services: [],
    locks: [],
    magusVersion: "",
    daemonVersion: "",
  };
}

test("failingTargets names every failure and carries its handles", () => {
  const s = statusWith([
    { label: "svc/api:test", state: "failed", ref: "out1a2b" },
    { label: "web/app:build", state: "passed", ref: "out9z9z" },
    { label: "lib/core:test", state: "failed" }, // failed before producing a ref
  ]);
  const failing = failingTargets(s);
  assert.deepEqual(failing, [
    { label: "svc/api:test", inv: "inv123", outputRef: "out1a2b" },
    { label: "lib/core:test", inv: "inv123", outputRef: "" },
  ]);
  // The list and the count must never disagree - the count is what the reader trusts, and a list
  // shorter than it reads as "these are all of them".
  assert.equal(failing.length, countFailing(s));
});

test("a target that failed without an output ref is still listed", () => {
  // Dropping it would make the count say two and the list show one, with nothing saying which was
  // withheld. Its run id is the fallback handle.
  const s = statusWith([{ label: "lib/core:test", state: "failed" }]);
  assert.equal(failingTargets(s)[0].inv, "inv123");
});

test("reproduceCommand puts the target first and the project second", () => {
  // magus's own argument order: `magus run <target> [<project>]`. Reversed, the command fails in
  // the terminal the reader pasted it into, which is worse than offering nothing.
  assert.equal(reproduceCommand("svc/api:test"), "magus run test svc/api");
  assert.equal(reproduceCommand("web/app:build"), "magus run build web/app");
});

test("reproduceCommand omits the project for a root target", () => {
  // The root project renders as "." in a label, but `magus run lint .` is noise next to
  // `magus run lint`, and a bare target has no project part at all.
  assert.equal(reproduceCommand(".:lint"), "magus run lint");
  assert.equal(reproduceCommand("lint"), "magus run lint");
});

test("inspectCommand is empty when there is no ref to fetch", () => {
  assert.equal(inspectCommand("out1a2b"), "magus query output out1a2b");
  assert.equal(inspectCommand(""), "", "an unfetchable ref must not become an offered command");
});

test("the verdict and the failure list agree about whether anything is wrong", () => {
  const clear = statusWith([{ label: "svc/api:test", state: "passed" }]);
  assert.equal(verdictFor(clear, countFailing(clear)).state, "clear");
  assert.equal(failingTargets(clear).length, 0);

  const broken = statusWith([{ label: "svc/api:test", state: "failed" }]);
  assert.equal(verdictFor(broken, countFailing(broken)).state, "attention");
  assert.equal(failingTargets(broken).length, 1);
});
