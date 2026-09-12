import { test } from "node:test";
import assert from "node:assert/strict";
import { JobHolder } from "@wire/job/v1alpha1/job_pb";
import { demoJobs, demoOverlaps } from "./demo";
import { buildJobTree, isTerminal, normalizeState, JOB_STATES, type JobState } from "./jobs";

// The showcase is a first impression, and these pin the claims its header comment makes: that both
// holders are in it, that it exercises every state, that both warnings are reachable, and that it
// stays put across reads.

const NOW = 1_760_000_000_000;

test("the demo carries both holders, which is the claim the view makes", () => {
  const holders = new Set(demoJobs(NOW).map((j) => j.holder));
  assert.ok(holders.has(JobHolder.DAEMON), "the daemon's own maintenance is part of the picture");
  assert.ok(holders.has(JobHolder.SESSION), "and so is the work a session was handed");
});

test("the daemon's jobs carry what only they have: what it does, and how much there is", () => {
  for (const job of demoJobs(NOW).filter((j) => j.holder === JobHolder.DAEMON)) {
    assert.ok(job.description, `${job.id} says nothing about what it does`);
    assert.ok(job.target, `${job.id} maintains nothing measurable`);
  }
});

test("the session work builds one tree under a single root", () => {
  const model = buildJobTree(demoJobs(NOW), demoOverlaps());
  assert.equal(model.nodes.length, 9);
  const roots = model.nodes.filter((n) => !n.parent && n.holder === JobHolder.SESSION);
  assert.equal(roots.length, 1, "one root, so the session's work reads as a single tree");
  assert.equal(roots[0]?.id, "claims-audience");
});

test("every state the view can draw appears in the demo", () => {
  const seen = new Set<JobState>(demoJobs(NOW).map((j) => normalizeState(j.state)));
  for (const s of JOB_STATES) {
    assert.ok(seen.has(s), `the showcase must exercise the ${s} state`);
  }
});

test("the overlap names two jobs that exist and really do intersect", () => {
  const ids = new Set(demoJobs(NOW).map((j) => j.id));
  const overlaps = demoOverlaps();
  assert.ok(overlaps.length > 0, "the overlap warning must be reachable in the demo");
  for (const o of overlaps) {
    assert.ok(ids.has(o.jobA), `${o.jobA} is not a job in the demo`);
    assert.ok(ids.has(o.jobB), `${o.jobB} is not a job in the demo`);
    assert.ok(o.pathsA.length > 0 && o.pathsB.length > 0, "each side declares its own paths");
    // Not a formality: an overlap whose paths do not actually intersect would be the view crying
    // wolf in the one place a reader is asked to trust it.
    const hit = o.pathsA.some((a) => o.pathsB.some((b) => a.startsWith(b) || b.startsWith(a)));
    assert.ok(hit, `${o.jobA} and ${o.jobB} are declared to overlap but their paths do not`);
  }
});

test("a stale non-terminal job exists, since a terminal one draws no warning", () => {
  const jobs = demoJobs(NOW);
  const nowSec = Math.floor(NOW / 1000);
  const stale = jobs.filter(
    (j) => !isTerminal(normalizeState(j.state)) && nowSec - Number(j.updated) > 3600,
  );
  assert.ok(stale.length > 0, "the staleness warning must be reachable in the demo");
});

test("dependencies and parents only ever name jobs that exist", () => {
  const jobs = demoJobs(NOW);
  const ids = new Set(jobs.map((j) => j.id));
  for (const j of jobs) {
    if (j.parent) assert.ok(ids.has(j.parent), `${j.id} names a missing parent ${j.parent}`);
    for (const d of j.dependsOn) {
      assert.ok(ids.has(d), `${j.id} depends on a missing job ${d}`);
    }
  }
});

test("timestamps are epoch SECONDS in the past", () => {
  const nowSec = Math.floor(NOW / 1000);
  for (const j of demoJobs(NOW)) {
    for (const [field, v] of [
      ["created", Number(j.created)],
      ["updated", Number(j.updated)],
    ] as const) {
      // Seconds, not milliseconds: written as ms these read as dates far in the future and every
      // age label in the view silently becomes wrong rather than absent.
      assert.ok(v <= nowSec, `${j.id}.${field} is in the future`);
      assert.ok(v > nowSec - 86_400, `${j.id}.${field} looks like milliseconds`);
    }
    assert.ok(Number(j.updated) >= Number(j.created), `${j.id} was updated before it was created`);
  }
});

test("the fixture is a pure function of now, so two reads agree", () => {
  assert.deepEqual(demoJobs(NOW), demoJobs(NOW));
  assert.deepEqual(demoOverlaps(), demoOverlaps());
});
