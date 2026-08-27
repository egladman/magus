import { must } from "../../lib/guards";
// demo.ts - the daemon-free showcase (#demo). Replay the shared scenario (demo-scenario.ts) as two
// magus.viewer.v1alpha1 Journals and REVEAL the primary one incrementally so the page feels like a live run
// streaming in. The primary (streamed) invocation is the failing services/identity:test run an agent
// kicked off ~92m ago - the beat where a libs/authkit contract change takes down a downstream token
// verifier; the completed sibling shown alongside it is the earlier `magus affected ci` sweep, mostly
// cache hits. So the viewer shows a FAIL, cached targets (one replaying stored output, one silent
// because a green `go build` prints nothing) and passed targets on one axis - and, crucially, the SAME
// runs (out92e1d4, outci7a*) the recent-runs tree, the activity trail, and the dashboard point at.
//
// It reuses the live-stream buffer (state.liveEvents / liveInvocation) and scheduleLiveRender end to
// end - the synthetic events are the SAME Event messages the wire carries (built with
// create(EventSchema, ...)), so buildModelFromEvents (pretty view), buildSpans, renderWaterfall,
// waterfallSource, and updateTimelineControl all run exactly as they would for a real journal. No new
// render path, no transport, nothing fetched.

import { create } from "@bufbuild/protobuf";
import type { Journal } from "@wire/viewer/v1alpha1/viewer_pb";
import {
  EventSchema,
  JournalSchema,
  Kind,
  Status,
  Stream,
  Trigger,
} from "@wire/viewer/v1alpha1/viewer_pb";
import { state } from "./state";
import { emptyEl, setRefIdentity } from "./dom";
import { tsMs } from "./waterfall";
import { scheduleLiveRender, setLiveStatus } from "./live";
import {
  scenarioInvocations,
  scenarioRuns,
  INV_TEST_BREAK,
  INV_CI,
  WORKSPACE_ROOT,
  type RunState,
  type ScenarioRun,
} from "../demo-scenario";

// demoTs / demoDur build protobuf Timestamp / Duration inits ({seconds: bigint, nanos}) from a
// millisecond value, the shapes tsMs / durMs decode.
function demoTs(ms: number): { seconds: bigint; nanos: number } {
  return { seconds: BigInt(Math.floor(ms / 1000)), nanos: Math.floor((ms % 1000) * 1e6) };
}
function demoDur(ms: number): { seconds: bigint; nanos: number } {
  return { seconds: BigInt(Math.floor(ms / 1000)), nanos: Math.floor((ms % 1000) * 1e6) };
}

const STATUS: Record<RunState, Status> = {
  passed: Status.PASS,
  failed: Status.FAIL,
  cached: Status.CACHED,
};

// A run laid onto the invocation's internal waterfall timeline: its start offset (ms from the
// invocation base) so the target windows cascade rather than all beginning at zero.
interface PlacedRun {
  run: ScenarioRun;
  start: number;
}

// buildJournal renders one invocation's placed runs into a Journal: a STARTED/SCOPE preamble, then
// per target its EXEC step markers (each with an OUTPUT line staggered across the target's window)
// and a terminal RESULT (status + duration + the run's real ref), then FINISHED. Start offsets and
// the runs' own durations overlap into a real cascade; the Invocation start/end frame the axis. The
// base is Date.now() so the reveal reads as "now"; the scenario's ref/status/output text carry the
// story. Events are sorted by time so the pretty view and the waterfall both fill in as they arrive.
function buildJournal(
  placed: PlacedRun[],
  invId: string,
  // The wire Command's own shape. It used to take {verb, args}, which are not fields of the proto
  // message - create() dropped them, so every demo journal carried an EMPTY argv and the lineage
  // line rendered as a bare "magus run" no matter which run it was.
  command: { arguments: string[]; cwd: string; trigger: Trigger },
  scopeText: string,
): Journal {
  const base = Date.now();
  const events = [];
  events.push(
    create(EventSchema, { kind: Kind.STARTED, time: demoTs(base), command, magusVersion: "demo" }),
  );
  events.push(create(EventSchema, { kind: Kind.SCOPE, time: demoTs(base), text: scopeText }));

  let maxEnd = 0;
  let anyFail = false;
  for (const { run, start } of placed) {
    const at0 = base + start;
    const execs = run.execs ?? [];
    for (let i = 0; i < execs.length; i++) {
      const at = at0 + Math.round((run.durationMs * i) / Math.max(1, execs.length));
      events.push(
        create(EventSchema, {
          kind: Kind.EXEC,
          time: demoTs(at),
          project: run.project,
          target: run.target,
          text: execs[i],
        }),
      );
    }
    const end = at0 + run.durationMs;
    maxEnd = Math.max(maxEnd, end - base);
    // Captured output is STAGGERED across the target's window rather than stamped at one instant.
    // A real tool dribbles its lines out while it works, and the reveal below only reads as live if
    // the timestamps say so - stamped together, a dozen lines land in one frame and the waterfall
    // gains a dozen coincident markers. stdout first, then stderr: a tool prints its failure block
    // last, and that is the part a reader scrolls to.
    const lines = [
      ...(run.stdout
        ? run.stdout.split("\n").map((text) => ({ stream: Stream.STDOUT, text }))
        : []),
      ...(run.stderr
        ? run.stderr.split("\n").map((text) => ({ stream: Stream.STDERR, text }))
        : []),
    ];
    lines.forEach((line, n) => {
      events.push(
        create(EventSchema, {
          kind: Kind.OUTPUT,
          // Spread strictly INSIDE (at0, end) so no output line can sort after its own RESULT.
          time: demoTs(at0 + Math.round((run.durationMs * (n + 1)) / (lines.length + 1))),
          project: run.project,
          target: run.target,
          stream: line.stream,
          text: line.text,
        }),
      );
    });
    if (run.state === "failed") anyFail = true;
    events.push(
      create(EventSchema, {
        kind: Kind.RESULT,
        time: demoTs(end),
        project: run.project,
        target: run.target,
        status: STATUS[run.state],
        ref: run.ref,
        duration: demoDur(run.durationMs),
      }),
    );
  }
  events.push(
    create(EventSchema, {
      kind: Kind.FINISHED,
      time: demoTs(base + maxEnd),
      level: anyFail ? "error" : "info",
    }),
  );
  events.sort((a, b) => (tsMs(a.time) || 0) - (tsMs(b.time) || 0));

  const invocation = {
    id: invId,
    command,
    startTime: demoTs(base),
    endTime: demoTs(base + maxEnd),
    magusVersion: "demo",
  };
  return create(JournalSchema, { invocation, events });
}

// brokenTestJournal is the primary streamed invocation: the agent-driven services/identity:test run
// that FAILs.
function brokenTestJournal(): Journal {
  const runs = scenarioRuns(Date.now());
  const test = must(runs.find((r) => r.inv === INV_TEST_BREAK));
  return buildJournal(
    [{ run: test, start: 0 }],
    INV_TEST_BREAK,
    {
      arguments: ["run", "test", "services/identity"],
      cwd: WORKSPACE_ROOT,
      trigger: Trigger.RUN,
    },
    "projects: services/identity (cwd)",
  );
}

// ciSweepJournal is the completed sibling invocation: the earlier `magus affected ci` sweep, mostly
// cache hits, all green - so the viewer shows cached/passed targets beside the FAIL.
function ciSweepJournal(): Journal {
  const runs = scenarioRuns(Date.now()).filter((r) => r.inv === INV_CI);
  // Stagger the sweep's targets so their windows cascade instead of stacking at zero.
  const offsets = [0, 250, 400, 700];
  const placed = runs.map((run, i) => ({ run, start: offsets[i] ?? i * 300 }));
  return buildJournal(
    placed,
    INV_CI,
    { arguments: ["affected", "ci"], cwd: WORKSPACE_ROOT, trigger: Trigger.CI },
    "projects: services/identity, apps/dashboard, libs/authkit, . (affected)",
  );
}

// demoJournal builds ONE invocation's journal from the shared scenario, for the run browser. The
// daemon-free showcase has no /api/v1/run to read, and a browser whose rows do not open is a picture
// of a feature rather than the feature - so the same buildJournal the streamed demo uses renders any
// row the tree lists. Returns null for an id the scenario does not know.
//
// The targets cascade on a fixed 300ms stagger rather than replaying real scheduling: the scenario
// records each run's duration, not when the engine started it, so any placement is a rendering
// choice and a regular one reads as a waterfall instead of as noise.
export function demoJournal(inv: string): Journal | null {
  const now = Date.now();
  const meta = scenarioInvocations(now).find((i) => i.inv === inv);
  const runs = scenarioRuns(now).filter((r) => r.inv === inv);
  if (!meta || !runs.length) return null;
  return buildJournal(
    runs.map((run, i) => ({ run, start: i * 300 })),
    inv,
    {
      arguments: meta.arguments,
      cwd: WORKSPACE_ROOT,
      trigger: TRIGGERS[meta.trigger] ?? Trigger.RUN,
    },
    "projects: " + [...new Set(runs.map((r) => r.project))].join(", "),
  );
}

// TRIGGERS maps the scenario's trigger vocabulary onto the wire enum.
const TRIGGERS: Record<string, Trigger> = {
  run: Trigger.RUN,
  affected: Trigger.AFFECTED,
  ci: Trigger.CI,
  direct: Trigger.DIRECT,
};

// startDemo enters the showcase: it frames the axis from the failing-test Invocation, opens the
// waterfall, streams that invocation's events in over a few seconds via the shared live buffer, and
// renders the completed CI sweep as its own group on the shared axis.
export function startDemo(): void {
  const journal = brokenTestJournal();
  const ordered = journal.events;
  const sibling = ciSweepJournal();

  stopDemo();
  state.demoActive = true;
  state.liveEvents = [];
  state.liveInvocation = journal.invocation ?? null; // frames the axis (start/end) and the command preamble
  state.livePaused = false;
  state.currentJournal = null; // waterfallSource then reads the live buffer, as in a real live run
  state.currentJournals = [sibling]; // the completed sibling invocation, rendered as its own group
  state.timeline = true; // open straight into the waterfall so it visibly fills in
  state.currentRef = "";
  if (emptyEl) emptyEl.hidden = true;
  // No real identity in demo mode - an empty value hides the ref pill entirely (setRefIdentity)
  // rather than showing a bordered box around the literal word "demo".
  setRefIdentity("", false);
  setLiveStatus("streaming");

  // Prime the first frame with enough events that a target span exists immediately (so the
  // waterfall is never momentarily empty): the STARTED/SCOPE preamble plus the first EXEC.
  let i = 0;
  const prime = Math.min(3, ordered.length);
  for (; i < prime; i++) state.liveEvents.push(ordered[i]);
  scheduleLiveRender();

  const BATCH = 3;
  const TICK_MS = 360;
  state.demoTimer = window.setInterval(() => {
    if (i >= ordered.length) {
      // Everything is already revealed and rendered by the prior batch tick; just settle the
      // status pill. (Re-rendering here would race scheduleLiveRender's rAF back to "streaming".)
      stopDemo();
      setLiveStatus("done");
      return;
    }
    for (let k = 0; k < BATCH && i < ordered.length; k++) state.liveEvents.push(ordered[i++]);
    scheduleLiveRender();
  }, TICK_MS);

  // Clear the reveal if the page is being torn down, so no timer fires after navigation.
  window.addEventListener("pagehide", stopDemo, { once: true });
}

export function stopDemo(): void {
  if (state.demoTimer !== null) {
    window.clearInterval(state.demoTimer);
    state.demoTimer = null;
  }
  state.demoActive = false;
}
