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
import type { AttentionRequest } from "./attentionQueue";
import type { StatusView } from "../state";

// request builds one open queue row. Only opened_ms varies across these tests; the rest satisfy
// the wire shape.
function request(openedMs: number): AttentionRequest {
  return {
    id: "att-0123456789ab",
    session: "agent-1",
    opened_ms: openedMs,
    outcome: "waiting",
    severity: "",
    source: "claude/Notification",
    where: "/repo",
    lease: "",
    message: "needs the deploy key",
  };
}

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

// The verdict reads the attention QUEUE and nothing else. This is the regression the tile was
// rewritten for: it used to derive "Attention needed" from the failing count, so the board could
// shout over an empty queue, or read "All clear" while agents sat blocked. Two things called
// attention on one screen, and no way to tell which one was lying.
test("the verdict comes from the queue, never from failing targets", () => {
  const broken = statusWith([{ label: "svc/api:test", state: "failed" }]);
  assert.equal(failingTargets(broken).length, 1, "the run really is failing");
  // A failing target is not a request. Nobody has been asked for anything, so nobody is waiting.
  assert.equal(verdictFor({ kind: "ok", requests: [], store: "/s" }).state, "clear");

  // ...and a request waiting is attention even with a perfectly green board.
  const clear = statusWith([{ label: "svc/api:test", state: "passed" }]);
  assert.equal(countFailing(clear), 0);
  const waiting = verdictFor({ kind: "ok", requests: [request(0)], store: "/s" });
  assert.equal(waiting.state, "attention");
  assert.equal(waiting.line, "1 request waiting");
});

// An unknown queue must never render as a calm one. "absent" and "unreadable" are the two reads
// where the tile does not KNOW whether anyone is blocked, and showing the good state for either is
// indistinguishable from nobody waiting - which is the one thing this tile must not say by mistake.
test("a queue that could not be read does not read as an empty one", () => {
  assert.equal(verdictFor({ kind: "absent" }).state, "warn");
  assert.equal(verdictFor({ kind: "unreadable", detail: "boom" }).state, "warn");
  assert.notEqual(
    verdictFor({ kind: "absent" }).line,
    verdictFor({ kind: "ok", requests: [], store: "/s" }).line,
  );
});

test("the verdict names how long the oldest request has waited", () => {
  const now = 10 * 60 * 1000;
  const v = verdictFor({ kind: "ok", requests: [request(now - 5 * 60 * 1000)], store: "/s" }, now);
  assert.match(v.sub, /waiting 5m/);
});
